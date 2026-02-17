package sync

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/njoerd114/reminderrelay/internal/model"
	"github.com/njoerd114/reminderrelay/internal/state"
)

var (
	testLogger   = slog.Default()
	testMappings = map[string]string{"Shopping": "todo.shopping"}
)

func newItem(uid, title, listName string, priority model.Priority, completed bool, modifiedAt time.Time) *model.Item {
	return &model.Item{
		UID:        uid,
		Title:      title,
		ListName:   listName,
		Priority:   priority,
		Completed:  completed,
		ModifiedAt: modifiedAt,
	}
}

// ---------------------------------------------------------------------------
// Scenario 1: Item exists only in Reminders → created in HA
// ---------------------------------------------------------------------------

func TestReconcile_NewReminderItem_CreatedInHA(t *testing.T) {
	now := time.Now().UTC()
	remItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityHigh, false, now)

	rem := newMockReminders(remItem)
	ha := newMockHA()
	store := newMockStore()

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Created != 1 {
		t.Errorf("Created = %d, want 1", stats.Created)
	}

	// HA should have the item.
	haItems := ha.getItems("todo.shopping")
	if len(haItems) != 1 {
		t.Fatalf("HA items = %d, want 1", len(haItems))
	}
	if haItems[0].Title != "Buy milk" {
		t.Errorf("HA item title = %q, want %q", haItems[0].Title, "Buy milk")
	}

	// State DB should have a mapping.
	if store.count() != 1 {
		t.Errorf("state items = %d, want 1", store.count())
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Item exists only in HA → created in Reminders
// ---------------------------------------------------------------------------

func TestReconcile_NewHAItem_CreatedInReminders(t *testing.T) {
	now := time.Now().UTC()

	rem := newMockReminders()
	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:        "ha-1",
		Title:      "Buy eggs",
		Priority:   model.PriorityNone,
		ModifiedAt: now,
	})
	store := newMockStore()

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Created != 1 {
		t.Errorf("Created = %d, want 1", stats.Created)
	}

	// Reminders should have the item.
	if rem.count() != 1 {
		t.Errorf("Reminders items = %d, want 1", rem.count())
	}

	// State DB should have a mapping.
	if store.count() != 1 {
		t.Errorf("state items = %d, want 1", store.count())
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: Both sides updated, Reminders newer → Reminders wins
// ---------------------------------------------------------------------------

func TestReconcile_Conflict_RemindersWins(t *testing.T) {
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	remTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	haTime := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)

	// State DB: item was synced with some hash at older time.
	origItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, older)
	origHash := origItem.ContentHash()

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID:      "rem-1",
		HAUID:             "ha-1",
		ListName:          "Shopping",
		Title:             "Buy milk",
		LastSyncHash:      origHash,
		RemindersModified: older,
		HAModified:        older,
		LastSyncedAt:      older,
	})

	// Reminders: title changed to "Buy whole milk" (newer).
	remItem := newItem("rem-1", "Buy whole milk", "Shopping", model.PriorityNone, false, remTime)
	rem := newMockReminders(remItem)

	// HA: title changed to "Buy skim milk" (older).
	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:        "ha-1",
		Title:      "Buy skim milk",
		Priority:   model.PriorityNone,
		ModifiedAt: haTime,
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Updated != 1 {
		t.Errorf("Updated = %d, want 1", stats.Updated)
	}
	if stats.Conflicts != 1 {
		t.Errorf("Conflicts = %d, want 1", stats.Conflicts)
	}

	// HA should have Reminders' version.
	haItems := ha.getItems("todo.shopping")
	if len(haItems) != 1 || haItems[0].Title != "Buy whole milk" {
		t.Errorf("HA item title = %q, want %q", haItems[0].Title, "Buy whole milk")
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: Both sides updated, HA newer → HA wins
// ---------------------------------------------------------------------------

func TestReconcile_Conflict_HAWins(t *testing.T) {
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	remTime := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	haTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	origItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, older)
	origHash := origItem.ContentHash()

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID:      "rem-1",
		HAUID:             "ha-1",
		ListName:          "Shopping",
		Title:             "Buy milk",
		LastSyncHash:      origHash,
		RemindersModified: older,
		HAModified:        older,
		LastSyncedAt:      older,
	})

	remItem := newItem("rem-1", "Buy skim milk", "Shopping", model.PriorityNone, false, remTime)
	rem := newMockReminders(remItem)

	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:        "ha-1",
		Title:      "Buy whole milk",
		Priority:   model.PriorityNone,
		ModifiedAt: haTime,
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Updated != 1 {
		t.Errorf("Updated = %d, want 1", stats.Updated)
	}

	// Reminders should have HA's version.
	got := rem.get("rem-1")
	if got == nil || got.Title != "Buy whole milk" {
		title := ""
		if got != nil {
			title = got.Title
		}
		t.Errorf("Reminders item title = %q, want %q", title, "Buy whole milk")
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: Deleted from Reminders → removed from HA + state DB
// ---------------------------------------------------------------------------

func TestReconcile_DeletedFromReminders_RemovedFromHA(t *testing.T) {
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		ListName:     "Shopping",
		Title:        "Buy milk",
		LastSyncHash: "old-hash",
		LastSyncedAt: older,
	})

	// Reminders: item gone.
	rem := newMockReminders()

	// HA: item still exists.
	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:   "ha-1",
		Title: "Buy milk",
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", stats.Deleted)
	}

	// HA should be empty.
	if len(ha.getItems("todo.shopping")) != 0 {
		t.Error("HA item should have been deleted")
	}

	// State DB should be empty.
	if store.count() != 0 {
		t.Error("state DB should be empty")
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: Deleted from HA → removed from Reminders + state DB
// ---------------------------------------------------------------------------

func TestReconcile_DeletedFromHA_RemovedFromReminders(t *testing.T) {
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	remItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, older)

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		ListName:     "Shopping",
		Title:        "Buy milk",
		LastSyncHash: "old-hash",
		LastSyncedAt: older,
	})

	rem := newMockReminders(remItem)
	ha := newMockHA() // HA: item gone

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", stats.Deleted)
	}

	// Reminders should be empty.
	if rem.count() != 0 {
		t.Error("Reminders item should have been deleted")
	}

	// State DB should be empty.
	if store.count() != 0 {
		t.Error("state DB should be empty")
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: No changes → no mutations (idempotent pass)
// ---------------------------------------------------------------------------

func TestReconcile_NoChanges_Idempotent(t *testing.T) {
	now := time.Now().UTC()
	remItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, now)
	hash := remItem.ContentHash()

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		ListName:     "Shopping",
		Title:        "Buy milk",
		LastSyncHash: hash,
		LastSyncedAt: now,
	})

	rem := newMockReminders(remItem)
	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:        "ha-1",
		Title:      "Buy milk",
		Priority:   model.PriorityNone,
		Completed:  false,
		ModifiedAt: now,
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Created != 0 || stats.Updated != 0 || stats.Deleted != 0 {
		t.Errorf("expected no mutations, got Created=%d Updated=%d Deleted=%d",
			stats.Created, stats.Updated, stats.Deleted)
	}
	if stats.Errors != 0 {
		t.Errorf("Errors = %d, want 0", stats.Errors)
	}
}

