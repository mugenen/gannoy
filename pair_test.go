package gannoy

import (
	"strings"
	"testing"
)

func TestNewPair(t *testing.T) {
	pairs := ""
	_, err := newPairFromReader(strings.NewReader(pairs))
	if err != nil {
		t.Errorf("newPair should not return error, but return %v", err)
	}
}

func TestPairKeyFromId(t *testing.T) {
	pairs := "100,1\n200,2\n"
	pair, err := newPairFromReader(strings.NewReader(pairs))
	if err != nil {
		t.Errorf("newPair should not return error, but return %v", err)
	}
	tests := map[uint]uint{
		uint(100): uint(1),
		uint(200): uint(2),
	}
	for key, id := range tests {
		actual, ok := pair.keyFromId(id)
		if !ok {
			t.Errorf("pair.keyFromId should return key, but dose not return.")
		}
		if key != actual.(uint) {
			t.Errorf("pair.keyFromId should return %d, but return %d.", key, actual.(uint))
		}
	}
}

func TestPairIdFromKey(t *testing.T) {
	pairs := "100,1\n200,2\n"
	pair, err := newPairFromReader(strings.NewReader(pairs))
	if err != nil {
		t.Errorf("newPair should not return error, but return %v", err)
	}
	tests := map[uint]uint{
		uint(100): uint(1),
		uint(200): uint(2),
	}
	for key, id := range tests {
		actual, ok := pair.idFromKey(key)
		if !ok {
			t.Errorf("pair.idFromKey should return key, but dose not return.")
		}
		if id != actual.(uint) {
			t.Errorf("pair.idFromKey should return %d, but return %d.", id, actual.(uint))
		}
	}
}

func TestPairAddPairAndRemoveByKey(t *testing.T) {
	pairs := ""
	pair, err := newPairFromReader(strings.NewReader(pairs))
	if err != nil {
		t.Errorf("newPair should not return error, but return %v", err)
	}

	key := uint(100)
	id := uint(1)

	_, ok := pair.idFromKey(key)
	if ok {
		t.Errorf("emptyPair.idFromKey should not return key, but return.")
	}

	pair.addPair(key, id)

	actual, ok := pair.idFromKey(key)
	if !ok {
		t.Errorf("pair.idFromKey should return key, but dose not return.")
	}
	if id != actual.(uint) {
		t.Errorf("pair.idFromKey should return %d, but return %d.", id, actual.(uint))
	}

	pair.removeByKey(key)

	_, ok = pair.idFromKey(key)
	if ok {
		t.Errorf("emptyPair.idFromKey should not return key, but return.")
	}
}

func TestPairIsLast(t *testing.T) {
	pairs := ""
	pair, err := newPairFromReader(strings.NewReader(pairs))
	if err != nil {
		t.Errorf("newPair should not return error, but return %v", err)
	}
	if isLast := pair.isLast(); isLast {
		t.Errorf("isLast should return false when Pair is empty, but return %t", isLast)
	}

	key := uint(100)
	id := uint(1)

	pair.addPair(key, id)
	if isLast := pair.isLast(); !isLast {
		t.Errorf("isLast should return true when Pair has one entry, but return %t", isLast)
	}

	key = uint(200)
	id = uint(2)

	pair.addPair(key, id)
	if isLast := pair.isLast(); isLast {
		t.Errorf("isLast should return false when Pair has entries, but return %t", isLast)
	}

	key = uint(300)
	id = uint(3)

	pair.addPair(key, id)
	pair.addPair(key, id)
	if isLast := pair.isLast(); isLast {
		t.Errorf("isLast should return false when Pair has entries, but return %t", isLast)
	}
}

func TestPairIsEmpty(t *testing.T) {
	pairs := ""
	pair, err := newPairFromReader(strings.NewReader(pairs))
	if err != nil {
		t.Errorf("newPair should not return error, but return %v", err)
	}

	if isEmpty := pair.isEmpty(); !isEmpty {
		t.Errorf("isEmpty should return true when Pair is empty, but return %t", isEmpty)
	}

	key := uint(100)
	id := uint(1)

	pair.addPair(key, id)
	if isEmpty := pair.isEmpty(); isEmpty {
		t.Errorf("isEmpty should return false when Pair isn't empty, but return %t", isEmpty)
	}

	pair.removeByKey(key)
	if isEmpty := pair.isEmpty(); !isEmpty {
		t.Errorf("isEmpty should return true when Pair is empty, but return %t", isEmpty)
	}
}
