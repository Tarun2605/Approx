package misra_gries

type MGTopK struct {
	k       int
	counter map[string]int
}

func NewMGTopK() (*MGTopK, error) {
	return &MGTopK{
		k:       100,
		counter: make(map[string]int),
	}, nil
}

func (m *MGTopK) Add(input string) (bool, error) {

	// 1. Item already exists
	if m.counter[input] > 0 {
		m.counter[input]++
		return true, nil
	}

	// 2. There is an empty slot
	if len(m.counter) < m.k-1 {
		m.counter[input] = 1
		return true, nil
	}

	// 3. Table is full and input is new.
	for key := range m.counter {
		m.counter[key]--
	}

	// 4. Remove candidates whose counter reached zero.
	for key, val := range m.counter {
		if val == 0 {
			delete(m.counter, key)
		}
	}

	return true, nil
}

func (m *MGTopK) ListEntries() ([]string, error) {
	op := make([]string, 0)
	for key, _ := range m.counter {
		op = append(op, key)
	}
	return op, nil
}
