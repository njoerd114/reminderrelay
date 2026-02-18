package reminders

import (
	ekreminders "github.com/BRO3886/go-eventkit/reminders"

	"github.com/njoerd114/reminderrelay/internal/model"
)

// reminderToItem converts an EventKit Reminder to a normalised model.Item.
// listName is passed explicitly because the go-eventkit Reminder.List field
// contains the list name as reported by EventKit, which may differ from the
// config mapping key in edge cases (e.g. leading/trailing whitespace).
func reminderToItem(r *ekreminders.Reminder, listName string) *model.Item {
	item := &model.Item{
		UID:         r.ID,
		Title:       r.Title,
		Description: r.Notes,
		Priority:    model.NormalizePriority(int(r.Priority)),
		Completed:   r.Completed,
		ListName:    listName,
	}

	if r.DueDate != nil {
		t := *r.DueDate
		item.DueDate = &t
	}

	if r.ModifiedAt != nil {
		item.ModifiedAt = *r.ModifiedAt
	}

	return item
}

// itemToCreateInput builds an EventKit CreateReminderInput from a model.Item.
func itemToCreateInput(item *model.Item) ekreminders.CreateReminderInput {
	input := ekreminders.CreateReminderInput{
		Title:    item.Title,
		Notes:    item.Description,
		ListName: item.ListName,
		Priority: priorityToEventKit(item.Priority),
	}

	if item.DueDate != nil {
		t := *item.DueDate
		input.DueDate = &t
	}

	return input
}

// itemToUpdateInput builds an EventKit UpdateReminderInput from a model.Item.
// All syncable fields are set so the update is a full overwrite rather than a
// partial patch — this matches the sync engine's semantics where the winning
// side's complete state is applied.
func itemToUpdateInput(item *model.Item) ekreminders.UpdateReminderInput {
	title := item.Title
	notes := item.Description
	prio := priorityToEventKit(item.Priority)

	input := ekreminders.UpdateReminderInput{
		Title:    &title,
		Notes:    &notes,
		Priority: &prio,
	}

	if item.DueDate != nil {
		t := *item.DueDate
		input.DueDate = &t
	} else {
		input.ClearDueDate = true
	}

	// Completed is handled separately in Adapter.Update via the dedicated
	// CompleteReminder / UncompleteReminder APIs, so we intentionally leave
	// input.Completed as nil here.

	return input
}

// priorityToEventKit maps a model.Priority back to the EventKit constant.
// The mapping is lossless because model.Priority values are a subset of
// EventKit priorities (0, 1, 5, 9).
func priorityToEventKit(p model.Priority) ekreminders.Priority {
	switch p {
	case model.PriorityHigh:
		return ekreminders.PriorityHigh
	case model.PriorityMedium:
		return ekreminders.PriorityMedium
	case model.PriorityLow:
		return ekreminders.PriorityLow
	default:
		return ekreminders.PriorityNone
	}
}
