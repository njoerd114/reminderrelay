package reminders

import (
	"testing"
	"time"

	ekreminders "github.com/BRO3886/go-eventkit/reminders"

	"github.com/njoerd114/reminderrelay/internal/model"
)

// ---------------------------------------------------------------------------
// reminderToItem
// ---------------------------------------------------------------------------

func TestReminderToItem_FullFields(t *testing.T) {
	due := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	mod := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)

	r := &ekreminders.Reminder{
		ID:         "EK-UID-123",
		Title:      "Buy milk",
		Notes:      "Whole milk preferred",
		List:       "Shopping",
		DueDate:    &due,
		ModifiedAt: &mod,
		Priority:   ekreminders.PriorityHigh,
		Completed:  false,
	}

	got := reminderToItem(r, "Shopping")

	if got.UID != "EK-UID-123" {
		t.Errorf("UID = %q, want %q", got.UID, "EK-UID-123")
	}
	if got.Title != "Buy milk" {
		t.Errorf("Title = %q, want %q", got.Title, "Buy milk")
	}
	if got.Description != "Whole milk preferred" {
		t.Errorf("Description = %q, want %q", got.Description, "Whole milk preferred")
	}
	if got.DueDate == nil || !got.DueDate.Equal(due) {
		t.Errorf("DueDate = %v, want %v", got.DueDate, due)
	}
	if got.Priority != model.PriorityHigh {
		t.Errorf("Priority = %v, want %v", got.Priority, model.PriorityHigh)
	}
	if got.Completed {
		t.Error("Completed = true, want false")
	}
	if !got.ModifiedAt.Equal(mod) {
		t.Errorf("ModifiedAt = %v, want %v", got.ModifiedAt, mod)
	}
	if got.ListName != "Shopping" {
		t.Errorf("ListName = %q, want %q", got.ListName, "Shopping")
	}
}

func TestReminderToItem_NilOptionals(t *testing.T) {
	r := &ekreminders.Reminder{
		ID:       "EK-UID-456",
		Title:    "No due date",
		Priority: ekreminders.PriorityNone,
	}

	got := reminderToItem(r, "Default")

	if got.DueDate != nil {
		t.Errorf("DueDate = %v, want nil", got.DueDate)
	}
	if got.ModifiedAt.IsZero() == false {
		// ModifiedAt is nil in source → zero value
		// Actually when ModifiedAt is nil, we get time.Time zero value
	}
	if got.Priority != model.PriorityNone {
		t.Errorf("Priority = %v, want %v", got.Priority, model.PriorityNone)
	}
}

func TestReminderToItem_PriorityNormalization(t *testing.T) {
	// EventKit can return priority values like 3 (high range) or 7 (low range).
	tests := []struct {
		ekPriority ekreminders.Priority
		want       model.Priority
	}{
		{0, model.PriorityNone},
		{1, model.PriorityHigh},
		{ekreminders.Priority(3), model.PriorityHigh}, // non-canonical high
		{5, model.PriorityMedium},
		{ekreminders.Priority(7), model.PriorityLow}, // non-canonical low
		{9, model.PriorityLow},
	}

	for _, tt := range tests {
		r := &ekreminders.Reminder{
			ID:       "test",
			Priority: tt.ekPriority,
		}
		got := reminderToItem(r, "Test")
		if got.Priority != tt.want {
			t.Errorf("priority(%d) → %v, want %v", tt.ekPriority, got.Priority, tt.want)
		}
	}
}

func TestReminderToItem_CompletedState(t *testing.T) {
	r := &ekreminders.Reminder{
		ID:        "done-task",
		Title:     "Already done",
		Completed: true,
	}
	got := reminderToItem(r, "Work")
	if !got.Completed {
		t.Error("Completed = false, want true")
	}
}

// ---------------------------------------------------------------------------
// itemToCreateInput
// ---------------------------------------------------------------------------

func TestItemToCreateInput_FullFields(t *testing.T) {
	due := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	item := &model.Item{
		Title:       "Write tests",
		Description: "All edge cases",
		ListName:    "Work",
		DueDate:     &due,
		Priority:    model.PriorityMedium,
	}

	got := itemToCreateInput(item)

	if got.Title != "Write tests" {
		t.Errorf("Title = %q, want %q", got.Title, "Write tests")
	}
	if got.Notes != "All edge cases" {
		t.Errorf("Notes = %q, want %q", got.Notes, "All edge cases")
	}
	if got.ListName != "Work" {
		t.Errorf("ListName = %q, want %q", got.ListName, "Work")
	}
	if got.DueDate == nil || !got.DueDate.Equal(due) {
		t.Errorf("DueDate = %v, want %v", got.DueDate, due)
	}
	if got.Priority != ekreminders.PriorityMedium {
		t.Errorf("Priority = %v, want %v", got.Priority, ekreminders.PriorityMedium)
	}
}

