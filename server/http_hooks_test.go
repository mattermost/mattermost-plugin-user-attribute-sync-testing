package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-user-attribute-sync-testing/server/sync"
)

// newTestPlugin returns a Plugin backed by a mocked server API, with its HTTP
// router initialized so requests can be driven through ServeHTTP exactly the
// way the Mattermost server drives them.
func newTestPlugin(t *testing.T) (*Plugin, *plugintest.API) {
	t.Helper()

	api := &plugintest.API{}
	t.Cleanup(func() { api.AssertExpectations(t) })
	mockLogs(api)

	client := pluginapi.NewClient(api, &plugintest.Driver{})

	p := &Plugin{
		MattermostPlugin: plugin.MattermostPlugin{API: api},
		client:           client,
		groupID:          "group-id",
		syncLock:         newTestSyncLock(t, api),
	}
	p.initializeAPI()

	return p, api
}

// newTestSyncLock returns the cluster mutex behind syncLock, with its KV writes
// mocked to always succeed: a test owns the whole cluster, so the lock is never
// contended.
func newTestSyncLock(t *testing.T, api *plugintest.API) *cluster.Mutex {
	t.Helper()

	api.On("KVSetWithOptions", "mutex_"+syncLockKey, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	syncLock, err := cluster.NewMutex(api, syncLockKey)
	require.NoError(t, err)
	return syncLock
}

// mockLogs registers permissive expectations for the log methods so tests that
// exercise error paths don't have to match every log line exactly.
func mockLogs(api *plugintest.API) {
	const maxLogFields = 16
	for _, method := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
		for n := 0; n <= maxLogFields; n++ {
			args := make([]interface{}, n+1)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(method, args...).Maybe()
		}
	}
}

// asSysadmin lets userID through the requireSysadmin middleware.
func asSysadmin(api *plugintest.API, userID string) {
	api.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(true).Once()
}

// doRequest drives a request through the plugin's real router. An empty userID
// simulates an unauthenticated request: the Mattermost server only sets the
// Mattermost-User-Id header once it has verified the session.
func doRequest(t *testing.T, p *Plugin, method, path, userID string, body []byte) *http.Response {
	t.Helper()

	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if userID != "" {
		r.Header.Set("Mattermost-User-Id", userID)
	}

	w := httptest.NewRecorder()
	p.ServeHTTP(&plugin.Context{}, w, r)

	return w.Result()
}

// storedValue encodes a StoredAttributes the way the upload handler does, so a KVGet mock returns
// what a handler would really find.
func storedValue(t *testing.T, lastUpdated time.Time, data []byte) []byte {
	t.Helper()

	value, err := json.Marshal(sync.StoredAttributes{LastUpdated: lastUpdated, Data: data})
	require.NoError(t, err)

	return value
}

// storesFile matches the value the upload handler writes while having a looser check on the
// LastUpdated field, since that is set by time.Now()
func storesFile(data []byte) interface{} {
	return mock.MatchedBy(func(value []byte) bool {
		var stored sync.StoredAttributes
		if err := json.Unmarshal(value, &stored); err != nil {
			return false
		}

		return bytes.Equal(stored.Data, data) && !stored.LastUpdated.IsZero()
	})
}

// requireErrorResponse asserts the handler failed with the given status and
// message, which errorWithJSON renders as {"error": "..."}.
func requireErrorResponse(t *testing.T, resp *http.Response, statusCode int, message string) {
	t.Helper()

	require.Equal(t, statusCode, resp.StatusCode)

	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, message, body.Error)
}

