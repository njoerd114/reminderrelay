package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/njoerd114/reminderrelay/internal/model"
	"github.com/njoerd114/reminderrelay/internal/state"
)

// action describes a single mutation the reconciler wants to perform.
type action int

const (
	actionNone         action = iota
	actionCreateInHA          // item exists in Reminders only → push to HA
	actionCreateInRem         // item exists in HA only → push to Reminders
	actionUpdateHA            // Reminders is the winner → push to HA
	actionUpdateRem           // HA is the winner → push to Reminders
	actionDeleteFromHA        // item deleted from Reminders → remove from HA
	actionDeleteFromRem       // item deleted from HA → remove from Reminders
)

// Stats tracks the number of mutations performed in a single reconcile pass.
type Stats struct {
	Created  int
	Updated  int
	Deleted  int
	Conflicts int
	Errors   int
}

// Reconciler performs a single bidirectional sync pass across all configured
// list mappings. It is stateless between calls — all persistent state lives
// in the [StateStore].
type Reconciler struct {
	rem   RemindersSource
	ha    HASource
	store StateStore
	log   *slog.Logger
}

// NewReconciler creates a Reconciler wired to the given adapters and state store.
func NewReconciler(rem RemindersSource, ha HASource, store StateStore, logger *slog.Logger) *Reconciler {
	return &Reconciler{rem: rem, ha: ha, store: store, log: logger}
}

