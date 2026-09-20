//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"time"
)

const (
	submitURL = "http://localhost:8080/submit"
	queryURL  = "http://localhost:8080/query"
	total     = 100000
	logEvery  = 1000
	stringLen = 20
)

type HLLTestResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Actual      int64    `json:"actualCardinality"`
		Approximate int64    `json:"approximateCardinality"`
		TopK        []string `json:"approximateTopK"`
	} `json:"data"`
}

type QueryTestResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Text             string   `json:"text"`
		ApproximateCount int      `json:"approximateCount"`
		IsTopKCandidate  bool     `json:"isTopKCandidate"`
		ApproximateTopK  []string `json:"approximateTopK"`
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
			submitURL,
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
		submitURL,
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

	fmt.Println()
	testMGTopK(client)
}

// testMGTopK submits a skewed stream to the server and validates that:
//   - Heavy hitters appear as top-K candidates via /submit
//   - /query correctly reports frequency and top-K membership for those items
//   - Rare items are not falsely promoted as top-K candidates
func testMGTopK(client *http.Client) {
	fmt.Println("========== MG TOP-K TEST ==========")

	r := rand.New(rand.NewSource(99))

	// Build a skewed workload: 5 heavy hitters submitted many times,
	// plus a long tail of unique strings submitted only once each.
	type item struct {
		label string
		count int
	}

	heavyHitters := []item{
		{"__hot_alpha__", 800},
		{"__hot_beta__", 600},
		{"__hot_gamma__", 400},
		{"__hot_delta__", 200},
		{"__hot_epsilon__", 100},
	}

	// Build the full submission list: heavy hitters interleaved with noise.
	type submission struct{ text string }
	var stream []submission

	for _, h := range heavyHitters {
		for i := 0; i < h.count; i++ {
			stream = append(stream, submission{h.label})
		}
	}
	// 500 unique noise strings (submitted once each).
	for i := 0; i < 500; i++ {
		stream = append(stream, submission{randomString(r, stringLen)})
	}

	// Shuffle so heavy hitters are not all at the front.
	r.Shuffle(len(stream), func(i, j int) { stream[i], stream[j] = stream[j], stream[i] })

	fmt.Printf("Submitting %d items (%d distinct heavy hitters + 500 noise)...\n",
		len(stream), len(heavyHitters))

	var lastTopK []string
	for _, s := range stream {
		body, _ := json.Marshal(map[string]string{"text": s.text})
		resp, err := client.Post(submitURL, "application/json", bytes.NewBuffer(body))
		if err != nil {
			panic(fmt.Sprintf("submit failed: %v", err))
		}
		var result HLLTestResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			panic(fmt.Sprintf("decode failed: %v", err))
		}
		resp.Body.Close()
		lastTopK = result.Data.TopK
	}

	fmt.Printf("Final top-K candidate list (%d entries):\n", len(lastTopK))
	sorted := append([]string(nil), lastTopK...)
	sort.Strings(sorted)
	for _, e := range sorted {
		fmt.Printf("  • %s\n", e)
	}

	// ── Validate heavy hitters appear in top-K ──────────────────────────────
	fmt.Println()
	fmt.Println("--- Heavy-hitter membership check (via /query) ---")

	topKSet := make(map[string]bool, len(lastTopK))
	for _, e := range lastTopK {
		topKSet[e] = true
	}

	allFound := true
	for _, h := range heavyHitters {
		body, _ := json.Marshal(map[string]string{"text": h.label})
		req, _ := http.NewRequest(http.MethodGet,
			queryURL+"?text="+h.label, bytes.NewBuffer(body))
		resp, err := client.Do(req)
		if err != nil {
			panic(fmt.Sprintf("query failed: %v", err))
		}
		var qr QueryTestResponse
		if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
			panic(fmt.Sprintf("decode failed: %v", err))
		}
		resp.Body.Close()

		status := "PASS ✓"
		if !qr.Data.IsTopKCandidate {
			status = "FAIL ✗"
			allFound = false
		}
		fmt.Printf("  %-22s submitted=%-4d  cms_count=%-4d  isTopK=%v  [%s]\n",
			h.label, h.count, qr.Data.ApproximateCount, qr.Data.IsTopKCandidate, status)
	}

	// ── Validate a cold item is unlikely to be in top-K ────────────────────
	fmt.Println()
	fmt.Println("--- Cold item check (via /query) ---")
	coldItem := "__never_submitted_xyz__"
	resp, err := client.Get(queryURL + "?text=" + coldItem)
	if err != nil {
		panic(fmt.Sprintf("query failed: %v", err))
	}
	var qr QueryTestResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		panic(fmt.Sprintf("decode failed: %v", err))
	}
	resp.Body.Close()

	coldStatus := "PASS ✓"
	if qr.Data.IsTopKCandidate {
		coldStatus = "FAIL ✗ (false positive)"
	}
	fmt.Printf("  %-28s cms_count=%d  isTopK=%v  [%s]\n",
		coldItem, qr.Data.ApproximateCount, qr.Data.IsTopKCandidate, coldStatus)

	// ── Summary ─────────────────────────────────────────────────────────────
	fmt.Println()
	if allFound && !qr.Data.IsTopKCandidate {
		fmt.Println("MG TOP-K TEST PASSED")
	} else {
		fmt.Println("MG TOP-K TEST FAILED — check output above")
	}
}
