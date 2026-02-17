package model

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NormalizePriority
// ---------------------------------------------------------------------------

func TestNormalizePriority(t *testing.T) {
	tests := []struct {
		raw  int
		want Priority
	}{
		{0, PriorityNone},
		{1, PriorityHigh},
		{2, PriorityHigh},
		{3, PriorityHigh},
		{4, PriorityHigh},
		{5, PriorityMedium},
		{6, PriorityLow},
		{7, PriorityLow},
		{8, PriorityLow},
		{9, PriorityLow},
		{-1, PriorityNone},
		{10, PriorityNone},
		{100, PriorityNone},
	}
	for _, tt := range tests {
		if got := NormalizePriority(tt.raw); got != tt.want {
			t.Errorf("NormalizePriority(%d) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Priority.String
// ---------------------------------------------------------------------------

func TestPriority_String(t *testing.T) {
	tests := []struct {
		p    Priority
		want string
	}{
		{PriorityNone, "None"},
		{PriorityHigh, "High"},
		{PriorityMedium, "Medium"},
		{PriorityLow, "Low"},
		{Priority(42), "None"}, // unknown value
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("Priority(%d).String() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ContentHash
// ---------------------------------------------------------------------------

func TestContentHash_Deterministic(t *testing.T) {
	due := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	item := &Item{
		Title:       "Buy milk",
		Description: "Whole milk preferred",
		DueDate:     &due,
		Priority:    PriorityHigh,
		Completed:   false,
	}
	h1 := item.ContentHash()
	h2 := item.ContentHash()
	if h1 != h2 {
		t.Error("ContentHash not deterministic")
	}
}

func TestContentHash_DiffersOnTitleChange(t *testing.T) {
	item := &Item{Title: "Buy milk", Priority: PriorityNone}
	h1 := item.ContentHash()
	item.Title = "Buy bread"
	h2 := item.ContentHash()
	if h1 == h2 {
		t.Error("ContentHash should differ when title changes")
	}
}

func TestContentHash_DiffersOnPriorityChange(t *testing.T) {
	item := &Item{Title: "Task", Priority: PriorityHigh}
	h1 := item.ContentHash()
	item.Priority = PriorityLow
	h2 := item.ContentHash()
	if h1 == h2 {
		t.Error("ContentHash should differ when priority changes")
	}
}

func TestContentHash_DiffersOnCompletedChange(t *testing.T) {
	item := &Item{Title: "Task", Completed: false}
	h1 := item.ContentHash()
	item.Completed = true
	h2 := item.ContentHash()
	if h1 == h2 {
		t.Error("ContentHash should differ when completed changes")
	}
}

func TestContentHash_IgnoresModifiedAt(t *testing.T) {
	item := &Item{Title: "Task", ModifiedAt: time.Now()}
	h1 := item.ContentHash()
	item.ModifiedAt = item.ModifiedAt.Add(time.Hour)
	h2 := item.ContentHash()
	if h1 != h2 {
		t.Error("ContentHash should not change when only ModifiedAt changes")
	}
}

func TestContentHash_NilDueDate(t *testing.T) {
	item := &Item{Title: "No due", DueDate: nil}
	h := item.ContentHash()
	if h == "" {
		t.Error("ContentHash should be non-empty even with nil DueDate")
	}
}

// ---------------------------------------------------------------------------
// Priority prefix encoding / decoding
// ---------------------------------------------------------------------------

func TestEncodePriorityPrefix(t *testing.T) {
	tests := []struct {
		p    Priority
		desc string
		want string
	}{
		{PriorityHigh, "Buy milk", "[High] Buy milk"},
		{PriorityMedium, "Email boss", "[Medium] Email boss"},
		{PriorityLow, "Tidy desk", "[Low] Tidy desk"},
		{PriorityNone, "Whatever", "Whatever"},
		{PriorityHigh, "", "[High] "},
		{PriorityNone, "", ""},
	}
	for _, tt := range tests {
		if got := EncodePriorityPrefix(tt.p, tt.desc); got != tt.want {
			t.Errorf("EncodePriorityPrefix(%v, %q) = %q, want %q", tt.p, tt.desc, got, tt.want)
		}
	}
}

func TestDecodePriorityPrefix(t *testing.T) {
	tests := []struct {
		input    string
		wantP    Priority
		wantDesc string
	}{
		{"[High] Buy milk", PriorityHigh, "Buy milk"},
		{"[Medium] Email boss", PriorityMedium, "Email boss"},
		{"[Low] Tidy desk", PriorityLow, "Tidy desk"},
		{"No prefix here", PriorityNone, "No prefix here"},
		{"[High] ", PriorityHigh, ""},
		{"", PriorityNone, ""},
		// Partial prefix — should NOT match
		{"[High]No space", PriorityNone, "[High]No space"},
	}
	for _, tt := range tests {
		gotP, gotDesc := DecodePriorityPrefix(tt.input)
		if gotP != tt.wantP || gotDesc != tt.wantDesc {
			t.Errorf("DecodePriorityPrefix(%q) = (%v, %q), want (%v, %q)",
				tt.input, gotP, gotDesc, tt.wantP, tt.wantDesc)
		}
	}
}

func TestPriorityPrefixRoundTrip(t *testing.T) {
	for _, p := range []Priority{PriorityNone, PriorityHigh, PriorityMedium, PriorityLow} {
		desc := "some task description"
		encoded := EncodePriorityPrefix(p, desc)
		gotP, gotDesc := DecodePriorityPrefix(encoded)
		if gotP != p {
			t.Errorf("round-trip priority: got %v, want %v", gotP, p)
		}
		if gotDesc != desc {
			t.Errorf("round-trip description: got %q, want %q", gotDesc, desc)
		}
	}
}