// TestUserAttributesAccessControl covers the two rejection paths the requireSysadmin middleware
// applies to every endpoint, so the individual handler tests below can assume an authorized admin.
// Driving each method and path proves the middleware is actually reached by all of them, which is
// the property that replaced a per-handler check.
func TestUserAttributesAccessControl(t *testing.T) {
	endpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"upload", http.MethodPost, "/attributes"},
		{"download", http.MethodGet, "/attributes"},
		{"status", http.MethodGet, "/attributes/status"},
		{"delete", http.MethodDelete, "/attributes"},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name+"/not logged in", func(t *testing.T) {
			p, _ := newTestPlugin(t)

			resp := doRequest(t, p, endpoint.method, endpoint.path, "", nil)
			requireErrorResponse(t, resp, http.StatusUnauthorized, "not logged in")
		})

		t.Run(endpoint.name+"/not a sysadmin", func(t *testing.T) {
			p, api := newTestPlugin(t)

			userID := model.NewId()
			api.On("HasPermissionTo", userID, model.PermissionManageSystem).Return(false).Once()

			resp := doRequest(t, p, endpoint.method, endpoint.path, userID, nil)
			requireErrorResponse(t, resp, http.StatusForbidden, "not authorized")
		})
	}
}

// TestHandleUploadUserAttributes covers the upload path: what is stored, and the rejection
// reasons a request can fail for (truncated JSON, the old bare-array format, null, too large, and
// a KV write that did not take).
func TestHandleUploadUserAttributes(t *testing.T) {
	validFile := []byte(`{"version": 2, "fields": {"user": [{"name": "job_title", "display_name": "Job Title", "type": "text"}]}, "users": [{"email":"user1@example.com","job_title":"Engineer"}]}`)

	t.Run("stores the uploaded document verbatim", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVSetWithOptions", sync.AttributesStoreKey, storesFile(validFile), model.PluginKVSetOptions{}).
			Return(true, nil).Once()
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "job_title"
		})).Return(&model.PropertyField{ID: "field-1", Name: "job_title", Type: model.PropertyFieldTypeText}, nil)
		api.On("SearchPropertyFields", "group-id", mock.Anything).Return([]*model.PropertyField{}, nil)
		api.On("GetUserByEmail", "user1@example.com").Return(&model.User{Id: "user1", Email: "user1@example.com"}, nil)
		api.On("UpsertPropertyValues", mock.Anything).Return([]*model.PropertyValue{}, nil)

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID, validFile)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		var body sync.Summary
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Equal(t, 1, body.FieldsCreated)
		require.Equal(t, 0, body.FieldsUpdated)
		require.Equal(t, 0, body.FieldsDeleted)
		require.Equal(t, 0, body.FieldsSkipped)
		require.Equal(t, 1, body.UsersSynced)
		require.Equal(t, 0, body.UsersSkipped)
		require.Equal(t, 0, body.ChannelsSynced)
		require.Equal(t, 0, body.ChannelsSkipped)
	})

	t.Run("stores the document when field lookup fails", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVSetWithOptions", sync.AttributesStoreKey, storesFile(validFile), model.PluginKVSetOptions{}).
			Return(true, nil).Once()
		api.On("SearchPropertyFields", "group-id", mock.Anything).
			Return(nil, model.NewAppError("SearchPropertyFields", "app.error", nil, "connection refused", http.StatusInternalServerError))
		api.On("GetUserByEmail", "user1@example.com").Return(&model.User{Id: "user1", Email: "user1@example.com"}, nil)

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID, validFile)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		var body sync.Summary
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Equal(t, 1, body.FieldsSkipped)
	})

	t.Run("rejects truncated json", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID, []byte(`{"version": 2, "users":`))
		requireErrorResponse(t, resp, http.StatusBadRequest,
			"invalid attributes document: not a valid attributes document: unexpected end of JSON input")
	})

	t.Run("rejects the old bare-array format", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID,
			[]byte(`[{"email":"user1@example.com","job_title":"Engineer"}]`))
		requireErrorResponse(t, resp, http.StatusBadRequest,
			"invalid attributes document: not a valid attributes document: json: cannot unmarshal array into Go value of type sync.AttributesDocument")
	})

	t.Run("rejects a file over the size limit", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)

		// One byte past the limit is enough to trip MaxBytesReader, and the body
		// never has to be valid JSON because it is rejected before parsing.
		oversized := bytes.Repeat([]byte("a"), maxFileSizeBytes+1)

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID, oversized)
		requireErrorResponse(t, resp, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("file exceeds %d byte limit", maxFileSizeBytes))
	})

	t.Run("rejects null json files", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID, []byte(`null`))
		requireErrorResponse(t, resp, http.StatusBadRequest,
			"invalid attributes document: unsupported document version 0: only version 2 is supported")
	})

	t.Run("reports a failed write", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVSetWithOptions", sync.AttributesStoreKey, storesFile(validFile), model.PluginKVSetOptions{}).
			Return(false, model.NewAppError("KVSetWithOptions", "kv.set.app_error", nil, "connection refused", http.StatusInternalServerError)).Once()

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID, validFile)
		requireErrorResponse(t, resp, http.StatusInternalServerError, "failed to upload file")
	})

	t.Run("reports a write that did not happen", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVSetWithOptions", sync.AttributesStoreKey, storesFile(validFile), model.PluginKVSetOptions{}).
			Return(false, nil).Once()

		resp := doRequest(t, p, http.MethodPost, "/attributes", userID, validFile)
		requireErrorResponse(t, resp, http.StatusInternalServerError, "failed to upload file, please try again")
	})
}

