package main

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"server/cms"
	hll2 "server/hll"
	"server/misra_gries"
	"strings"
	"sync"
)

const logPath = "submit.log"

// ── errors ────────────────────────────────────────────────────────────────────

// ServiceError carries a machine-readable code and a human-readable message.
// Handlers map it directly to the HTTP error envelope without reconstructing
// error strings.
type ServiceError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func badInput(message string) *ServiceError {
	return &ServiceError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "INVALID_INPUT",
		Message:    message,
	}
}

func internalErr(cause error) *ServiceError {
	return &ServiceError{
		HTTPStatus: http.StatusInternalServerError,
		Code:       "INTERNAL_SERVER_ERROR",
		Message:    cause.Error(),
	}
}

// ── service ───────────────────────────────────────────────────────────────────

// SketchService owns all three probabilistic data structures and exposes
// Submit / Query as pure business logic — no HTTP concerns.
type SketchService struct {
	mu   sync.RWMutex
	hll  *hll2.HLL
	cms  *cms.CMS
	topK *misra_gries.MGTopK

	// logFile is the open O_APPEND file descriptor for persistence.
	// logBuf batches writes to avoid a syscall per request.
	logFile *os.File
	logBuf  *bufio.Writer
}

func NewSketchService() (*SketchService, error) {
	cmsSketch, err := cms.NewCms()
	if err != nil {
		return nil, fmt.Errorf("initialise CMS: %w", err)
	}

	topK, err := misra_gries.NewMGTopK()
	if err != nil {
		return nil, fmt.Errorf("initialise TopK: %w", err)
	}

	svc := &SketchService{
		hll:  hll2.NewHLL(),
		cms:  cmsSketch,
		topK: topK,
	}

	// Replay any previously persisted entries before accepting new requests.
	// This rebuilds the in-memory sketches from the log file without writing
	// anything back to disk.
	if err := svc.replay(); err != nil {
		return nil, fmt.Errorf("replay log: %w", err)
	}

	// Open the log file for appending. A separate file descriptor is used here
	// (vs the one used during replay) because O_RDONLY and O_APPEND cannot be
	// mixed on the same fd safely.
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log for writing: %w", err)
	}

	svc.logFile = f
	// bufio.Writer accumulates writes in a 4 KB RAM buffer and flushes to the
	// OS in one syscall when the buffer fills up, instead of one syscall per
	// request.
	svc.logBuf = bufio.NewWriter(f)

	return svc, nil
}

// replay reads the existing log file line by line and feeds each entry into
// the in-memory sketches. It uses bufio.Scanner so only one line at a time
// lives in RAM, regardless of file size.
func (s *SketchService) replay() error {
	f, err := os.Open(logPath)
	if errors.Is(err, os.ErrNotExist) {
		// First boot — no log yet, nothing to replay.
		return nil
	}
	if err != nil {
		return fmt.Errorf("open log for replay: %w", err)
	}
	defer f.Close()

	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// ingest directly — bypasses the log write in Submit so entries
		// are not written back to the file during replay.
		if err := s.ingest(line); err != nil {
			return fmt.Errorf("replay line %q: %w", line, err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan log: %w", err)
	}

	slog.Info("replayed log", "entries", count)
	return nil
}

// ingest updates all data structures in memory. It is the single source of
// truth for what "adding a string" means. Called by both Submit (live path)
// and replay (startup path). It does NOT write to the log.
func (s *SketchService) ingest(text string) error {
	if _, err := s.hll.Add(text); err != nil {
		return err
	}
	if err := s.cms.Add(text); err != nil {
		return err
	}
	if _, err := s.topK.Add(text); err != nil {
		return err
	}
	return nil
}

// Close flushes the write buffer and closes the log file. Must be called
// after the HTTP server has shut down and all in-flight requests have finished.
func (s *SketchService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.logBuf.Flush(); err != nil {
		return fmt.Errorf("flush log buffer: %w", err)
	}
	return s.logFile.Close()
}

// ── result types ──────────────────────────────────────────────────────────────

// SubmitResult is the structured output of a successful Submit call.
type SubmitResult struct {
	ApproximateCardinality int64    `json:"approximateCardinality"`
	ApproximateTopK        []string `json:"approximateTopK"`
}

// QueryResult is the structured output of a successful Query call.
type QueryResult struct {
	Text             string   `json:"text"`
	ApproximateCount int      `json:"approximateCount"`
	IsTopKCandidate  bool     `json:"isTopKCandidate"`
	ApproximateTopK  []string `json:"approximateTopK"`
}

// ── methods ───────────────────────────────────────────────────────────────────

// Submit ingests text into all three data structures, persists it to the
// append-only log, and returns current cardinality and top-K statistics.
func (s *SketchService) Submit(text string) (*SubmitResult, *ServiceError) {
	if text == "" {
		return nil, badInput("text must not be empty")
	}
	// Newlines would corrupt the one-entry-per-line log format.
	if strings.ContainsAny(text, "\n\r") {
		return nil, badInput("text must not contain newline characters")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ingest(text); err != nil {
		return nil, internalErr(err)
	}

	// Write to log after a successful ingest. If the write fails the entry is
	// already reflected in the in-memory sketches for this session — we surface
	// the error but do not roll back (approximate structures tolerate this).
	if _, err := fmt.Fprintf(s.logBuf, "%s\n", text); err != nil {
		return nil, internalErr(fmt.Errorf("write to log: %w", err))
	}

	card, err := s.hll.GetCardinality()
	if err != nil {
		return nil, internalErr(err)
	}

	topKEntries, err := s.topK.ListEntries()
	if err != nil {
		return nil, internalErr(err)
	}

	return &SubmitResult{
		ApproximateCardinality: card,
		ApproximateTopK:        topKEntries,
	}, nil
}

// Query returns the CMS frequency estimate for text, whether it is a current
// top-K candidate, and the full candidate list.
func (s *SketchService) Query(text string) (*QueryResult, *ServiceError) {
	if text == "" {
		return nil, badInput("text must not be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	count, err := s.cms.Query(text)
	if err != nil {
		return nil, internalErr(err)
	}

	topKEntries, err := s.topK.ListEntries()
	if err != nil {
		return nil, internalErr(err)
	}

	isTopK := false
	for _, entry := range topKEntries {
		if entry == text {
			isTopK = true
			break
		}
	}

	return &QueryResult{
		Text:             text,
		ApproximateCount: count,
		IsTopKCandidate:  isTopK,
		ApproximateTopK:  topKEntries,
	}, nil
}
