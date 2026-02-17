package homeassistant

import (
	"testing"
	"time"

	"github.com/njoerd114/reminderrelay/internal/model"
)

// ---------------------------------------------------------------------------
// haItemToModelItem
// ---------------------------------------------------------------------------

func TestHAItemToModelItem_FullFields(t *testing.T) {
	h := haTodoItem{
		UID:         "ha-uid-123",
		Summary:     "Buy groceries",
		Status:      statusNeedsAction,
		Description: "[High] Whole milk and eggs",
		Due:         "2026-03-15",
	}

	got := haItemToModelItem(h)

	if got.UID != "ha-uid-123" {
		t.Errorf("UID = %q, want %q", got.UID, "ha-uid-123")
	}
	if got.Title != "Buy groceries" {
		t.Errorf("Title = %q, want %q", got.Title, "Buy groceries")
	}
	if got.Description != "Whole milk and eggs" {
		t.Errorf("Description = %q, want %q (priority prefix should be stripped)", got.Description, "Whole milk and eggs")
	}
	if got.Priority != model.PriorityHigh {
		t.Errorf("Priority = %v, want %v", got.Priority, model.PriorityHigh)
	}
	if got.Completed {
		t.Error("Completed = true, want false")
	}
	if got.DueDate == nil {
		t.Fatal("DueDate = nil, want 2026-03-15")
	}
	want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if !got.DueDate.Equal(want) {
		t.Errorf("DueDate = %v, want %v", got.DueDate, want)
	}
}

func TestHAItemToModelItem_CompletedStatus(t *testing.T) {
	h := haTodoItem{
		UID:     "done-1",
		Summary: "Done task",
		Status:  statusCompleted,
	}
	got := haItemToModelItem(h)
	if !got.Completed {
		t.Error("Completed = false, want true for status=completed")
	}
}

func TestHAItemToModelItem_NoPriorityPrefix(t *testing.T) {
	h := haTodoItem{
		UID:         "no-prio-1",
		Summary:     "Plain task",
		Status:      statusNeedsAction,
		Description: "Just a note",
	}
	got := haItemToModelItem(h)
	if got.Priority != model.PriorityNone {
		t.Errorf("Priority = %v, want %v", got.Priority, model.PriorityNone)
	}
	if got.Description != "Just a note" {
		t.Errorf("Description = %q, want %q", got.Description, "Just a note")
	}
}

func TestHAItemToModelItem_MediumPriority(t *testing.T) {
	h := haTodoItem{
		UID:         "med-1",
		Summary:     "Medium task",
		Description: "[Medium] Some details",
	}
	got := haItemToModelItem(h)
	if got.Priority != model.PriorityMedium {
		t.Errorf("Priority = %v, want %v", got.Priority, model.PriorityMedium)
	}
	if got.Description != "Some details" {
		t.Errorf("Description = %q, want %q", got.Description, "Some details")
	}
}

func TestHAItemToModelItem_LowPriority(t *testing.T) {
	h := haTodoItem{
		UID:         "low-1",
		Summary:     "Low task",
		Description: "[Low] Not urgent",
	}
	got := haItemToModelItem(h)
	if got.Priority != model.PriorityLow {
		t.Errorf("Priority = %v, want %v", got.Priority, model.PriorityLow)
	}
}

func TestHAItemToModelItem_NoDueDate(t *testing.T) {
	h := haTodoItem{
		UID:     "nodue-1",
		Summary: "No deadline",
		Status:  statusNeedsAction,
	}
	got := haItemToModelItem(h)
	if got.DueDate != nil {
		t.Errorf("DueDate = %v, want nil", got.DueDate)
	}
}

