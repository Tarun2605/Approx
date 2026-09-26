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

type SketchService struct {
	mu         sync.RWMutex
	hll        *hll2.HLL
	cms        *cms.CMS
	topK       *misra_gries.MGTopK
	totalItems int64
	totalBytes int64

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

	if err := svc.replay(); err != nil {
		return nil, fmt.Errorf("replay log: %w", err)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log for writing: %w", err)
	}

	svc.logFile = f
	svc.logBuf = bufio.NewWriter(f)

	return svc, nil
}

func (s *SketchService) replay() error {
	f, err := os.Open(logPath)
	if errors.Is(err, os.ErrNotExist) {
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
	s.totalItems++
	s.totalBytes += int64(len(text))
	return nil
}

func (s *SketchService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.logBuf.Flush(); err != nil {
		return fmt.Errorf("flush log buffer: %w", err)
	}
	return s.logFile.Close()
}

// ── result types ──────────────────────────────────────────────────────────────

type SubmitResult struct {
	ApproximateCardinality int64 `json:"approximateCardinality"`
}

type QueryResult struct {
	Text             string `json:"text"`
	ApproximateCount int    `json:"approximateCount"`
}

type StatsResult struct {
	ApproximateDistinctCount int64    `json:"approximateDistinctCount"`
	TopK                     []string `json:"topK"`
}

type MemoryStats struct {
	HLLBytes        int64 `json:"hllBytes"`
	CMSBytes        int64 `json:"cmsBytes"`
	MisraGriesBytes int64 `json:"misraGriesBytes"`

	TotalItemsReceived int64 `json:"totalItemsReceived"`
	UniqueItemCount    int64 `json:"uniqueItemCount"`
	AvgStringLenBytes  int64 `json:"avgStringLenBytes"`
	NaiveEstimateBytes int64 `json:"naiveEstimateBytes"`
}

// ── methods ───────────────────────────────────────────────────────────────────

func (s *SketchService) Submit(text string) (*SubmitResult, *ServiceError) {
	if text == "" {
		return nil, badInput("text must not be empty")
	}
	if strings.ContainsAny(text, "\n\r") {
		return nil, badInput("text must not contain newline characters")
	}

	if len(text) >= 20 {
		return nil, badInput("text must be less than 20 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ingest(text); err != nil {
		return nil, internalErr(err)
	}

	card, err := s.hll.GetCardinality()
	if err != nil {
		return nil, internalErr(err)
	}

	return &SubmitResult{
		ApproximateCardinality: card,
	}, nil
}

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

	return &QueryResult{
		Text:             text,
		ApproximateCount: count,
	}, nil
}

func (s *SketchService) Stats() (*StatsResult, *ServiceError) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	card, err := s.hll.GetCardinality()
	if err != nil {
		return nil, internalErr(err)
	}

	topKEntries, err := s.topK.ListEntries()
	if err != nil {
		return nil, internalErr(err)
	}

	return &StatsResult{
		ApproximateDistinctCount: card,
		TopK:                     topKEntries,
	}, nil
}

func (s *SketchService) MemoryStats() (*MemoryStats, *ServiceError) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var avgLen int64
	if s.totalItems > 0 {
		avgLen = s.totalBytes / s.totalItems
	}

	card, err := s.hll.GetCardinality()
	if err != nil {
		return nil, internalErr(err)
	}

	naiveBytes := card * 2 * (128 + avgLen + 8)

	return &MemoryStats{
		HLLBytes:           int64(1<<14) * 8,
		CMSBytes:           int64(30*16256)*8 + int64(30)*8,
		MisraGriesBytes:    int64(len(s.topK.Counter)) * (128 + avgLen + 8),
		TotalItemsReceived: s.totalItems,
		UniqueItemCount:    card,
		AvgStringLenBytes:  avgLen,
		NaiveEstimateBytes: naiveBytes,
	}, nil
}