// ---------------------------------------------------------------------------
// Scenario: Only Reminders changed → propagate to HA (no conflict)
// ---------------------------------------------------------------------------

func TestReconcile_OnlyRemindersChanged_UpdatesHA(t *testing.T) {
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	origItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, older)
	origHash := origItem.ContentHash()

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		ListName:     "Shopping",
		Title:        "Buy milk",
		LastSyncHash: origHash,
		LastSyncedAt: older,
	})

	// Reminders: updated title.
	remItem := newItem("rem-1", "Buy whole milk", "Shopping", model.PriorityNone, false, newer)
	rem := newMockReminders(remItem)

	// HA: unchanged (same hash as original).
	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:        "ha-1",
		Title:      "Buy milk",
		Priority:   model.PriorityNone,
		ModifiedAt: older,
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Updated != 1 {
		t.Errorf("Updated = %d, want 1", stats.Updated)
	}
	if stats.Conflicts != 0 {
		t.Errorf("Conflicts = %d, want 0 (only one side changed)", stats.Conflicts)
	}

	// HA should have updated title.
	haItems := ha.getItems("todo.shopping")
	if len(haItems) != 1 || haItems[0].Title != "Buy whole milk" {
		t.Errorf("HA item title = %q, want %q", haItems[0].Title, "Buy whole milk")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Only HA changed → propagate to Reminders (no conflict)
// ---------------------------------------------------------------------------

func TestReconcile_OnlyHAChanged_UpdatesReminders(t *testing.T) {
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	origItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, older)
	origHash := origItem.ContentHash()

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		ListName:     "Shopping",
		Title:        "Buy milk",
		LastSyncHash: origHash,
		LastSyncedAt: older,
	})

	// Reminders: unchanged.
	remItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, older)
	rem := newMockReminders(remItem)

	// HA: updated title.
	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:        "ha-1",
		Title:      "Buy whole milk",
		Priority:   model.PriorityNone,
		ModifiedAt: newer,
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Updated != 1 {
		t.Errorf("Updated = %d, want 1", stats.Updated)
	}

	got := rem.get("rem-1")
	if got == nil || got.Title != "Buy whole milk" {
		title := ""
		if got != nil {
			title = got.Title
		}
		t.Errorf("Reminders item title = %q, want %q", title, "Buy whole milk")
	}
}

