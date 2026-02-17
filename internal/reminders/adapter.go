// Package reminders wraps the go-eventkit reminders library and converts
// between native EventKit types and the shared [model.Item] representation.
//
// The adapter exposes only the operations needed by the sync engine. It
// accepts context.Context on every method for API consistency with the
// architectural invariants, even though the underlying cgo calls are
// non-cancellable (sub-200ms latency).
package reminders

import (
	"context"
	"fmt"
	"log/slog"

	ekreminders "github.com/BRO3886/go-eventkit/reminders"

	"github.com/njoerd114/reminderrelay/internal/model"
)

// EventKitClient is the subset of [ekreminders.Client] methods used by the
// adapter. Defining it as an interface allows mock injection in tests.
type EventKitClient interface {
	Reminders(opts ...ekreminders.ListOption) ([]ekreminders.Reminder, error)
	CreateReminder(input ekreminders.CreateReminderInput) (*ekreminders.Reminder, error)
	UpdateReminder(id string, input ekreminders.UpdateReminderInput) (*ekreminders.Reminder, error)
	DeleteReminder(id string) error
	CompleteReminder(id string) (*ekreminders.Reminder, error)
	UncompleteReminder(id string) (*ekreminders.Reminder, error)
}

// Adapter provides sync-engine–oriented operations on Apple Reminders via
// EventKit. Create one with [NewAdapter] or [NewAdapterWithClient].
type Adapter struct {
	client EventKitClient
	log    *slog.Logger
}

// NewAdapter creates an Adapter backed by a real EventKit client.
// This triggers the macOS TCC permissions prompt on first use.
func NewAdapter(logger *slog.Logger) (*Adapter, error) {
	c, err := ekreminders.New()
	if err != nil {
		return nil, fmt.Errorf("initialising reminders client: %w", err)
	}
	return &Adapter{client: c, log: logger}, nil
}

// NewAdapterWithClient creates an Adapter with a caller-supplied client.
// Intended for testing with a mock [EventKitClient].
func NewAdapterWithClient(client EventKitClient, logger *slog.Logger) *Adapter {
	return &Adapter{client: client, log: logger}
}

// FetchAll returns all reminders (completed and incomplete) across the given
// list names, converted to [model.Item].
func (a *Adapter) FetchAll(ctx context.Context, listNames []string) ([]*model.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("fetch all reminders: %w", err)
	}

	var items []*model.Item
	for _, name := range listNames {
		a.log.Debug("fetching reminders", "list", name)

		rems, err := a.client.Reminders(ekreminders.WithList(name))
		if err != nil {
			return nil, fmt.Errorf("fetching reminders for list %q: %w", name, err)
		}

		for i := range rems {
			items = append(items, reminderToItem(&rems[i], name))
		}
		a.log.Debug("fetched reminders", "list", name, "count", len(rems))
	}
	return items, nil
}

// Create creates a new reminder from a [model.Item] and returns the
// UID assigned by EventKit.
func (a *Adapter) Create(ctx context.Context, item *model.Item) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("create reminder: %w", err)
	}

	input := itemToCreateInput(item)
	a.log.Debug("creating reminder", "title", item.Title, "list", item.ListName)

	rem, err := a.client.CreateReminder(input)
	if err != nil {
		return "", fmt.Errorf("creating reminder %q in list %q: %w", item.Title, item.ListName, err)
	}

	// If the item should be completed, mark it now — CreateReminder always
	// creates an incomplete reminder.
	if item.Completed {
		if _, err := a.client.CompleteReminder(rem.ID); err != nil {
			return rem.ID, fmt.Errorf("marking new reminder %q as completed: %w", rem.ID, err)
		}
	}

	return rem.ID, nil
}

// Update applies the fields from a [model.Item] to an existing reminder.
func (a *Adapter) Update(ctx context.Context, uid string, item *model.Item) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("update reminder: %w", err)
	}

	a.log.Debug("updating reminder", "uid", uid, "title", item.Title)

	// Fetch current state to decide if completion status changed.
	input := itemToUpdateInput(item)
	updated, err := a.client.UpdateReminder(uid, input)
	if err != nil {
		return fmt.Errorf("updating reminder %q: %w", uid, err)
	}

	// Handle completion status change through the dedicated API so that
	// CompletionDate is set/cleared properly.
	if item.Completed && !updated.Completed {
		if _, err := a.client.CompleteReminder(uid); err != nil {
			return fmt.Errorf("completing reminder %q: %w", uid, err)
		}
	} else if !item.Completed && updated.Completed {
		if _, err := a.client.UncompleteReminder(uid); err != nil {
			return fmt.Errorf("uncompleting reminder %q: %w", uid, err)
		}
	}

	return nil
}

// Delete permanently removes a reminder by UID.
func (a *Adapter) Delete(ctx context.Context, uid string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}

	a.log.Debug("deleting reminder", "uid", uid)
	if err := a.client.DeleteReminder(uid); err != nil {
		return fmt.Errorf("deleting reminder %q: %w", uid, err)
	}
	return nil
}
