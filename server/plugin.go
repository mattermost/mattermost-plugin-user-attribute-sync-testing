package main

import (
	"sync"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/pkg/errors"

	attrsync "github.com/mattermost/mattermost-plugin-user-attribute-sync-testing/server/sync"
)

// syncLockKey names the cluster mutex behind Plugin.syncLock.
const syncLockKey = "sync"

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// client is the Mattermost server API client.
	client *pluginapi.Client

	// router is the HTTP router that serves API endpoints.
	router *mux.Router

	// groupID is the ID of the Mattermost property group this plugin reads and writes.
	// We use the "access_control" group because user attribute fields defined here can be
	// referenced from attribute-based access control (ABAC) policy rules — e.g. a channel
	// policy that only admits users whose "Programs" includes "Apples".
	groupID string

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// syncLock serializes activation, upload, and delete across every node in the
	// cluster, so two triggers cannot interleave their writes.
	syncLock *cluster.Mutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration
}

// OnActivate is invoked when the plugin is activated. If an error is returned, the plugin will be deactivated.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	syncLock, err := cluster.NewMutex(p.API, syncLockKey)
	if err != nil {
		return errors.Wrap(err, "failed to create sync lock")
	}
	p.syncLock = syncLock

	// Register the HTTP routes that back the System Console upload UI. Do this before anything
	// that can fail, so the endpoints exist for any request the server routes to us.
	p.initializeAPI()

	// "access_control" is the property group whose fields can be referenced
	// from attribute-based access control (ABAC) policy rules. We register
	// our user attributes here so policies can evaluate against them (e.g.
	// "only admit users whose Programs includes Apples"). Mattermost core
	// registers the group automatically on server startup; we just look it
	// up here to get the group ID we'll write fields and values against.
	group, err := p.client.Property.GetPropertyGroup(model.AccessControlPropertyGroupName)
	if err != nil {
		return errors.Wrap(err, "failed to get access_control property group")
	}
	p.groupID = group.ID

	summary, err := p.runSync()
	switch {
	case errors.Is(err, attrsync.ErrNoStoredDocument):
		p.client.Log.Info("No attributes document stored, skipping sync")
	case err != nil:
		// Activate anyway: failing here would take the HTTP routes down, and the
		// admin would have no way to upload a replacement.
		p.client.Log.Error("Failed to apply stored attributes document", "error", err.Error())
	default:
		p.client.Log.Info("Applied stored attributes document",
			"fields_created", summary.FieldsCreated,
			"fields_updated", summary.FieldsUpdated,
			"fields_deleted", summary.FieldsDeleted,
			"fields_skipped", summary.FieldsSkipped,
			"users_synced", summary.UsersSynced,
			"users_skipped", summary.UsersSkipped,
			"channels_synced", summary.ChannelsSynced,
			"channels_skipped", summary.ChannelsSkipped,
		)
	}

	return nil
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
