package tunnel

import (
	"strings"
	"testing"
)

func TestLineWriterSplitsAndRetainsPartial(t *testing.T) {
	t.Parallel()
	w := &lineWriter{}
	_, _ = w.Write([]byte("hello\nwor"))
	if string(w.rest) != "wor" {
		t.Fatalf("rest = %q, want %q", w.rest, "wor")
	}
	_, _ = w.Write([]byte("ld\n"))
	if len(w.rest) != 0 {
		t.Fatalf("rest = %q, want empty", w.rest)
	}
	_, _ = w.Write([]byte("a\nb\nc"))
	if string(w.rest) != "c" {
		t.Fatalf("rest = %q, want %q", w.rest, "c")
	}
}

func TestLineWriterManyFragments(t *testing.T) {
	t.Parallel()
	// Small writes without newlines must not corrupt the buffer; this is the
	// aliasing path that bit the previous implementation.
	w := &lineWriter{}
	for i := 0; i < 1000; i++ {
		_, _ = w.Write([]byte("x"))
	}
	if string(w.rest) != strings.Repeat("x", 1000) {
		t.Fatalf("rest length = %d, want 1000", len(w.rest))
	}
	_, _ = w.Write([]byte("\n"))
	if len(w.rest) != 0 {
		t.Fatalf("rest = %q, want empty", w.rest)
	}
}
