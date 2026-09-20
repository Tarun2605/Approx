//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

const (
	url       = "http://localhost:8080/submit"
	total     = 100000
	logEvery  = 1000
	stringLen = 20
)

type HLLTestResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Actual      int64 `json:"actualCardinality"`
		Approximate int64 `json:"approximateCardinality"`
	} `json:"data"`
}

func randomString(r *rand.Rand, n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+[]{};:',.<>/?"

	b := make([]byte, n)

	for i := range b {
		b[i] = chars[r.Intn(len(chars))]
	}

	return string(b)
}

func main() {
	r := rand.New(rand.NewSource(42))

	// Ground truth.
	seen := make(map[string]bool)

	client := &http.Client{}

	var totalTime time.Duration

	for i := 1; i <= total; i++ {
		value := randomString(r, stringLen)

		seen[value] = true

		body, _ := json.Marshal(map[string]string{
			"text": value,
		})

		start := time.Now()

		resp, err := client.Post(
			url,
			"application/json",
			bytes.NewBuffer(body),
		)

		elapsed := time.Since(start)
		totalTime += elapsed

		if err != nil {
			panic(err)
		}

		var result HLLTestResponse
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if err != nil {
			panic(err)
		}

		actual := int64(len(seen))
		approx := result.Data.Approximate

		errorPercent := float64(approx-actual) / float64(actual) * 100

		if i%logEvery == 0 {
			fmt.Printf(
				"n=%-7d actual=%-7d approx=%-7d error=%+.3f%% avg_latency=%v\n",
				i,
				actual,
				approx,
				errorPercent,
				totalTime/time.Duration(i),
			)
		}
	}

	fmt.Println()
	fmt.Println("========== FINAL ==========")

	actual := int64(len(seen))

	// Get final result with one extra request.
	value := randomString(r, stringLen)

	body, _ := json.Marshal(map[string]string{
		"text": value,
	})

	resp, err := client.Post(
		url,
		"application/json",
		bytes.NewBuffer(body),
	)

	if err != nil {
		panic(err)
	}

	var result HLLTestResponse
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()

	approx := result.Data.Approximate
	errorPercent := float64(approx-actual) / float64(actual) * 100

	fmt.Printf("Actual:      %d\n", actual)
	fmt.Printf("Approximate: %d\n", approx)
	fmt.Printf("Error:       %+.3f%%\n", errorPercent)
	fmt.Printf("Avg latency: %v\n", totalTime/time.Duration(total))
}
