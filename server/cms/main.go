package cms

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"hash/fnv"
)

type CMS struct {
	w     int
	d     int
	seeds []uint64
	table [][]int
}

func NewCms() (*CMS, error) {
	op := &CMS{
		w: 16256,
		d: 30,
	}
	err := op.build()
	if err != nil {
		return nil, err
	}
	return op, nil
}

func hashString(s string) (uint64, error) {
	if len(s) == 0 {
		return 0, errors.New("empty string not allowed")
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(s))

	return h.Sum64(), nil
}

func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (cms *CMS) build() error {
	cms.seeds = make([]uint64, cms.d)
	cms.table = make([][]int, cms.d)

	// Generate one seed per row.
	for i := 0; i < cms.d; i++ {
		if err := binary.Read(
			rand.Reader,
			binary.LittleEndian,
			&cms.seeds[i],
		); err != nil {
			return err
		}
	}

	// Create d rows, each containing w counters.
	for i := 0; i < cms.d; i++ {
		cms.table[i] = make([]int, cms.w)
	}

	return nil
}

func (cms *CMS) Add(input string) error {
	hashValue, err := hashString(input)
	if err != nil {
		return err
	}

	for i := 0; i < cms.d; i++ {
		bucket := mix(hashValue+cms.seeds[i]) % uint64(cms.w)
		cms.table[i][bucket]++
	}

	return nil
}

func (cms *CMS) Query(input string) (int, error) {
	hashValue, err := hashString(input)
	if err != nil {
		return 0, err
	}

	ans := int(^uint(0) >> 1)

	for i := 0; i < cms.d; i++ {
		bucket := mix(hashValue+cms.seeds[i]) % uint64(cms.w)
		ans = min(ans, cms.table[i][bucket])
	}

	return ans, nil
}
