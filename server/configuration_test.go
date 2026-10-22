package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestOnConfigurationChange tests that the config loaded from the server is applied.
func TestOnConfigurationChange(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)

	// The server writes the plugin settings into the struct it is handed, so the
	// mock has to do the same rather than just return.
	api.On("LoadPluginConfiguration", mock.AnythingOfType("*main.configuration")).
		Run(func(args mock.Arguments) {
			*args.Get(0).(*configuration) = configuration{}
		}).Return(nil).Once()

	p := &Plugin{
		MattermostPlugin: plugin.MattermostPlugin{API: api},
		client:           pluginapi.NewClient(api, &plugintest.Driver{}),
	}

	require.NoError(t, p.OnConfigurationChange())
	assert.NotNil(t, p.getConfiguration())
}