// TestHandleDownloadUserAttributes checks that a download returns the stored bytes unchanged,
// and that "nothing stored" is a 404 rather than an empty 200.
func TestHandleDownloadUserAttributes(t *testing.T) {
	t.Run("returns the stored file", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		file := []byte(`{"version": 2, "users": [{"email":"user1@example.com","job_title":"Engineer"}]}`)
		asSysadmin(api, userID)
		api.On("KVGet", sync.AttributesStoreKey).Return(storedValue(t, time.Now(), file), nil).Once()

		resp := doRequest(t, p, http.MethodGet, "/attributes", userID, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := readAll(resp)
		require.NoError(t, err)
		require.Equal(t, file, body)
	})

	t.Run("reports no stored file", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVGet", sync.AttributesStoreKey).Return(nil, nil).Once()

		resp := doRequest(t, p, http.MethodGet, "/attributes", userID, nil)
		requireErrorResponse(t, resp, http.StatusNotFound, "file not found")
	})

	t.Run("reports a failed read", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVGet", sync.AttributesStoreKey).
			Return(nil, model.NewAppError("KVGet", "kv.get.app_error", nil, "connection refused", http.StatusInternalServerError)).Once()

		resp := doRequest(t, p, http.MethodGet, "/attributes", userID, nil)
		requireErrorResponse(t, resp, http.StatusInternalServerError, "failed to download file")
	})
}

// TestHandleUserAttributesStatus covers the endpoint the settings UI polls on load.
func TestHandleUserAttributesStatus(t *testing.T) {
	uploadedAt, err := time.Parse(time.RFC3339, "2026-08-20T15:04:05Z")
	require.NoError(t, err)

	t.Run("reports a stored file with upload time", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVGet", sync.AttributesStoreKey).
			Return(storedValue(t, uploadedAt, []byte(`{"version": 2, "users": [{"email":"user1@example.com"}]}`)), nil).Once()

		resp := doRequest(t, p, http.MethodGet, "/attributes/status", userID, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body attributesStatus
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.True(t, body.Exists)
		require.NotNil(t, body.LastUpdated)
		require.True(t, uploadedAt.Equal(*body.LastUpdated))
	})

	t.Run("reports no stored file", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVGet", sync.AttributesStoreKey).Return(nil, nil).Once()

		resp := doRequest(t, p, http.MethodGet, "/attributes/status", userID, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body attributesStatus
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.False(t, body.Exists)
		require.Nil(t, body.LastUpdated)
	})

	t.Run("reports a failed read", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVGet", sync.AttributesStoreKey).
			Return(nil, model.NewAppError("KVGet", "kv.get.app_error", nil, "connection refused", http.StatusInternalServerError)).Once()

		resp := doRequest(t, p, http.MethodGet, "/attributes/status", userID, nil)
		requireErrorResponse(t, resp, http.StatusInternalServerError, "failed to access storage")
	})

	// A value that will not decode says nothing about whether a file is there, so it fails rather
	// than reporting nothing stored.
	t.Run("reports a malformed stored value", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVGet", sync.AttributesStoreKey).Return([]byte("not json"), nil).Once()

		resp := doRequest(t, p, http.MethodGet, "/attributes/status", userID, nil)
		requireErrorResponse(t, resp, http.StatusInternalServerError, "failed to access storage")
	})
}

