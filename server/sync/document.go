package sync

import (
	"encoding/json"
	"fmt"
)

// SupportedDocumentVersion is the only value of the document's `version` key that this plugin
// accepts. Bumping it is how a future breaking change to the document format is announced,
// the same way this version broke the previous bare-array format.
const SupportedDocumentVersion = 2

// FieldSchema is the document's `fields` object: one list of field definitions per object
// type the plugin syncs.
type FieldSchema struct {
	User     []FieldDefinition `json:"user"`
	Channel  []FieldDefinition `json:"channel"`
	Template []FieldDefinition `json:"template"`
}

// AttributesDocument is the uploaded file's shape: a version marker, field definitions, and
// records keyed by identity — users by `email`, channels by `team` plus `channel`.
type AttributesDocument struct {
	Version  int                      `json:"version"`
	Fields   FieldSchema              `json:"fields"`
	Users    []map[string]interface{} `json:"users"`
	Channels []map[string]interface{} `json:"channels"`
}

func ParseAttributesDocument(raw []byte) (AttributesDocument, error) {
	var doc AttributesDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return AttributesDocument{}, fmt.Errorf("not a valid attributes document: %w", err)
	}

	if doc.Version != SupportedDocumentVersion {
		return AttributesDocument{}, fmt.Errorf("unsupported document version %d: only version %d is supported", doc.Version, SupportedDocumentVersion)
	}

	return doc, nil
}