func TestHAItemToModelItem_RFC3339DueDate(t *testing.T) {
	h := haTodoItem{
		UID:     "rfc3339-1",
		Summary: "Datetime due",
		Due:     "2026-04-01T14:30:00+02:00",
	}
	got := haItemToModelItem(h)
	if got.DueDate == nil {
		t.Fatal("DueDate = nil, want parsed datetime")
	}
	// Should parse to 2026-04-01T12:30:00Z (UTC equivalent).
	if got.DueDate.Year() != 2026 || got.DueDate.Month() != 4 || got.DueDate.Day() != 1 {
		t.Errorf("DueDate = %v, want 2026-04-01 with time component", got.DueDate)
	}
}

func TestHAItemToModelItem_EmptyDescription(t *testing.T) {
	h := haTodoItem{
		UID:     "empty-desc",
		Summary: "No notes",
		Status:  statusNeedsAction,
	}
	got := haItemToModelItem(h)
	if got.Description != "" {
		t.Errorf("Description = %q, want empty", got.Description)
	}
	if got.Priority != model.PriorityNone {
		t.Errorf("Priority = %v, want %v", got.Priority, model.PriorityNone)
	}
}

// ---------------------------------------------------------------------------
// buildAddItemData
// ---------------------------------------------------------------------------

func TestBuildAddItemData_FullFields(t *testing.T) {
	due := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	item := &model.Item{
		Title:       "New task",
		Description: "Some notes",
		Priority:    model.PriorityHigh,
		DueDate:     &due,
	}

	data := buildAddItemData("todo.shopping", item)

	if data["entity_id"] != "todo.shopping" {
		t.Errorf("entity_id = %v, want todo.shopping", data["entity_id"])
	}
	if data["item"] != "New task" {
		t.Errorf("item = %v, want New task", data["item"])
	}
	if data["description"] != "[High] Some notes" {
		t.Errorf("description = %v, want [High] Some notes", data["description"])
	}
	if data["due_date"] != "2026-05-01" {
		t.Errorf("due_date = %v, want 2026-05-01", data["due_date"])
	}
}

func TestBuildAddItemData_NoPriorityNoDescription(t *testing.T) {
	item := &model.Item{
		Title:    "Simple task",
		Priority: model.PriorityNone,
	}

	data := buildAddItemData("todo.work", item)

	if _, ok := data["description"]; ok {
		t.Errorf("description should be absent for no-priority empty description, got %v", data["description"])
	}
	if _, ok := data["due_date"]; ok {
		t.Errorf("due_date should be absent when nil, got %v", data["due_date"])
	}
}

func TestBuildAddItemData_PriorityOnlyNoDescription(t *testing.T) {
	item := &model.Item{
		Title:    "Priority only",
		Priority: model.PriorityMedium,
	}

	data := buildAddItemData("todo.work", item)

	// "[Medium] " + "" = "[Medium] "
	if data["description"] != "[Medium] " {
		t.Errorf("description = %q, want %q", data["description"], "[Medium] ")
	}
}

// ---------------------------------------------------------------------------
// buildUpdateItemData
// ---------------------------------------------------------------------------

func TestBuildUpdateItemData_TitleChanged(t *testing.T) {
	due := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	item := &model.Item{
		Title:       "Updated title",
		Description: "Updated notes",
		Priority:    model.PriorityLow,
		Completed:   false,
		DueDate:     &due,
	}

	data := buildUpdateItemData("todo.shopping", "Old title", item)

	if data["entity_id"] != "todo.shopping" {
		t.Errorf("entity_id = %v, want todo.shopping", data["entity_id"])
	}
	if data["item"] != "Old title" {
		t.Errorf("item = %v, want Old title", data["item"])
	}
	if data["rename"] != "Updated title" {
		t.Errorf("rename = %v, want Updated title", data["rename"])
	}
	if data["description"] != "[Low] Updated notes" {
		t.Errorf("description = %v, want [Low] Updated notes", data["description"])
	}
	if data["status"] != statusNeedsAction {
		t.Errorf("status = %v, want %s", data["status"], statusNeedsAction)
	}
	if data["due_date"] != "2026-06-01" {
		t.Errorf("due_date = %v, want 2026-06-01", data["due_date"])
	}
}

