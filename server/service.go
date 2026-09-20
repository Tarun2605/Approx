package main

import (
	"fmt"
	"net/http"
	"server/cms"
	hll2 "server/hll"
	"server/misra_gries"
)

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

// internal helpers for the two error classes this service can produce.
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
	hll   *hll2.HLL
	cms   *cms.CMS
	topK  *misra_gries.MGTopK
	exact map[string]bool // ground-truth set kept for comparison
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

	return &SketchService{
		hll:   hll2.NewHLL(),
		cms:   cmsSketch,
		topK:  topK,
		exact: make(map[string]bool),
	}, nil
}

// ── result types ──────────────────────────────────────────────────────────────

// SubmitResult is the structured output of a successful Submit call.
type SubmitResult struct {
	ActualCardinality      int64    `json:"actualCardinality"`
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

// Submit ingests text into HLL, CMS, and Misra-Gries, then returns current
// cardinality and top-K statistics.
func (s *SketchService) Submit(text string) (*SubmitResult, *ServiceError) {
	if text == "" {
		return nil, badInput("text must not be empty")
	}

	if _, err := s.hll.Add(text); err != nil {
		return nil, internalErr(err)
	}

	if err := s.cms.Add(text); err != nil {
		return nil, internalErr(err)
	}

	if _, err := s.topK.Add(text); err != nil {
		return nil, internalErr(err)
	}

	card, err := s.hll.GetCardinality()
	if err != nil {
		return nil, internalErr(err)
	}

	topKEntries, err := s.topK.ListEntries()
	if err != nil {
		return nil, internalErr(err)
	}

	s.exact[text] = true

	return &SubmitResult{
		ActualCardinality:      int64(len(s.exact)),
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
