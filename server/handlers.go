package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ── HTTP envelope ─────────────────────────────────────────────────────────────

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Success: true, Data: data})
}

// fail maps a ServiceError directly to its HTTP status and error envelope.
// All error-readable information comes from the ServiceError itself — handlers
// never construct error strings.
func fail(c *gin.Context, svcErr *ServiceError) {
	c.JSON(svcErr.HTTPStatus, Response{
		Success: false,
		Error:   &ErrorInfo{Code: svcErr.Code, Message: svcErr.Message},
	})
}

// ── handlers ──────────────────────────────────────────────────────────────────

type submitInput struct {
	Text string `json:"text" binding:"required"`
}

// handleSubmit ingests a string and returns cardinality + top-K stats.
func handleSubmit(svc *SketchService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in submitInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, &ServiceError{
				HTTPStatus: http.StatusBadRequest,
				Code:       "INVALID_REQUEST_BODY",
				Message:    "request body must be JSON with a non-empty 'text' field",
			})
			return
		}

		result, svcErr := svc.Submit(in.Text)
		if svcErr != nil {
			fail(c, svcErr)
			return
		}

		ok(c, result)
	}
}

// handleQuery returns the CMS frequency estimate for the requested string.
func handleQuery(svc *SketchService) gin.HandlerFunc {
	return func(c *gin.Context) {
		text := c.Query("text")
		if text == "" {
			fail(c, &ServiceError{
				HTTPStatus: http.StatusBadRequest,
				Code:       "MISSING_PARAM",
				Message:    "query parameter 'text' is required",
			})
			return
		}

		result, svcErr := svc.Query(text)
		if svcErr != nil {
			fail(c, svcErr)
			return
		}

		ok(c, result)
	}
}

// handleStats returns the HLL distinct-count estimate and the top-K candidate list.
func handleStats(svc *SketchService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, svcErr := svc.Stats()
		if svcErr != nil {
			fail(c, svcErr)
			return
		}

		ok(c, result)
	}
}

func handleMemoryStats(svc *SketchService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, svcErr := svc.MemoryStats()
		if svcErr != nil {
			fail(c, svcErr)
			return
		}
		ok(c, result)
	}
}
