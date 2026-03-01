package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	ekreminders "github.com/BRO3886/go-eventkit/reminders"
)

// HAEntity represents a discovered Home Assistant todo entity.
type HAEntity struct {
	EntityID     string
	FriendlyName string
}

// String returns a human-readable representation for selection prompts.
func (e HAEntity) String() string {
	if e.FriendlyName != "" {
		return fmt.Sprintf("%s (%s)", e.FriendlyName, e.EntityID)
	}
	return e.EntityID
}

// RemindersList represents a discovered Apple Reminders list.
type RemindersList struct {
	Title string
	Count int
}

// PingHA verifies connectivity with the Home Assistant instance using the
// given URL and token. Returns nil on success.
func PingHA(ctx context.Context, haURL, haToken string) error {
	endpoint := strings.TrimRight(haURL, "/") + "/api/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+haToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", haURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid access token (HTTP 401)")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP %d from %s", resp.StatusCode, haURL)
	}
	return nil
}

// haStateEntry is the minimal JSON shape of /api/states entries.
type haStateEntry struct {
	EntityID   string `json:"entity_id"`
	Attributes struct {
		FriendlyName string `json:"friendly_name"`
	} `json:"attributes"`
}

// DiscoverHATodoEntities fetches all entities from Home Assistant and returns
// those in the "todo" domain, sorted alphabetically by entity ID.
func DiscoverHATodoEntities(ctx context.Context, haURL, haToken string) ([]HAEntity, error) {
	endpoint := strings.TrimRight(haURL, "/") + "/api/states"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+haToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching HA states: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HA returned HTTP %d", resp.StatusCode)
	}

	var states []haStateEntry
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		return nil, fmt.Errorf("parsing HA states response: %w", err)
	}

	var entities []HAEntity
	for _, s := range states {
		if strings.HasPrefix(s.EntityID, "todo.") {
			entities = append(entities, HAEntity{
				EntityID:     s.EntityID,
				FriendlyName: s.Attributes.FriendlyName,
			})
		}
	}

	sort.Slice(entities, func(i, j int) bool {
		return entities[i].EntityID < entities[j].EntityID
	})
	return entities, nil
}

// DiscoverRemindersLists returns all Apple Reminders lists available on this
// Mac. This triggers the macOS TCC permissions prompt on first use.
func DiscoverRemindersLists(logger *slog.Logger) ([]RemindersList, error) {
	client, err := ekreminders.New()
	if err != nil {
		return nil, fmt.Errorf("initialising Reminders client: %w", err)
	}

	lists, err := client.Lists()
	if err != nil {
		return nil, fmt.Errorf("fetching Reminders lists: %w", err)
	}

	logger.Debug("discovered Reminders lists", "count", len(lists))

	var result []RemindersList
	for _, l := range lists {
		result = append(result, RemindersList{
			Title: l.Title,
			Count: l.Count,
		})
	}
	return result, nil
}
