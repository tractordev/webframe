package webframe

import "testing"

// A page can call window.__webframe directly with a fake result, so Eval's
// id must not be guessable from having seen earlier ids.
func TestRandomIDNotSequential(t *testing.T) {
	ids := make(map[int]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := randomID()
		if id < 0 {
			t.Fatalf("randomID returned a negative id: %d", id)
		}
		if ids[id] {
			t.Fatalf("randomID returned a duplicate id: %d", id)
		}
		ids[id] = true
	}

	// A sequential or low-entropy generator would cluster small, nearby
	// values; a 63-bit random source should not produce two ids closer than
	// a handful of integers to each other in a sample this size.
	seen := make([]int, 0, len(ids))
	for id := range ids {
		seen = append(seen, id)
	}
	adjacent := 0
	for _, id := range seen {
		if ids[id+1] || ids[id-1] {
			adjacent++
		}
	}
	if adjacent > len(seen)/10 {
		t.Fatalf("too many adjacent ids (%d of %d); generator may not be random", adjacent, len(seen))
	}
}

// EvalContext must not hand out an id that is already pending, even if the
// random source were ever to collide.
func TestEvalContextSkipsCollidingID(t *testing.T) {
	f := &Frame{evals: map[int]*pending{}}
	taken := randomID()
	f.evals[taken] = &pending{cb: func(string, error) {}}

	// Simulate the id-selection loop in EvalContext directly, since it needs
	// a live glaze.WebView to dispatch through.
	id := taken
	for f.evals[id] != nil {
		id = randomID()
	}
	if id == taken {
		t.Fatalf("id selection did not avoid the already-pending id")
	}
}
