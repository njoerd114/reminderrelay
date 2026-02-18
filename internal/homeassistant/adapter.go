package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	haclient "github.com/mkelcik/go-ha-client/v2"

	"github.com/njoerd114/reminderrelay/internal/model"
)

// RESTClient is the subset of [haclient.Client] methods used by the adapter.
// Defining it as an interface allows mock injection in tests.
type RESTClient interface {
	Ping(ctx context.Context) error
	// CallService POSTs to /api/services/<domain>/<service> without
	// return_response. Used for mutations (add, update, remove).
	CallService(ctx context.Context, domain, service string, body io.Reader) error
	// CallServiceWithResponse POSTs with ?return_response=true. Used for
	// todo.get_items which returns data.
	CallServiceWithResponse(ctx context.Context, domain, service string, body io.Reader) (haclient.ServiceCallResponse, error)
}

// haClientWrapper wraps [haclient.Client] and adds a plain CallService method
// that POSTs without ?return_response — required for HA services that don't
// support responses (e.g. todo.add_item, todo.update_item, todo.remove_item).
type haClientWrapper struct {
	client  *haclient.Client
	baseURL string
	token   string
	hc      *http.Client
}

func (w *haClientWrapper) Ping(ctx context.Context) error {
	return w.client.Ping(ctx)
}

