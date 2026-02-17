package sync

import (
	"context"
	"fmt"
	"sync"

	"github.com/njoerd114/reminderrelay/internal/model"
	"github.com/njoerd114/reminderrelay/internal/state"
)

// --- Mock Reminders Source ---------------------------------------------------

type mockReminders struct {
	mu    sync.Mutex
	items map[string]*model.Item // UID → Item
	nextUID int
}

func newMockReminders(items ...*model.Item) *mockReminders {
	m := &mockReminders{items: make(map[string]*model.Item), nextUID: len(items)}
	for _, item := range items {
		m.items[item.UID] = item
	}
	return m
}

func (m *mockReminders) FetchAll(_ context.Context, listNames []string) ([]*model.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	nameSet := make(map[string]bool, len(listNames))
	for _, n := range listNames {
		nameSet[n] = true
	}

	var result []*model.Item
	for _, item := range m.items {
		if nameSet[item.ListName] {
			result = append(result, item)
		}
	}
	return result, nil
}

func (m *mockReminders) Create(_ context.Context, item *model.Item) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextUID++
	uid := fmt.Sprintf("rem-%d", m.nextUID)
	cp := *item
	cp.UID = uid
	m.items[uid] = &cp
	return uid, nil
}

func (m *mockReminders) Update(_ context.Context, uid string, item *model.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.items[uid]
	if !ok {
		return fmt.Errorf("reminder %q not found", uid)
	}
	existing.Title = item.Title
	existing.Description = item.Description
	existing.DueDate = item.DueDate
	existing.Priority = item.Priority
	existing.Completed = item.Completed
	existing.ModifiedAt = item.ModifiedAt
	return nil
}

func (m *mockReminders) Delete(_ context.Context, uid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.items[uid]; !ok {
		return fmt.Errorf("reminder %q not found", uid)
	}
	delete(m.items, uid)
	return nil
}

func (m *mockReminders) get(uid string) *model.Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[uid]
}

func (m *mockReminders) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// --- Mock HA Source -----------------------------------------------------------

type mockHA struct {
	mu      sync.Mutex
	items   map[string][]model.Item // entityID → items
	nextUID int
}

func newMockHA() *mockHA {
	return &mockHA{items: make(map[string][]model.Item), nextUID: 100}
}

func (m *mockHA) addItems(entityID string, items ...model.Item) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[entityID] = append(m.items[entityID], items...)
}

func (m *mockHA) GetItems(_ context.Context, entityID string) ([]model.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	items := m.items[entityID]
	// Return copies.
	result := make([]model.Item, len(items))
	copy(result, items)
	return result, nil
}

func (m *mockHA) AddItem(_ context.Context, entityID string, item *model.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextUID++
	cp := *item
	cp.UID = fmt.Sprintf("ha-%d", m.nextUID)
	m.items[entityID] = append(m.items[entityID], cp)
	return nil
}

func (m *mockHA) UpdateItem(_ context.Context, entityID, currentTitle string, item *model.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	items := m.items[entityID]
	for i, h := range items {
		if h.Title == currentTitle {
			items[i].Title = item.Title
			items[i].Description = item.Description
			items[i].DueDate = item.DueDate
			items[i].Priority = item.Priority
			items[i].Completed = item.Completed
			items[i].ModifiedAt = item.ModifiedAt
			return nil
		}
	}
	return fmt.Errorf("item %q not found in %s", currentTitle, entityID)
}

func (m *mockHA) RemoveItem(_ context.Context, entityID, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	items := m.items[entityID]
	for i, h := range items {
		if h.Title == title {
			m.items[entityID] = append(items[:i], items[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("item %q not found in %s", title, entityID)
}

func (m *mockHA) getItems(entityID string) []model.Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[entityID]
}

// --- Mock State Store --------------------------------------------------------

type mockStore struct {
	mu    sync.Mutex
	items map[int64]*state.Item
	nextID int64
}

func newMockStore() *mockStore {
	return &mockStore{items: make(map[int64]*state.Item)}
}

func (m *mockStore) seed(items ...*state.Item) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range items {
		m.nextID++
		item.ID = m.nextID
		m.items[item.ID] = item
	}
}

func (m *mockStore) GetItemByRemindersUID(_ context.Context, uid string) (*state.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.items {
		if item.RemindersUID == uid {
			cp := *item
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *mockStore) GetItemByHAUID(_ context.Context, uid string) (*state.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.items {
		if item.HAUID == uid {
			cp := *item
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *mockStore) GetAllItemsForList(_ context.Context, listName string) ([]*state.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*state.Item
	for _, item := range m.items {
		if item.ListName == listName {
			cp := *item
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (m *mockStore) UpsertItem(_ context.Context, item *state.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item.ID == 0 {
		// Check for existing by RemindersUID.
		for _, existing := range m.items {
			if item.RemindersUID != "" && existing.RemindersUID == item.RemindersUID {
				item.ID = existing.ID
				*existing = *item
				return nil
			}
		}
		m.nextID++
		item.ID = m.nextID
	}
	cp := *item
	m.items[item.ID] = &cp
	return nil
}

func (m *mockStore) DeleteItem(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, id)
	return nil
}

func (m *mockStore) IsEmpty(_ context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items) == 0, nil
}

func (m *mockStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

func (m *mockStore) allItems() []*state.Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*state.Item
	for _, item := range m.items {
		cp := *item
		result = append(result, &cp)
	}
	return result
}
