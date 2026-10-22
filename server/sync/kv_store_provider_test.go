package sync

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestKVClient(t *testing.T) (*pluginapi.Client, *plugintest.API) {
	t.Helper()

	api := &plugintest.API{}
	t.Cleanup(func() { api.AssertExpectations(t) })

	return pluginapi.NewClient(api, &plugintest.Driver{}), api
}

// storedValue encodes a StoredAttributes the way the upload handler does, so a KVGet mock returns
// what the reader would really find.
func storedValue(t *testing.T, lastUpdated time.Time, data []byte) []byte {
	t.Helper()

	value, err := json.Marshal(StoredAttributes{LastUpdated: lastUpdated, Data: data})
	require.NoError(t, err)

	return value
}

func TestReadStoredAttributes_NothingStored(t *testing.T) {
	client, api := newTestKVClient(t)

	api.On("KVGet", AttributesStoreKey).Return(nil, nil).Once()

	stored, err := ReadStoredAttributes(client)
	require.NoError(t, err)
	assert.Empty(t, stored.Data)
}

func TestReadStoredAttributes_EmptyData(t *testing.T) {
	client, api := newTestKVClient(t)

	api.On("KVGet", AttributesStoreKey).Return(storedValue(t, time.Now(), nil), nil).Once()

	stored, err := ReadStoredAttributes(client)
	require.NoError(t, err)
	assert.Empty(t, stored.Data)
}

func TestReadStoredAttributes_ReturnsData(t *testing.T) {
	client, api := newTestKVClient(t)
	data := []byte(`{"version": 2, "users": [{"email": "user1@example.com"}]}`)

	api.On("KVGet", AttributesStoreKey).Return(storedValue(t, time.Now(), data), nil).Once()

	stored, err := ReadStoredAttributes(client)
	require.NoError(t, err)
	assert.Equal(t, data, stored.Data)
}

func TestReadStoredAttributes_FileStoreError(t *testing.T) {
	client, api := newTestKVClient(t)

	api.On("KVGet", AttributesStoreKey).
		Return(nil, model.NewAppError("KVGet", "kv.get.app_error", nil, "connection refused", http.StatusInternalServerError)).Once()

	stored, err := ReadStoredAttributes(client)
	assert.Error(t, err)
	assert.Empty(t, stored.Data)
	assert.Contains(t, err.Error(), "failed to read attributes from the KV store")
}

func TestReadStoredAttributes_MalformedStoredValue(t *testing.T) {
	client, api := newTestKVClient(t)

	api.On("KVGet", AttributesStoreKey).Return([]byte(`malformed`), nil).Once()

	stored, err := ReadStoredAttributes(client)
	assert.Error(t, err)
	assert.Empty(t, stored.Data)
	assert.Contains(t, err.Error(), "malformed data")
}
