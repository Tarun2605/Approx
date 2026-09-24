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
	stringLen = 8 // server rejects strings >= 10 chars
)

type HLLTestResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Approximate int64 `json:"approximateCardinality"`
	} `json:"data"`
}

type QueryTestResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Text             string `json:"text"`
		ApproximateCount int    `json:"approximateCount"`
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

		// Skip rate-limited responses — don't count this iteration.
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			i--
			time.Sleep(10 * time.Millisecond)
			continue
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
				"n=%-7d seen=%-7d approx=%-7d error=%+.3f%% avg_latency=%v\n",
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

	fmt.Printf("Seen:        %d\n", actual)
	fmt.Printf("Approximate: %d\n", approx)
	fmt.Printf("Error:       %+.3f%%\n", errorPercent)
	fmt.Printf("Avg latency: %v\n", totalTime/time.Duration(total))

	fmt.Println()
	testMGTopK(client)
}

// testMGTopK submits a skewed stream to the server and validates that
// heavy hitters appear in the /stats top-K list and have high CMS counts.
func testMGTopK(client *http.Client) {
	fmt.Println("========== MG TOP-K TEST ==========")

	r := rand.New(rand.NewSource(99))

	type item struct {
		label string
		count int
	}

	// Heavy hitters use <= 9 chars to pass the server's length check.
	heavyHitters := []item{
		{"hot_a", 800},
		{"hot_b", 600},
		{"hot_c", 400},
		{"hot_d", 200},
		{"hot_e", 100},
	}

	type submission struct{ text string }
	var stream []submission

	for _, h := range heavyHitters {
		for i := 0; i < h.count; i++ {
			stream = append(stream, submission{h.label})
		}
	}
	// noise strings — 8 chars, passes length check
	for i := 0; i < 500; i++ {
		stream = append(stream, submission{randomString(r, 8)})
	}

	r.Shuffle(len(stream), func(i, j int) { stream[i], stream[j] = stream[j], stream[i] })

	fmt.Printf("Submitting %d items (%d heavy hitters + 500 noise)...\n",
		len(stream), len(heavyHitters))

	for _, s := range stream {
		body, _ := json.Marshal(map[string]string{"text": s.text})
		for {
			resp, err := client.Post(submitURL, "application/json", bytes.NewBuffer(body))
			if err != nil {
				panic(fmt.Sprintf("submit failed: %v", err))
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusTooManyRequests {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Fetch top-K from /stats
	type StatsResponse struct {
		Success bool `json:"success"`
		Data    struct {
			TopK []string `json:"topK"`
		} `json:"data"`
	}

	statsResp, err := client.Get("http://localhost:8080/stats")
	if err != nil {
		panic(fmt.Sprintf("stats failed: %v", err))
	}
	var sr StatsResponse
	json.NewDecoder(statsResp.Body).Decode(&sr)
	statsResp.Body.Close()

	topKSet := make(map[string]bool, len(sr.Data.TopK))
	for _, e := range sr.Data.TopK {
		topKSet[e] = true
	}

	sorted := append([]string(nil), sr.Data.TopK...)
	sort.Strings(sorted)
	fmt.Printf("Top-K candidates (%d entries):\n", len(sorted))
	for _, e := range sorted {
		fmt.Printf("  • %s\n", e)
	}

	// ── Heavy-hitter membership + CMS count check ────────────────────────
	fmt.Println()
	fmt.Println("--- Heavy-hitter check ---")

	allFound := true
	for _, h := range heavyHitters {
		resp, err := client.Get(queryURL + "?text=" + h.label)
		if err != nil {
			panic(fmt.Sprintf("query failed: %v", err))
		}
		var qr QueryTestResponse
		json.NewDecoder(resp.Body).Decode(&qr)
		resp.Body.Close()

		inTopK := topKSet[h.label]
		status := "PASS ✓"
		if !inTopK {
			status = "FAIL ✗"
			allFound = false
		}
		fmt.Printf("  %-8s submitted=%-4d  cms_count=%-4d  inTopK=%-5v  [%s]\n",
			h.label, h.count, qr.Data.ApproximateCount, inTopK, status)
	}

	// ── Cold item should not be in top-K ────────────────────────────────
	fmt.Println()
	fmt.Println("--- Cold item check ---")
	coldItem := "zzzcold"
	resp, err := client.Get(queryURL + "?text=" + coldItem)
	if err != nil {
		panic(fmt.Sprintf("query failed: %v", err))
	}
	var qr QueryTestResponse
	json.NewDecoder(resp.Body).Decode(&qr)
	resp.Body.Close()

	coldInTopK := topKSet[coldItem]
	coldStatus := "PASS ✓"
	if coldInTopK {
		coldStatus = "FAIL ✗ (false positive)"
	}
	fmt.Printf("  %-8s cms_count=%d  inTopK=%v  [%s]\n",
		coldItem, qr.Data.ApproximateCount, coldInTopK, coldStatus)

	fmt.Println()
	if allFound && !coldInTopK {
		fmt.Println("MG TOP-K TEST PASSED")
	} else {
		fmt.Println("MG TOP-K TEST FAILED — check output above")
	}
}