// ---------------------------------------------------------------------------
// Scenario: Multiple items across lists
// ---------------------------------------------------------------------------

func TestReconcile_MultipleItems(t *testing.T) {
	now := time.Now().UTC()

	rem := newMockReminders(
		newItem("rem-1", "Existing", "Shopping", model.PriorityNone, false, now),
		newItem("rem-2", "New from Rem", "Shopping", model.PriorityNone, false, now),
	)

	ha := newMockHA()
	ha.addItems("todo.shopping",
		model.Item{UID: "ha-1", Title: "Existing", ModifiedAt: now},
		model.Item{UID: "ha-3", Title: "New from HA", ModifiedAt: now},
	)

	// Only "Existing" is tracked.
	existingItem := newItem("rem-1", "Existing", "Shopping", model.PriorityNone, false, now)
	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		ListName:     "Shopping",
		Title:        "Existing",
		LastSyncHash: existingItem.ContentHash(),
		LastSyncedAt: now,
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "New from Rem" → created in HA, "New from HA" → created in Reminders.
	if stats.Created != 2 {
		t.Errorf("Created = %d, want 2", stats.Created)
	}
	if stats.Updated != 0 {
		t.Errorf("Updated = %d, want 0", stats.Updated)
	}

	// State should have 3 entries total.
	if store.count() != 3 {
		t.Errorf("state items = %d, want 3", store.count())
	}
}

// ---------------------------------------------------------------------------
// Scenario: Completed status changed in Reminders → propagate to HA
// ---------------------------------------------------------------------------

func TestReconcile_CompletedStatusChange(t *testing.T) {
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	origItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, false, older)
	origHash := origItem.ContentHash()

	store := newMockStore()
	store.seed(&state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		ListName:     "Shopping",
		Title:        "Buy milk",
		LastSyncHash: origHash,
		LastSyncedAt: older,
	})

	// Reminders: completed.
	remItem := newItem("rem-1", "Buy milk", "Shopping", model.PriorityNone, true, newer)
	rem := newMockReminders(remItem)

	// HA: unchanged.
	ha := newMockHA()
	ha.addItems("todo.shopping", model.Item{
		UID:        "ha-1",
		Title:      "Buy milk",
		Priority:   model.PriorityNone,
		Completed:  false,
		ModifiedAt: older,
	})

	r := NewReconciler(rem, ha, store, testLogger)
	stats, err := r.Run(context.Background(), testMappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Updated != 1 {
		t.Errorf("Updated = %d, want 1", stats.Updated)
	}

	haItems := ha.getItems("todo.shopping")
	if len(haItems) != 1 || !haItems[0].Completed {
		t.Error("HA item should be completed after sync")
	}
}

// ---------------------------------------------------------------------------
// decide() unit tests
// ---------------------------------------------------------------------------

func TestDecide_BothDeleted(t *testing.T) {
	r := NewReconciler(nil, nil, nil, testLogger)
	si := &state.Item{RemindersUID: "rem-1", HAUID: "ha-1"}
	got := r.decide(si, nil, nil)
	if got != actionDeleteFromHA {
		t.Errorf("decide(both deleted) = %v, want actionDeleteFromHA", got)
	}
}

func TestDecide_EqualTimestampsFavourReminders(t *testing.T) {
	sameTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	origHash := "different-from-both"

	si := &state.Item{
		RemindersUID: "rem-1",
		HAUID:        "ha-1",
		LastSyncHash: origHash,
	}
	remItem := newItem("rem-1", "A", "Shopping", model.PriorityNone, false, sameTime)
	haItem := newItem("ha-1", "B", "Shopping", model.PriorityNone, false, sameTime)

	r := NewReconciler(nil, nil, nil, testLogger)
	got := r.decide(si, remItem, haItem)
	if got != actionUpdateHA {
		t.Errorf("decide(equal timestamps) = %v, want actionUpdateHA (Reminders wins)", got)
	}
}
