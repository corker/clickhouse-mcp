package tools

import (
	"strings"
	"testing"
)

func TestResolveTableLimit(t *testing.T) {
	tests := []struct {
		name     string
		argLimit int
		folded   bool
		want     int
	}{
		{"zero lean -> default", 0, false, DefaultTableLimit},
		{"zero folded -> tighter default", 0, true, DefaultFoldedTableLimit},
		{"negative folded -> tighter default", -5, true, DefaultFoldedTableLimit},
		{"negative lean -> default", -1, false, DefaultTableLimit},
		{"explicit overrides lean", 7, false, 7},
		{"explicit overrides folded tier", 7, true, 7},
	}
	for _, tt := range tests {
		if got := resolveTableLimit(tt.argLimit, tt.folded); got != tt.want {
			t.Errorf("%s: resolveTableLimit(%d,%v) = %d, want %d", tt.name, tt.argLimit, tt.folded, got, tt.want)
		}
	}
}

func TestResolveLimit(t *testing.T) {
	if got := resolveLimit(0, 200); got != 200 {
		t.Errorf("zero -> default: got %d", got)
	}
	if got := resolveLimit(-3, 200); got != 200 {
		t.Errorf("negative -> default: got %d", got)
	}
	if got := resolveLimit(5, 200); got != 5 {
		t.Errorf("explicit overrides: got %d", got)
	}
}

func TestTruncate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6}

	t.Run("under limit: not truncated, count set, no note", func(t *testing.T) {
		kept, tr := truncate(items[:3], 5, "tables")
		if tr.Truncated || len(kept) != 3 || tr.Count != 3 || tr.Limit != 5 || tr.Note != "" {
			t.Fatalf("got %+v (kept=%d)", tr, len(kept))
		}
	})

	t.Run("exactly at limit: not truncated (the off-by-one boundary)", func(t *testing.T) {
		kept, tr := truncate(items[:5], 5, "tables")
		if tr.Truncated || len(kept) != 5 || tr.Count != 5 || tr.Note != "" {
			t.Fatalf("5 items at limit 5 must not truncate: %+v (kept=%d)", tr, len(kept))
		}
	})

	t.Run("limit+1: truncated, sentinel dropped, note has count+noun", func(t *testing.T) {
		kept, tr := truncate(items, 5, "tables") // 6 items, limit 5
		if !tr.Truncated || len(kept) != 5 || tr.Count != 5 {
			t.Fatalf("6 items at limit 5 must truncate to 5: %+v (kept=%d)", tr, len(kept))
		}
		if !strings.Contains(tr.Note, "5") || !strings.Contains(tr.Note, "tables") {
			t.Errorf("note should mention the count and noun, got %q", tr.Note)
		}
	})

	t.Run("empty slice: not truncated, count 0", func(t *testing.T) {
		kept, tr := truncate([]int{}, 5, "tables")
		if tr.Truncated || len(kept) != 0 || tr.Count != 0 || tr.Note != "" {
			t.Fatalf("empty input must not truncate: %+v (kept=%d)", tr, len(kept))
		}
	})

	// A non-positive limit is treated as no-cap, not "slice to 0 / panic".
	t.Run("limit=0: no cap, not truncated", func(t *testing.T) {
		kept, tr := truncate(items, 0, "tables")
		if tr.Truncated || len(kept) != len(items) || tr.Note != "" {
			t.Fatalf("limit 0 must return everything uncapped: %+v (kept=%d)", tr, len(kept))
		}
	})

	t.Run("negative limit: no cap, no panic", func(t *testing.T) {
		kept, tr := truncate(items, -1, "tables")
		if tr.Truncated || len(kept) != len(items) {
			t.Fatalf("negative limit must return everything, not panic: %+v (kept=%d)", tr, len(kept))
		}
	})
}
