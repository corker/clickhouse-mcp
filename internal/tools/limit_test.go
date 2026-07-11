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

	t.Run("under limit: not truncated, no note", func(t *testing.T) {
		kept, tr, note := truncate(items[:3], 5, "tables")
		if tr || len(kept) != 3 || note != "" {
			t.Fatalf("got kept=%d truncated=%v note=%q", len(kept), tr, note)
		}
	})

	t.Run("exactly at limit: not truncated (the off-by-one boundary)", func(t *testing.T) {
		kept, tr, note := truncate(items[:5], 5, "tables")
		if tr || len(kept) != 5 || note != "" {
			t.Fatalf("5 items at limit 5 must not truncate: kept=%d truncated=%v note=%q", len(kept), tr, note)
		}
	})

	t.Run("limit+1: truncated, sentinel dropped, note has count+noun", func(t *testing.T) {
		kept, tr, note := truncate(items, 5, "tables") // 6 items, limit 5
		if !tr || len(kept) != 5 {
			t.Fatalf("6 items at limit 5 must truncate to 5: kept=%d truncated=%v", len(kept), tr)
		}
		if !strings.Contains(note, "5") || !strings.Contains(note, "tables") {
			t.Errorf("note should mention the count and noun, got %q", note)
		}
	})
}
