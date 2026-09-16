package hll

import (
	"errors"
	"hash/fnv"
	"math"
	"math/bits"
)

type HLL struct {
	p      int //bits of the hash used to choose the bucket
	n      int //number of registers/buckets in the HLL
	m      int //number of hash bits we're working with
	bucket []int
	alphaM float64
}

func NewHLL() *HLL {
	p := 14
	m := 1 << p

	return &HLL{
		p:      p,
		n:      64,
		m:      m,
		bucket: make([]int, m),
		alphaM: 0.7213 / (1 + (1.079 / float64(m))),
	}
}

func hashString(s string) (uint64, error) {
	if len(s) == 0 {
		return 0, errors.New("empty string not allowed")
	}

	h := fnv.New64a()

	_, err := h.Write([]byte(s))
	if err != nil {
		return 0, err
	}

	return h.Sum64(), nil
}

func (hll *HLL) Add(input string) (bool, error) {
	hashValue, err := hashString(input)
	if err != nil {
		return false, err
	}

	// First p bits decide the bucket.
	bucketIndex := int(hashValue >> uint(hll.n-hll.p))

	// Remaining bits determine the rank.
	remainingValue := hashValue << uint(hll.p)
	rank := bits.LeadingZeros64(remainingValue) + 1

	if rank > hll.bucket[bucketIndex] {
		hll.bucket[bucketIndex] = rank
	}

	return true, nil
}

func (hll *HLL) GetCardinality() (int64, error) {
	sum := 0.0
	zeroBuckets := 0

	for _, rank := range hll.bucket {
		if rank == 0 {
			zeroBuckets++
		}

		sum += math.Pow(2, -float64(rank))
	}

	estimate := hll.alphaM * float64(hll.m*hll.m) / sum

	// Small-range correction.
	if estimate <= 2.5*float64(hll.m) && zeroBuckets > 0 {
		estimate = float64(hll.m) *
			math.Log(float64(hll.m)/float64(zeroBuckets))
	}

	return int64(math.Round(estimate)), nil
}
