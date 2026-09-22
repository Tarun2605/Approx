package misra_gries

type MGTopK struct {
	k       int
	Counter map[string]int
}

func NewMGTopK() (*MGTopK, error) {
	return &MGTopK{
		k:       100,
		Counter: make(map[string]int),
	}, nil
}

func (m *MGTopK) Add(input string) (bool, error) {

	// 1. Item already exists
	if m.Counter[input] > 0 {
		m.Counter[input]++
		return true, nil
	}

	// 2. There is an empty slot
	if len(m.Counter) < m.k-1 {
		m.Counter[input] = 1
		return true, nil
	}

	// 3. Table is full and input is new.
	for key := range m.Counter {
		m.Counter[key]--
	}

	// 4. Remove candidates whose counter reached zero.
	for key, val := range m.Counter {
		if val == 0 {
			delete(m.Counter, key)
		}
	}

	return true, nil
}

func (m *MGTopK) ListEntries() ([]string, error) {
	op := make([]string, 0)
	for key, _ := range m.Counter {
		op = append(op, key)
	}
	return op, nil
}