func TestBuildUpdateItemData_TitleUnchanged(t *testing.T) {
	item := &model.Item{
		Title:     "Same title",
		Completed: true,
	}

	data := buildUpdateItemData("todo.work", "Same title", item)

	if _, ok := data["rename"]; ok {
		t.Error("rename should be absent when title unchanged")
	}
	if data["status"] != statusCompleted {
		t.Errorf("status = %v, want %s", data["status"], statusCompleted)
	}
}

// ---------------------------------------------------------------------------
// buildRemoveItemData
// ---------------------------------------------------------------------------

func TestBuildRemoveItemData(t *testing.T) {
	data := buildRemoveItemData("todo.shopping", "Old item")

	if data["entity_id"] != "todo.shopping" {
		t.Errorf("entity_id = %v, want todo.shopping", data["entity_id"])
	}
	if data["item"] != "Old item" {
		t.Errorf("item = %v, want Old item", data["item"])
	}
}

// ---------------------------------------------------------------------------
// parseDue / formatDue
// ---------------------------------------------------------------------------

func TestParseDue_DateOnly(t *testing.T) {
	got, err := parseDue("2026-03-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("parseDue = %v, want %v", got, want)
	}
}

func TestParseDue_RFC3339(t *testing.T) {
	got, err := parseDue("2026-04-01T14:30:00+02:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Year() != 2026 || got.Month() != 4 || got.Day() != 1 {
		t.Errorf("parseDue date = %v, want 2026-04-01", got)
	}
}

func TestParseDue_Invalid(t *testing.T) {
	_, err := parseDue("not-a-date")
	if err == nil {
		t.Error("expected error for invalid date, got nil")
	}
}

func TestFormatDue(t *testing.T) {
	d := time.Date(2026, 12, 25, 10, 30, 0, 0, time.UTC)
	got := formatDue(&d)
	if got != "2026-12-25" {
		t.Errorf("formatDue = %q, want %q", got, "2026-12-25")
	}
}

// ---------------------------------------------------------------------------
// Round-trip: model.Item → addData → haTodoItem → model.Item
// ---------------------------------------------------------------------------

func TestConversionRoundTrip(t *testing.T) {
	due := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)
	original := &model.Item{
		Title:       "Independence Day",
		Description: "Fireworks shopping",
		Priority:    model.PriorityHigh,
		Completed:   false,
		DueDate:     &due,
	}

	// model.Item → addData
	data := buildAddItemData("todo.events", original)

	// Simulate what HA would return via get_items
	haItem := haTodoItem{
		UID:         "ha-new-uid",
		Summary:     data["item"].(string),
		Description: data["description"].(string),
		Status:      statusNeedsAction,
		Due:         data["due_date"].(string),
	}

	// haTodoItem → model.Item
	result := haItemToModelItem(haItem)

	if result.Title != original.Title {
		t.Errorf("Title = %q, want %q", result.Title, original.Title)
	}
	if result.Description != original.Description {
		t.Errorf("Description = %q, want %q", result.Description, original.Description)
	}
	if result.Priority != original.Priority {
		t.Errorf("Priority = %v, want %v", result.Priority, original.Priority)
	}
	if result.Completed != original.Completed {
		t.Errorf("Completed = %v, want %v", result.Completed, original.Completed)
	}

	// Due dates should match day-level (HA stores date-only).
	if result.DueDate == nil {
		t.Fatal("DueDate = nil after round-trip")
	}
	if result.DueDate.Year() != due.Year() || result.DueDate.Month() != due.Month() || result.DueDate.Day() != due.Day() {
		t.Errorf("DueDate = %v, want %v (date part)", result.DueDate, due)
	}

	// Content hashes should match since all content fields are preserved.
	if result.ContentHash() != original.ContentHash() {
		t.Error("ContentHash mismatch after round-trip — content was not preserved")
	}
}
