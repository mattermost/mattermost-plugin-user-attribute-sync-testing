package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-user-attribute-sync-testing/server/sync"
)

func TestOnActivate(t *testing.T) {
	newPlugin := func(t *testing.T) (*Plugin, *plugintest.API) {
		t.Helper()

		api := &plugintest.API{}
		t.Cleanup(func() { api.AssertExpectations(t) })

		p := &Plugin{
			MattermostPlugin: plugin.MattermostPlugin{
				API:    api,
				Driver: &plugintest.Driver{},
			},
		}
		api.On("KVSetWithOptions", "mutex_"+syncLockKey, mock.Anything, mock.Anything).Return(true, nil).Maybe()

		return p, api
	}

	mockPropertyGroup := func(api *plugintest.API) {
		api.On("GetPropertyGroup", model.AccessControlPropertyGroupName).
			Return(&model.PropertyGroup{ID: "group-id"}, nil).Once()
	}

	forbidFieldSync := func(t *testing.T, api *plugintest.API) {
		t.Helper()
		fail := func(mock.Arguments) { t.Fatal("SyncFields must not run") }
		api.On("LogInfo", "Syncing field definitions", mock.Anything, mock.Anything).Run(fail).Maybe()
		api.On("SearchPropertyFields", mock.Anything, mock.Anything).Run(fail).Maybe()
	}

	t.Run("KV read error logs and still activates", func(t *testing.T) {
		p, api := newPlugin(t)
		mockPropertyGroup(api)
		api.On("KVGet", sync.AttributesStoreKey).
			Return(nil, model.NewAppError("KVGet", "kv.get.app_error", nil, "connection refused", http.StatusInternalServerError)).Once()
		api.On("LogError", "Failed to apply stored attributes document", "error", mock.AnythingOfType("string")).Once()
		forbidFieldSync(t, api)
		mockLogs(api)

		require.NoError(t, p.OnActivate())
	})

	t.Run("no stored document activates and touches no property field", func(t *testing.T) {
		p, api := newPlugin(t)
		mockPropertyGroup(api)
		api.On("KVGet", sync.AttributesStoreKey).Return(nil, nil)
		api.On("LogInfo", "No attributes document stored, skipping sync").Once()
		forbidFieldSync(t, api)
		mockLogs(api)

		require.NoError(t, p.OnActivate())
	})

	t.Run("unparseable document logs and still activates", func(t *testing.T) {
		p, api := newPlugin(t)
		mockPropertyGroup(api)
		api.On("KVGet", sync.AttributesStoreKey).
			Return(storedValue(t, time.Now(), []byte("not json")), nil)
		api.On("LogError", "Failed to apply stored attributes document", "error", mock.AnythingOfType("string")).Once()
		forbidFieldSync(t, api)
		mockLogs(api)

		require.NoError(t, p.OnActivate())
	})

	t.Run("stored document is applied", func(t *testing.T) {
		p, api := newPlugin(t)
		mockPropertyGroup(api)
		api.On("KVGet", sync.AttributesStoreKey).
			Return(storedValue(t, time.Now(), []byte(`{"version": 2, "fields": {"user": [{"name": "job_title", "display_name": "Job Title", "type": "text"}]}, "users": [{"email":"user1@example.com", "job_title": "Engineer"}]}`)), nil).Once()
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "job_title"
		})).Return(&model.PropertyField{ID: "field-1", Name: "job_title", Type: model.PropertyFieldTypeText}, nil)
		api.On("SearchPropertyFields", "group-id", mock.Anything).Return([]*model.PropertyField{}, nil)
		api.On("GetUserByEmail", "user1@example.com").Return(&model.User{Id: "user1", Email: "user1@example.com"}, nil)
		api.On("UpsertPropertyValues", mock.Anything).Return([]*model.PropertyValue{}, nil)
		mockLogs(api)

		require.NoError(t, p.OnActivate())
	})
}
