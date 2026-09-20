package main

import (
	"fmt"
	"net/http"
	"server/cms"
	hll2 "server/hll"
	"server/misra_gries"

	"github.com/gin-gonic/gin"
)

// Response is the standard API envelope.
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OK sends a 200 success response.
func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Success: true, Data: data})
}

// Fail sends an error response with the given HTTP status.
func Fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, Response{
		Success: false,
		Error:   &ErrorInfo{Code: code, Message: message},
	})
}

// ── request / response types ──────────────────────────────────────────────────

type submitInput struct {
	Text string `json:"text" binding:"required"`
}

type submitOutput struct {
	// Exact count via the in-memory set (ground truth kept server-side).
	ActualCardinality int64 `json:"actualCardinality"`
	// HyperLogLog estimate of distinct elements seen so far.
	ApproximateCardinality int64 `json:"approximateCardinality"`
	// Misra-Gries top-K candidates at the time of this request.
	ApproximateTopK []string `json:"approximateTopK"`
}

type queryOutput struct {
	// The queried string.
	Text string `json:"text"`
	// Count-Min Sketch estimate of how many times this string was submitted.
	ApproximateCount int `json:"approximateCount"`
	// Whether this string is currently tracked as a top-K candidate.
	IsTopKCandidate bool `json:"isTopKCandidate"`
	// Current top-K candidates at the time of this query.
	ApproximateTopK []string `json:"approximateTopK"`
}

// ── server ────────────────────────────────────────────────────────────────────

func main() {
	hll := hll2.NewHLL()

	cmsSketch, err := cms.NewCms()
	if err != nil {
		fmt.Println("Failed to initialise CMS: " + err.Error())
		return
	}

	topK, err := misra_gries.NewMGTopK()
	if err != nil {
		fmt.Println("Failed to initialise TopK: " + err.Error())
		return
	}

	// Exact ground-truth set (kept in memory for comparison).
	exact := make(map[string]bool)

	router := gin.Default()

	// POST /submit  {"text": "some string"}
	// Ingests a string into all three data structures and returns current stats.
	router.POST("/submit", func(c *gin.Context) {
		var in submitInput
		if err := c.ShouldBindJSON(&in); err != nil {
			Fail(c, http.StatusBadRequest, "INVALID_REQUEST_BODY",
				"request body must be JSON with a non-empty 'text' field")
			return
		}

		if _, err := hll.Add(in.Text); err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		if err := cmsSketch.Add(in.Text); err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		if _, err := topK.Add(in.Text); err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		card, err := hll.GetCardinality()
		if err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		topKEntries, err := topK.ListEntries()
		if err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		exact[in.Text] = true

		OK(c, submitOutput{
			ActualCardinality:      int64(len(exact)),
			ApproximateCardinality: card,
			ApproximateTopK:        topKEntries,
		})
	})

	// GET /query?text=<value>
	// Returns the CMS frequency estimate for the given string, whether it is
	// a top-K candidate, and the current top-K candidate list.
	router.GET("/query", func(c *gin.Context) {
		text := c.Query("text")
		if text == "" {
			Fail(c, http.StatusBadRequest, "MISSING_PARAM",
				"query parameter 'text' is required")
			return
		}

		count, err := cmsSketch.Query(text)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		topKEntries, err := topK.ListEntries()
		if err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		isTopK := false
		for _, entry := range topKEntries {
			if entry == text {
				isTopK = true
				break
			}
		}

		OK(c, queryOutput{
			Text:             text,
			ApproximateCount: count,
			IsTopKCandidate:  isTopK,
			ApproximateTopK:  topKEntries,
		})
	})

	err = router.Run(":8080")
	if err != nil {
		fmt.Println("Server crashed: " + err.Error())
	}
}
