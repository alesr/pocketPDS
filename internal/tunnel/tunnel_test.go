package tunnel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLineWriterSplitsAndRetainsPartial(t *testing.T) {
	t.Parallel()
	w := &lineWriter{}
	_, _ = w.Write([]byte("hello\nwor"))
	require.Equal(t, "wor", string(w.rest))
	_, _ = w.Write([]byte("ld\n"))
	require.Empty(t, w.rest)
	_, _ = w.Write([]byte("a\nb\nc"))
	require.Equal(t, "c", string(w.rest))
}

func TestLineWriterManyFragments(t *testing.T) {
	t.Parallel()
	// Small writes without newlines must not corrupt the buffer; this is the
	// aliasing path that bit the previous implementation.
	w := &lineWriter{}
	for range 1000 {
		_, _ = w.Write([]byte("x"))
	}
	require.Len(t, w.rest, 1000)
	require.Equal(t, strings.Repeat("x", 1000), string(w.rest))
	_, _ = w.Write([]byte("\n"))
	require.Empty(t, w.rest)
}
