package main

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func main() {
	svc, err := NewSketchService()
	if err != nil {
		fmt.Println("Failed to initialise service: " + err.Error())
		return
	}

	router := gin.Default()

	// POST /submit  {"text": "some string"}
	router.POST("/submit", handleSubmit(svc))

	// GET /query?text=<value>
	router.GET("/query", handleQuery(svc))

	if err := router.Run(":8080"); err != nil {
		fmt.Println("Server crashed: " + err.Error())
	}
}
