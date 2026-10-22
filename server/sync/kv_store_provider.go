package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// AttributesStoreKey holds what the HTTP handlers in server/http_hooks.go uploaded: the file, and
// the timestamp the status endpoint reports.
const AttributesStoreKey = "attributes"

// ErrNoStoredDocument is returned when nothing has been uploaded, or the last document was deleted.
// Callers match it with errors.Is; it is not an empty document.
var ErrNoStoredDocument = errors.New("no attributes document stored")

// StoredAttributes is the value under AttributesStoreKey.
//
// Data is the file exactly as it was uploaded, so a download returns what the admin gave us.
type StoredAttributes struct {
	LastUpdated time.Time `json:"lastUpdated"`
	Data        []byte    `json:"data"`
}

// ReadStoredAttributes reads the stored file and its timestamp.
func ReadStoredAttributes(client *pluginapi.Client) (StoredAttributes, error) {
	// Unmarshal here rather than letting KV.Get do it, so an unreachable store and a corrupt value
	// are not reported as the same error.
	var raw []byte
	if err := client.KV.Get(AttributesStoreKey, &raw); err != nil {
		return StoredAttributes{}, fmt.Errorf("failed to read %s from the KV store: %w", AttributesStoreKey, err)
	}

	if len(raw) == 0 {
		return StoredAttributes{}, nil
	}

	var stored StoredAttributes
	if err := json.Unmarshal(raw, &stored); err != nil {
		return StoredAttributes{}, fmt.Errorf("malformed data in %s: %w", AttributesStoreKey, err)
	}

	return stored, nil
}
