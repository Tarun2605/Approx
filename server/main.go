package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	ulimiter "github.com/ulule/limiter"
	ginlimiter "github.com/ulule/limiter/drivers/middleware/gin"
	"github.com/ulule/limiter/drivers/store/memory"
)

func ginSlogLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency", time.Since(start),
			"ip", c.ClientIP(),
		)
	}
}

func main() {
	svc, err := NewSketchService()
	if err != nil {
		fmt.Println("Failed to initialise service: " + err.Error())
		return
	}
	mode := os.Getenv("GIN_MODE")
	if mode == "" {
		mode = gin.DebugMode
	}
	gin.SetMode(mode)

	rate, _ := ulimiter.NewRateFromFormatted("1000-S") // 1000 req/sec
	store := memory.NewStore()
	lim := ulimiter.New(store, rate)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(ginSlogLogger())
	router.Use(ginlimiter.NewMiddleware(lim))

	router.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins(),
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	router.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20) // 1 MB cap
		c.Next()
	})
	// POST /submit  {"text": "some string"}
	router.POST("/submit", handleSubmit(svc))

	// GET /query?text=<value>
	router.GET("/query", handleQuery(svc))

	// GET /stats — distinct count + top-K candidates
	router.GET("/stats", handleStats(svc))

	// GET /memory — probabilistic vs naive memory comparison
	router.GET("/memory", handleMemoryStats(svc))

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Serve the frontend from ./static/index.html
	router.StaticFile("/", "./static/index.html")

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	// Start server non-blocking
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("shutting down...")

	// Give in-flight requests up to 5 seconds to finish
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("forced shutdown:", err)
	}

	if err := svc.Close(); err != nil {
		log.Println("close service:", err)
	}

	fmt.Println("server exited")
}

// allowedOrigins reads ALLOWED_ORIGINS env var (comma-separated).
// Falls back to localhost defaults when not set (dev mode).
func allowedOrigins() []string {
	raw := os.Getenv("ALLOWED_ORIGINS")
	if raw == "" {
		return []string{"http://localhost", "http://localhost:8080"}
	}
	var origins []string
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}