func TestHandleDeleteUserAttributes(t *testing.T) {
	t.Run("deletes the stored file", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		// KV.Delete is a Set of a nil value under the hood.
		api.On("KVSetWithOptions", sync.AttributesStoreKey, []byte(nil), model.PluginKVSetOptions{}).
			Return(true, nil).Once()
		owned := &model.PropertyField{
			ID:       model.NewId(),
			Name:     "old_title",
			CreateAt: 1,
			Attrs:    model.StringInterface{model.PropertyAttrsSourcePluginID: manifest.Id},
		}
		api.On("SearchPropertyFields", "group-id", mock.Anything).Return([]*model.PropertyField{owned}, nil).Once()
		mock.InOrder(
			api.On("DeletePropertyValuesForField", "group-id", owned.ID).Return(nil).Once(),
			api.On("DeletePropertyField", "group-id", owned.ID).Return(nil).Once(),
		)

		resp := doRequest(t, p, http.MethodDelete, "/attributes", userID, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body sync.Summary
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Equal(t, 0, body.FieldsCreated)
		require.Equal(t, 0, body.FieldsUpdated)
		require.Equal(t, 1, body.FieldsDeleted)
		require.Equal(t, 0, body.FieldsSkipped)
		require.Equal(t, 0, body.UsersSynced)
		require.Equal(t, 0, body.UsersSkipped)
		require.Equal(t, 0, body.ChannelsSynced)
		require.Equal(t, 0, body.ChannelsSkipped)
	})

	t.Run("reports a failed field removal", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVSetWithOptions", sync.AttributesStoreKey, []byte(nil), model.PluginKVSetOptions{}).
			Return(true, nil).Once()
		api.On("SearchPropertyFields", "group-id", mock.Anything).
			Return(nil, model.NewAppError("SearchPropertyFields", "app.error", nil, "connection refused", http.StatusInternalServerError)).Once()

		resp := doRequest(t, p, http.MethodDelete, "/attributes", userID, nil)
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		var body struct {
			Error string `json:"error"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Contains(t, body.Error, "document deleted but attribute removal failed:")
		api.AssertNotCalled(t, "DeletePropertyField", mock.Anything, mock.Anything)
	})

	t.Run("reports a failed delete", func(t *testing.T) {
		p, api := newTestPlugin(t)

		userID := model.NewId()
		asSysadmin(api, userID)
		api.On("KVSetWithOptions", sync.AttributesStoreKey, []byte(nil), model.PluginKVSetOptions{}).
			Return(false, model.NewAppError("KVSetWithOptions", "kv.set.app_error", nil, "connection refused", http.StatusInternalServerError)).Once()
		api.On("SearchPropertyFields", mock.Anything, mock.Anything).Maybe()
		api.On("DeletePropertyValuesForField", mock.Anything, mock.Anything).Maybe()
		api.On("DeletePropertyField", mock.Anything, mock.Anything).Maybe()

		resp := doRequest(t, p, http.MethodDelete, "/attributes", userID, nil)
		requireErrorResponse(t, resp, http.StatusInternalServerError, "failed to delete file")
		api.AssertNotCalled(t, "SearchPropertyFields", mock.Anything, mock.Anything)
		api.AssertNotCalled(t, "DeletePropertyValuesForField", mock.Anything, mock.Anything)
		api.AssertNotCalled(t, "DeletePropertyField", mock.Anything, mock.Anything)
	})
}

// readAll drains a response body and closes it.
func readAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