// Run performs a full bidirectional sync for all list mappings. It returns
// aggregate statistics and the first error encountered (sync continues past
// individual item errors to maximise progress).
func (r *Reconciler) Run(ctx context.Context, listMappings map[string]string) (Stats, error) {
	var stats Stats
	var firstErr error

	listNames := make([]string, 0, len(listMappings))
	for name := range listMappings {
		listNames = append(listNames, name)
	}

	// 1. Fetch all Reminders items across configured lists.
	remItems, err := r.rem.FetchAll(ctx, listNames)
	if err != nil {
		return stats, fmt.Errorf("fetching reminders: %w", err)
	}

	// Index Reminders items by UID for fast lookup.
	remByUID := make(map[string]*model.Item, len(remItems))
	for _, item := range remItems {
		remByUID[item.UID] = item
	}

	// 2. Process each list mapping independently.
	for listName, entityID := range listMappings {
		ls, err := r.reconcileList(ctx, listName, entityID, remByUID)
		stats.Created += ls.Created
		stats.Updated += ls.Updated
		stats.Deleted += ls.Deleted
		stats.Conflicts += ls.Conflicts
		stats.Errors += ls.Errors
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	r.log.Info("reconcile complete",
		"created", stats.Created,
		"updated", stats.Updated,
		"deleted", stats.Deleted,
		"conflicts", stats.Conflicts,
		"errors", stats.Errors,
	)

	return stats, firstErr
}

// ReconcileEntity performs reconciliation for a single HA entity. Called by
// the WebSocket listener when a state_changed event is received.
func (r *Reconciler) ReconcileEntity(ctx context.Context, listName, entityID string) (Stats, error) {
	// We need the Reminders items for just this list.
	remItems, err := r.rem.FetchAll(ctx, []string{listName})
	if err != nil {
		return Stats{}, fmt.Errorf("fetching reminders for %q: %w", listName, err)
	}

	remByUID := make(map[string]*model.Item, len(remItems))
	for _, item := range remItems {
		remByUID[item.UID] = item
	}

	return r.reconcileList(ctx, listName, entityID, remByUID)
}

// reconcileList performs bidirectional sync for a single list ↔ entity pair.
func (r *Reconciler) reconcileList(ctx context.Context, listName, entityID string, remByUID map[string]*model.Item) (Stats, error) {
	var stats Stats
	var firstErr error

	r.log.Debug("reconciling list", "list", listName, "entity", entityID)

	// Fetch HA items for this entity.
	haItems, err := r.ha.GetItems(ctx, entityID)
	if err != nil {
		return stats, fmt.Errorf("fetching HA items for %s: %w", entityID, err)
	}

	// Index HA items by UID.
	haByUID := make(map[string]*model.Item, len(haItems))
	for i := range haItems {
		haItems[i].ListName = listName
		haByUID[haItems[i].UID] = &haItems[i]
	}

	// Fetch all tracked state items for this list.
	stateItems, err := r.store.GetAllItemsForList(ctx, listName)
	if err != nil {
		return stats, fmt.Errorf("fetching state items for %q: %w", listName, err)
	}

	// Build a set of state RemindersUIDs and HAUIDs we've processed,
	// so we can detect new items after processing tracked ones.
	processedRemUIDs := make(map[string]bool, len(stateItems))
	processedHAUIDs := make(map[string]bool, len(stateItems))

	// 1. Process items we're already tracking.
	for _, si := range stateItems {
		remItem := remByUID[si.RemindersUID]
		haItem := haByUID[si.HAUID]

		if si.RemindersUID != "" {
			processedRemUIDs[si.RemindersUID] = true
		}
		if si.HAUID != "" {
			processedHAUIDs[si.HAUID] = true
		}

		act := r.decide(si, remItem, haItem)
		oldHash := si.LastSyncHash // capture before execute modifies si
		if err := r.execute(ctx, act, si, remItem, haItem, entityID); err != nil {
			r.log.Error("sync action failed",
				"action", act,
				"title", si.Title,
				"error", err,
			)
			stats.Errors++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		switch act {
		case actionCreateInHA, actionCreateInRem:
			stats.Created++
		case actionUpdateHA, actionUpdateRem:
			stats.Updated++
			// Check if this was a conflict (both sides changed).
			if remItem != nil && haItem != nil {
				remHash := remItem.ContentHash()
				haHash := haItem.ContentHash()
				if remHash != oldHash && haHash != oldHash {
					stats.Conflicts++
				}
			}
		case actionDeleteFromHA, actionDeleteFromRem:
			stats.Deleted++
		}
	}

	// 2. Detect new Reminders items not in state DB → create in HA.
	for uid, remItem := range remByUID {
		if remItem.ListName != listName {
			continue
		}
		if processedRemUIDs[uid] {
			continue
		}

		r.log.Info("new reminder detected", "title", remItem.Title, "uid", uid)
		if err := r.createInHA(ctx, remItem, entityID); err != nil {
			r.log.Error("failed to create in HA", "title", remItem.Title, "error", err)
			stats.Errors++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		stats.Created++
	}

	// 3. Detect new HA items not in state DB → create in Reminders.
	for uid, haItem := range haByUID {
		if processedHAUIDs[uid] {
			continue
		}

		r.log.Info("new HA item detected", "title", haItem.Title, "uid", uid)
		if err := r.createInReminders(ctx, haItem, entityID); err != nil {
			r.log.Error("failed to create in Reminders", "title", haItem.Title, "error", err)
			stats.Errors++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		stats.Created++
	}

	return stats, firstErr
}

// decide determines what action to take for a tracked item based on hash
// and timestamp comparison.
func (r *Reconciler) decide(si *state.Item, remItem, haItem *model.Item) action {
	remExists := remItem != nil
	haExists := haItem != nil

	// Both deleted → just clean up state (handled as deleteFromHA path).
	if !remExists && !haExists {
		return actionDeleteFromHA // will clean state DB only
	}

	// Deleted from Reminders, still in HA → delete from HA.
	if !remExists && haExists {
		return actionDeleteFromHA
	}

	// Deleted from HA, still in Reminders → delete from Reminders.
	if remExists && !haExists {
		return actionDeleteFromRem
	}

	// Both exist — check for changes via content hash.
	remHash := remItem.ContentHash()
	haHash := haItem.ContentHash()
	remChanged := remHash != si.LastSyncHash
	haChanged := haHash != si.LastSyncHash

	// Neither changed → no-op.
	if !remChanged && !haChanged {
		return actionNone
	}

	// Only one side changed → propagate.
	if remChanged && !haChanged {
		return actionUpdateHA
	}
	if !remChanged && haChanged {
		return actionUpdateRem
	}

	// Both changed → conflict → last-write-wins.
	r.log.Info("conflict detected",
		"title", si.Title,
		"reminders_modified", remItem.ModifiedAt,
		"ha_modified", haItem.ModifiedAt,
	)

	if !remItem.ModifiedAt.Before(haItem.ModifiedAt) {
		// Reminders wins (equal timestamps also favour Reminders as the "primary" source).
		return actionUpdateHA
	}
	return actionUpdateRem
}

// execute dispatches the decided action to the appropriate adapter and
// updates the state DB.
func (r *Reconciler) execute(ctx context.Context, act action, si *state.Item, remItem, haItem *model.Item, entityID string) error {
	now := time.Now().UTC()

	switch act {
	case actionNone:
		return nil

	case actionCreateInHA:
		// Reminders item exists but HA counterpart was deleted → shouldn't normally
		// happen for tracked items; treat as delete from Reminders.
		// Actually this case is: item tracked, Reminders still exists, HA gone →
		// we chose actionDeleteFromRem above. So fall through here is unexpected.
		// This branch handles the edge case defensively.
		return r.store.DeleteItem(ctx, si.ID)

	case actionCreateInRem:
		// Same defensive logic as above.
		return r.store.DeleteItem(ctx, si.ID)

	case actionDeleteFromHA:
		if haItem != nil {
			if err := r.ha.RemoveItem(ctx, entityID, haItem.Title); err != nil {
				return fmt.Errorf("deleting %q from HA: %w", si.Title, err)
			}
		}
		return r.store.DeleteItem(ctx, si.ID)

	case actionDeleteFromRem:
		if remItem != nil {
			if err := r.rem.Delete(ctx, remItem.UID); err != nil {
				return fmt.Errorf("deleting %q from Reminders: %w", si.Title, err)
			}
		}
		return r.store.DeleteItem(ctx, si.ID)

	case actionUpdateHA:
		// Use the HA item's current title to identify it (may differ from
		// state DB title if both sides changed).
		currentHATitle := si.Title
		if haItem != nil {
			currentHATitle = haItem.Title
		}
		if err := r.ha.UpdateItem(ctx, entityID, currentHATitle, remItem); err != nil {
			return fmt.Errorf("updating %q in HA: %w", remItem.Title, err)
		}
		si.Title = remItem.Title
		si.LastSyncHash = remItem.ContentHash()
		si.RemindersModified = remItem.ModifiedAt
		si.LastSyncedAt = now
		return r.store.UpsertItem(ctx, si)

	case actionUpdateRem:
		if err := r.rem.Update(ctx, si.RemindersUID, haItem); err != nil {
			return fmt.Errorf("updating %q in Reminders: %w", haItem.Title, err)
		}
		si.Title = haItem.Title
		si.LastSyncHash = haItem.ContentHash()
		si.HAModified = haItem.ModifiedAt
		si.LastSyncedAt = now
		return r.store.UpsertItem(ctx, si)
	}

	return nil
}

// createInHA pushes a new Reminders item to HA and writes the state DB entry.
func (r *Reconciler) createInHA(ctx context.Context, remItem *model.Item, entityID string) error {
	if err := r.ha.AddItem(ctx, entityID, remItem); err != nil {
		return fmt.Errorf("adding %q to HA: %w", remItem.Title, err)
	}

	// After adding, fetch items again to get the HA UID.
	haItems, err := r.ha.GetItems(ctx, entityID)
	if err != nil {
		return fmt.Errorf("refetching items from %s: %w", entityID, err)
	}

	var haUID string
	for _, h := range haItems {
		if h.Title == remItem.Title {
			haUID = h.UID
			break
		}
	}

	now := time.Now().UTC()
	si := &state.Item{
		RemindersUID:      remItem.UID,
		HAUID:             haUID,
		ListName:          remItem.ListName,
		Title:             remItem.Title,
		LastSyncHash:      remItem.ContentHash(),
		RemindersModified: remItem.ModifiedAt,
		LastSyncedAt:      now,
	}
	return r.store.UpsertItem(ctx, si)
}

// createInReminders pushes a new HA item to Reminders and writes the state DB entry.
func (r *Reconciler) createInReminders(ctx context.Context, haItem *model.Item, entityID string) error {
	uid, err := r.rem.Create(ctx, haItem)
	if err != nil {
		return fmt.Errorf("creating %q in Reminders: %w", haItem.Title, err)
	}

	now := time.Now().UTC()
	si := &state.Item{
		RemindersUID: uid,
		HAUID:        haItem.UID,
		ListName:     haItem.ListName,
		Title:        haItem.Title,
		LastSyncHash: haItem.ContentHash(),
		HAModified:   haItem.ModifiedAt,
		LastSyncedAt: now,
	}
	return r.store.UpsertItem(ctx, si)
}