func TestItemToCreateInput_NoDueDate(t *testing.T) {
	item := &model.Item{
		Title:    "No deadline",
		ListName: "Personal",
		Priority: model.PriorityNone,
	}
	got := itemToCreateInput(item)
	if got.DueDate != nil {
		t.Errorf("DueDate = %v, want nil", got.DueDate)
	}
}

// ---------------------------------------------------------------------------
// itemToUpdateInput
// ---------------------------------------------------------------------------

func TestItemToUpdateInput_FullFields(t *testing.T) {
	due := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	item := &model.Item{
		Title:       "Updated title",
		Description: "Updated notes",
		DueDate:     &due,
		Priority:    model.PriorityLow,
	}

	got := itemToUpdateInput(item)

	if got.Title == nil || *got.Title != "Updated title" {
		t.Errorf("Title = %v, want %q", got.Title, "Updated title")
	}
	if got.Notes == nil || *got.Notes != "Updated notes" {
		t.Errorf("Notes = %v, want %q", got.Notes, "Updated notes")
	}
	if got.DueDate == nil || !got.DueDate.Equal(due) {
		t.Errorf("DueDate = %v, want %v", got.DueDate, due)
	}
	if got.Priority == nil || *got.Priority != ekreminders.PriorityLow {
		t.Errorf("Priority = %v, want %v", got.Priority, ekreminders.PriorityLow)
	}
	if got.ClearDueDate {
		t.Error("ClearDueDate = true, want false when DueDate is set")
	}
	if got.Completed != nil {
		t.Error("Completed should be nil (handled by CompleteReminder/UncompleteReminder)")
	}
}

func TestItemToUpdateInput_ClearDueDate(t *testing.T) {
	item := &model.Item{
		Title:   "Remove deadline",
		DueDate: nil,
	}
	got := itemToUpdateInput(item)
	if !got.ClearDueDate {
		t.Error("ClearDueDate = false, want true when DueDate is nil")
	}
	if got.DueDate != nil {
		t.Errorf("DueDate = %v, want nil when ClearDueDate is true", got.DueDate)
	}
}

// ---------------------------------------------------------------------------
// priorityToEventKit
// ---------------------------------------------------------------------------

func TestPriorityToEventKit(t *testing.T) {
	tests := []struct {
		p    model.Priority
		want ekreminders.Priority
	}{
		{model.PriorityNone, ekreminders.PriorityNone},
		{model.PriorityHigh, ekreminders.PriorityHigh},
		{model.PriorityMedium, ekreminders.PriorityMedium},
		{model.PriorityLow, ekreminders.PriorityLow},
	}
	for _, tt := range tests {
		if got := priorityToEventKit(tt.p); got != tt.want {
			t.Errorf("priorityToEventKit(%v) = %v, want %v", tt.p, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Round-trip: model.Item → CreateInput → Reminder → model.Item
// ---------------------------------------------------------------------------

func TestConversionRoundTrip(t *testing.T) {
	due := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	mod := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)

	original := &model.Item{
		Title:       "Round trip task",
		Description: "Test notes",
		DueDate:     &due,
		Priority:    model.PriorityHigh,
		Completed:   false,
		ListName:    "Shopping",
	}

	// model.Item → CreateInput
	createInput := itemToCreateInput(original)

	// Simulate what EventKit would return after creation
	ekReminder := &ekreminders.Reminder{
		ID:         "new-uid",
		Title:      createInput.Title,
		Notes:      createInput.Notes,
		List:       createInput.ListName,
		DueDate:    createInput.DueDate,
		Priority:   createInput.Priority,
		Completed:  false,
		ModifiedAt: &mod,
	}

	// Reminder → model.Item
	result := reminderToItem(ekReminder, "Shopping")

	if result.Title != original.Title {
		t.Errorf("Title = %q, want %q", result.Title, original.Title)
	}
	if result.Description != original.Description {
		t.Errorf("Description = %q, want %q", result.Description, original.Description)
	}
	if result.DueDate == nil || !result.DueDate.Equal(due) {
		t.Errorf("DueDate = %v, want %v", result.DueDate, due)
	}
	if result.Priority != original.Priority {
		t.Errorf("Priority = %v, want %v", result.Priority, original.Priority)
	}
	if result.Completed != original.Completed {
		t.Errorf("Completed = %v, want %v", result.Completed, original.Completed)
	}
	if result.ListName != original.ListName {
		t.Errorf("ListName = %q, want %q", result.ListName, original.ListName)
	}

	// ContentHash should be identical since all content fields match
	if result.ContentHash() != original.ContentHash() {
		t.Error("ContentHash mismatch after round-trip — content was not preserved")
	}
}
