package main

import (
	"fmt"

	attrsync "github.com/mattermost/mattermost-plugin-user-attribute-sync-testing/server/sync"
)

// runSync holds the lock across the stored-document read and the sync so two triggers
// cannot interleave their property-service writes.
func (p *Plugin) runSync() (attrsync.Summary, error) {
	p.syncLock.Lock()
	defer p.syncLock.Unlock()

	stored, err := attrsync.ReadStoredAttributes(p.client)
	if err != nil {
		return attrsync.Summary{}, fmt.Errorf("failed to read stored attributes: %w", err)
	}
	if len(stored.Data) == 0 {
		return attrsync.Summary{}, attrsync.ErrNoStoredDocument
	}

	doc, err := attrsync.ParseAttributesDocument(stored.Data)
	if err != nil {
		return attrsync.Summary{}, fmt.Errorf("failed to parse stored attributes document: %w", err)
	}

	return attrsync.SyncDocument(p.client, p.groupID, manifest.Id, doc)
}