// CallService POSTs the body to /api/services/<domain>/<service> without
// appending ?return_response, so HA does not try to return data.
func (w *haClientWrapper) CallService(ctx context.Context, domain, service string, body io.Reader) error {
	endpoint := fmt.Sprintf("%s/api/services/%s/%s",
		strings.TrimRight(w.baseURL, "/"),
		url.PathEscape(domain),
		url.PathEscape(service),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("create service request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.hc.Do(req)
	if err != nil {
		return fmt.Errorf("execute service request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusBadRequest {
		var br struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&br)
		return errors.New(br.Message)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("HA returned 401 Unauthorized — check ha_token")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HA returned unexpected status %d", resp.StatusCode)
	}
	return nil
}

func (w *haClientWrapper) CallServiceWithResponse(ctx context.Context, domain, service string, body io.Reader) (haclient.ServiceCallResponse, error) {
	return w.client.CallServiceWithResponse(ctx, domain, service, body)
}

// Adapter provides sync-engine–oriented operations on Home Assistant todo
// lists via the REST and WebSocket APIs. Create one with [NewAdapter] or
// [NewAdapterWithClient].
type Adapter struct {
	rest   RESTClient
	ws     *haclient.WSClient
	logger *slog.Logger
}

// NewAdapter creates an Adapter backed by real HA REST and WebSocket clients.
// The WebSocket is configured with unlimited auto-reconnect.
func NewAdapter(haURL, token string, logger *slog.Logger) (*Adapter, error) {
	rest, err := haclient.NewClient(haURL,
		haclient.WithToken(token),
		haclient.WithLogger(logger),
	)
	if err != nil {
		return nil, fmt.Errorf("create HA REST client: %w", err)
	}

	wrapper := &haClientWrapper{
		client:  rest,
		baseURL: haURL,
		token:   token,
		hc:      &http.Client{},
	}

	ws := rest.WS(
		haclient.WithAutoReconnect(true),
		haclient.WithMaxRetries(0), // unlimited retries
		haclient.WithOnReconnect(func() {
			logger.Info("HA WebSocket reconnected")
		}),
		haclient.WithOnReconnectError(func(err error) {
			logger.Error("HA WebSocket reconnect failed", "error", err)
		}),
	)

	return &Adapter{rest: wrapper, ws: ws, logger: logger}, nil
}

// NewAdapterWithClient creates an Adapter with a caller-supplied REST client.
// Intended for testing with a mock [RESTClient]. WebSocket features
// (SubscribeChanges) are unavailable on adapters created this way.
func NewAdapterWithClient(rest RESTClient, logger *slog.Logger) *Adapter {
	return &Adapter{rest: rest, logger: logger}
}

// Ping validates the HA connection and token with retry.
func (a *Adapter) Ping(ctx context.Context) error {
	err := Retry(ctx, defaultMaxAttempts, func() error {
		return a.rest.Ping(ctx)
	})
	if err != nil {
		return fmt.Errorf("ping HA: %w", err)
	}
	return nil
}

// Connect establishes the WebSocket connection. Must be called before
// [Adapter.SubscribeChanges].
func (a *Adapter) Connect(ctx context.Context) error {
	if a.ws == nil {
		return fmt.Errorf("WebSocket client not configured")
	}
	return a.ws.Connect(ctx)
}

// Close shuts down the WebSocket connection gracefully.
func (a *Adapter) Close() error {
	if a.ws == nil {
		return nil
	}
	return a.ws.Close()
}

// GetItems fetches all todo items for the given HA entity.
func (a *Adapter) GetItems(ctx context.Context, entityID string) ([]model.Item, error) {
	data := buildGetItemsData(entityID)

	var resp haclient.ServiceCallResponse
	err := Retry(ctx, defaultMaxAttempts, func() error {
		var callErr error
		resp, callErr = a.rest.CallServiceWithResponse(ctx, domainTodo, serviceGetItems, serviceBody(data))
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("get items for %s: %w", entityID, err)
	}

	return parseGetItemsResponse(resp, entityID)
}

// AddItem creates a new todo item in the given HA entity. The item's Priority
// is encoded as a description prefix automatically.
func (a *Adapter) AddItem(ctx context.Context, entityID string, item *model.Item) error {
	data := buildAddItemData(entityID, item)
	err := Retry(ctx, defaultMaxAttempts, func() error {
		return a.rest.CallService(ctx, domainTodo, serviceAddItem, serviceBody(data))
	})
	if err != nil {
		return fmt.Errorf("add item %q to %s: %w", item.Title, entityID, err)
	}
	return nil
}

// UpdateItem updates an existing todo item in HA. currentTitle is the item's
// title as it currently exists in HA, used to identify the target item.
func (a *Adapter) UpdateItem(ctx context.Context, entityID, currentTitle string, item *model.Item) error {
	data := buildUpdateItemData(entityID, currentTitle, item)
	err := Retry(ctx, defaultMaxAttempts, func() error {
		return a.rest.CallService(ctx, domainTodo, serviceUpdateItem, serviceBody(data))
	})
	if err != nil {
		return fmt.Errorf("update item %q in %s: %w", currentTitle, entityID, err)
	}
	return nil
}

// RemoveItem deletes a todo item from HA by its current title.
func (a *Adapter) RemoveItem(ctx context.Context, entityID, title string) error {
	data := buildRemoveItemData(entityID, title)
	err := Retry(ctx, defaultMaxAttempts, func() error {
		return a.rest.CallService(ctx, domainTodo, serviceRemoveItem, serviceBody(data))
	})
	if err != nil {
		return fmt.Errorf("remove item %q from %s: %w", title, entityID, err)
	}
	return nil
}

// SubscribeChanges starts a WebSocket subscription for state_changed events
// on the given todo entities. When any tracked entity changes, callback is
// invoked with the entity ID. This method blocks until ctx is cancelled.
func (a *Adapter) SubscribeChanges(ctx context.Context, entityIDs []string, callback func(entityID string)) error {
	if a.ws == nil {
		return fmt.Errorf("WebSocket client not configured")
	}

	// Build a set for O(1) lookup.
	entitySet := make(map[string]struct{}, len(entityIDs))
	for _, id := range entityIDs {
		entitySet[id] = struct{}{}
	}

	sub, err := a.ws.SubscribeEvents(ctx, haclient.EventTypeStateChanged)
	if err != nil {
		return fmt.Errorf("subscribe state_changed: %w", err)
	}
	defer func() { _ = sub.Unsubscribe(ctx) }()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-sub.Events():
			if !ok {
				return fmt.Errorf("subscription events channel closed")
			}
			data, isStateChanged, parseErr := ev.StateChanged()
			if parseErr != nil {
				a.logger.Debug("failed to parse state_changed event", "error", parseErr)
				continue
			}
			if !isStateChanged {
				continue
			}
			if _, tracked := entitySet[data.EntityID]; tracked {
				a.logger.Debug("tracked entity changed", "entity_id", data.EntityID)
				callback(data.EntityID)
			}
		case subErr, ok := <-sub.Errors():
			if !ok {
				return fmt.Errorf("subscription errors channel closed")
			}
			a.logger.Error("subscription error", "error", subErr)
			// Auto-reconnect restores the subscription; just log.
		}
	}
}

// serviceBody marshals data to a JSON [io.Reader] for service calls.
func serviceBody(data map[string]interface{}) io.Reader {
	b, _ := json.Marshal(data) //nolint:errcheck // map[string]interface{} always marshals
	return bytes.NewReader(b)
}

// parseGetItemsResponse extracts todo items from the service call response.
func parseGetItemsResponse(resp haclient.ServiceCallResponse, entityID string) ([]model.Item, error) {
	raw, ok := resp.ServiceResponse[entityID]
	if !ok {
		return nil, fmt.Errorf("no service response for entity %s", entityID)
	}

	var haResp haItemsResponse
	if err := json.Unmarshal(raw, &haResp); err != nil {
		return nil, fmt.Errorf("parse items response for %s: %w", entityID, err)
	}

	items := make([]model.Item, 0, len(haResp.Items))
	for _, h := range haResp.Items {
		items = append(items, haItemToModelItem(h))
	}
	return items, nil
}
