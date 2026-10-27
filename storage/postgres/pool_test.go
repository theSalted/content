package postgres

import "testing"

func TestWorkerConnectionBudget(t *testing.T) {
	for _, n := range []int32{0, 6} {
		if e := WithConnectionLimit(n)(&Store{}); e == nil {
			t.Fatal("invalid budget accepted")
		}
	}
	s := &Store{}
	if e := WithConnectionLimit(2)(s); e != nil || s.maxConnections != 2 {
		t.Fatal(e)
	}
}
