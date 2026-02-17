// Package sync implements the bidirectional reconciliation engine for
// ReminderRelay. It compares Apple Reminders items and Home Assistant todo
// items against the state database, detects creates, updates, deletes,
// and conflicts, and dispatches mutations to the appropriate adapter.
//
// The package contains two main components:
//
//   - [Engine] runs the polling loop and optional WebSocket listener.
//   - [Bootstrap] handles first-run title-matching to link existing
//     items on both sides.
package sync

import (
	"context"

	"github.com/njoerd114/reminderrelay/internal/model"
	"github.com/njoerd114/reminderrelay/internal/state"
)

// RemindersSource provides read/write access to Apple Reminders items.
// Implemented by [reminders.Adapter].
type RemindersSource interface {
	FetchAll(ctx context.Context, listNames []string) ([]*model.Item, error)
	Create(ctx context.Context, item *model.Item) (uid string, err error)
	Update(ctx context.Context, uid string, item *model.Item) error
	Delete(ctx context.Context, uid string) error
}

// HASource provides read/write access to Home Assistant todo items.
// Implemented by [homeassistant.Adapter].
type HASource interface {
	GetItems(ctx context.Context, entityID string) ([]model.Item, error)
	AddItem(ctx context.Context, entityID string, item *model.Item) error
	UpdateItem(ctx context.Context, entityID, currentTitle string, item *model.Item) error
	RemoveItem(ctx context.Context, entityID, title string) error
}

// StateStore provides access to the sync state database.
// Implemented by [state.Store].
type StateStore interface {
	GetItemByRemindersUID(ctx context.Context, uid string) (*state.Item, error)
	GetItemByHAUID(ctx context.Context, uid string) (*state.Item, error)
	GetAllItemsForList(ctx context.Context, listName string) ([]*state.Item, error)
	UpsertItem(ctx context.Context, item *state.Item) error
	DeleteItem(ctx context.Context, id int64) error
	IsEmpty(ctx context.Context) (bool, error)
}
