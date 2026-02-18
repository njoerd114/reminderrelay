// Package model defines shared types used across the sync engine and adapters.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Priority represents the priority level of a task.
// Values match Apple EventKit's canonical priority integers.
type Priority int

const (
	// PriorityNone indicates no priority is set.
	PriorityNone Priority = 0
	// PriorityHigh indicates high priority (EventKit 1–4).
	PriorityHigh Priority = 1
	// PriorityMedium indicates medium priority (EventKit 5).
	PriorityMedium Priority = 5
	// PriorityLow indicates low priority (EventKit 6–9).
	PriorityLow Priority = 9
)

// String returns the human-readable label for the priority.
func (p Priority) String() string {
	switch p {
	case PriorityHigh:
		return "High"
	case PriorityMedium:
		return "Medium"
	case PriorityLow:
		return "Low"
	default:
		return "None"
	}
}

// NormalizePriority maps any EventKit priority integer (0–9) to one of the
// four canonical levels. Values outside 0–9 are treated as None.
func NormalizePriority(raw int) Priority {
	switch {
	case raw >= 1 && raw <= 4:
		return PriorityHigh
	case raw == 5:
		return PriorityMedium
	case raw >= 6 && raw <= 9:
		return PriorityLow
	default:
		return PriorityNone
	}
}

// Item is the normalised representation of a task shared between the Reminders
// adapter, Home Assistant adapter, and sync engine.
type Item struct {
	// UID is the adapter-specific unique identifier (EventKit calendarItemIdentifier
	// or HA todo item UID).
	UID string

	// Title is the task's display title.
	Title string

	// Description is the task's body text (Reminders "notes" / HA "description").
	// For HA items the priority prefix has already been stripped; for Reminders
	// the raw notes are used as-is.
	Description string

	// DueDate is when the task is due. Nil means no due date.
	DueDate *time.Time

	// Priority is the normalised priority level.
	Priority Priority

	// Completed is true when the task has been marked as done.
	Completed bool

	// ModifiedAt is the last modification time reported by the source adapter.
	// Used for last-write-wins conflict resolution.
	ModifiedAt time.Time

	// ListName is the Apple Reminders list this item belongs to.
	// Used to look up the corresponding HA entity in the config mapping.
	ListName string
}

// ContentHash returns a deterministic SHA-256 hex digest of the fields that
// matter for change detection: title, description, due date, priority, and
// completed status. ModifiedAt is intentionally excluded — it changes on every
// save and is only used for conflict resolution, not change detection.
func (i *Item) ContentHash() string {
	h := sha256.New()
	h.Write([]byte(i.Title))
	h.Write([]byte("|"))
	h.Write([]byte(i.Description))
	h.Write([]byte("|"))
	if i.DueDate != nil {
		h.Write([]byte(i.DueDate.UTC().Format(time.RFC3339)))
	}
	h.Write([]byte("|"))
	_, _ = fmt.Fprintf(h, "%d", i.Priority)
	h.Write([]byte("|"))
	_, _ = fmt.Fprintf(h, "%t", i.Completed)
	return hex.EncodeToString(h.Sum(nil))
}

// --- Priority prefix encoding for Home Assistant descriptions ----------------

const (
	prefixHigh   = "[High] "
	prefixMedium = "[Medium] "
	prefixLow    = "[Low] "
)

// EncodePriorityPrefix prepends the priority tag to a description string for
// storage in Home Assistant (which has no native priority field).
func EncodePriorityPrefix(p Priority, description string) string {
	switch p {
	case PriorityHigh:
		return prefixHigh + description
	case PriorityMedium:
		return prefixMedium + description
	case PriorityLow:
		return prefixLow + description
	default:
		return description
	}
}

// DecodePriorityPrefix strips the priority tag from an HA description and
// returns the priority and the clean description text.
func DecodePriorityPrefix(description string) (Priority, string) {
	switch {
	case strings.HasPrefix(description, prefixHigh):
		return PriorityHigh, strings.TrimPrefix(description, prefixHigh)
	case strings.HasPrefix(description, prefixMedium):
		return PriorityMedium, strings.TrimPrefix(description, prefixMedium)
	case strings.HasPrefix(description, prefixLow):
		return PriorityLow, strings.TrimPrefix(description, prefixLow)
	default:
		return PriorityNone, description
	}
}
