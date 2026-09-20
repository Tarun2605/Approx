package main

import (
	"fmt"
	"net/http"
	"server/cms"
	hll2 "server/hll"

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

// OK sends a success response.
func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
	})
}

// Fail sends an error response.
func Fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, Response{
		Success: false,
		Error:   &ErrorInfo{Code: code, Message: message},
	})
}

type UserInput struct {
	Text string `json:"text"`
}

type submitOutput struct {
	ActualCardinality      int64 `json:"actualCardinality"`
	ApproximateCardinality int64 `json:"approximateCardinality"`
}

type queryOutput struct {
	Text             string `json:"text"`
	ApproximateCount int    `json:"approximateCount"`
}

func main() {
	router := gin.Default()
	hll := hll2.NewHLL()
	cmsSketch, err := cms.NewCms()
	if err != nil {
		fmt.Println("Failed to initialise CMS: " + err.Error())
		return
	}
	set := make(map[string]bool)
	router.POST("/submit", func(c *gin.Context) {
		var userInput UserInput

		if err := c.ShouldBindJSON(&userInput); err != nil {
			Fail(
				c,
				http.StatusBadRequest,
				"INVALID_REQUEST_BODY",
				"Request body must contain valid JSON",
			)
			return
		}

		fmt.Println(userInput.Text)
		_, err := hll.Add(userInput.Text)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		if err := cmsSketch.Add(userInput.Text); err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		card, err := hll.GetCardinality()
		if err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}
		set[userInput.Text] = true

		OK(c, submitOutput{
			ActualCardinality:      int64(len(set)),
			ApproximateCardinality: card,
		})
	})

	router.GET("/query", func(c *gin.Context) {
		text := c.Query("text")
		if text == "" {
			Fail(c, http.StatusBadRequest, "MISSING_PARAM", "query parameter 'text' is required")
			return
		}

		count, err := cmsSketch.Query(text)
		if err != nil {
			Fail(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", err.Error())
			return
		}

		OK(c, queryOutput{Text: text, ApproximateCount: count})
	})

	err = router.Run(":8080")
	if err != nil {
		fmt.Print("Server crashed" + err.Error())
		return
	}
}
