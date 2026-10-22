package main

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-user-attribute-sync-testing/server/sync"
)

func TestRunSync(t *testing.T) {
	newPlugin := func(t *testing.T) (*Plugin, *plugintest.API) {
		t.Helper()

		api := &plugintest.API{}
		t.Cleanup(func() { api.AssertExpectations(t) })
		mockLogs(api)

		client := pluginapi.NewClient(api, &plugintest.Driver{})

		return &Plugin{
			client:   client,
			groupID:  "group-id",
			syncLock: newTestSyncLock(t, api),
		}, api
	}

	t.Run("no stored document", func(t *testing.T) {
		p, api := newPlugin(t)
		api.On("KVGet", sync.AttributesStoreKey).Return(nil, nil).Once()

		_, err := p.runSync()

		require.ErrorIs(t, err, sync.ErrNoStoredDocument)
	})

	t.Run("takes and releases the cluster sync lock", func(t *testing.T) {
		p, api := newPlugin(t)
		api.On("KVGet", sync.AttributesStoreKey).Return(nil, nil).Once()

		_, _ = p.runSync()

		lockKey := "mutex_" + syncLockKey
		api.AssertCalled(t, "KVSetWithOptions", lockKey, []byte{1}, mock.MatchedBy(func(o model.PluginKVSetOptions) bool { return o.Atomic }))
		api.AssertCalled(t, "KVSetWithOptions", lockKey, []byte(nil), model.PluginKVSetOptions{})
	})

	t.Run("a stored document", func(t *testing.T) {
		p, api := newPlugin(t)
		api.On("KVGet", sync.AttributesStoreKey).
			Return(storedValue(t, time.Now(), []byte(`{"version": 2, "users": [{"email":"nobody@example.com"}]}`)), nil).Once()
		api.On("SearchPropertyFields", "group-id", mock.Anything).Return([]*model.PropertyField{}, nil)

		notFoundErr := model.NewAppError("GetUserByEmail", "app.user.get_by_email.app_error", nil, "", 404)
		api.On("GetUserByEmail", "nobody@example.com").Return(nil, notFoundErr).Once()

		summary, err := p.runSync()

		require.NoError(t, err)
		assert.Equal(t, 1, summary.UsersSkipped)
	})
}
