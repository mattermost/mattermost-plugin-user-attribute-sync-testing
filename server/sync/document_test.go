package sync

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAttributesDocument_ValidDocument(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "users": [
		{"email": "user1@example.com", "job_title": "Engineer"},
		{"email": "user2@example.com", "job_title": "Sales"}
	]}`))

	require.NoError(t, err)
	assert.Len(t, doc.Users, 2)
	assert.Equal(t, "user1@example.com", doc.Users[0]["email"])
	assert.Equal(t, "Engineer", doc.Users[0]["job_title"])
	assert.Equal(t, "user2@example.com", doc.Users[1]["email"])
	assert.Equal(t, "Sales", doc.Users[1]["job_title"])
	assert.Empty(t, doc.Fields.User)
}

// TestParseAttributesDocument_BareArrayRejected tests that a bare JSON array of user records is
// rejected; only the versioned document format is accepted.
func TestParseAttributesDocument_BareArrayRejected(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`[{"email": "user1@example.com"}]`))

	assert.Error(t, err)
	assert.Empty(t, doc.Users)
	assert.Contains(t, err.Error(), "not a valid attributes document")
}

// TestParseAttributesDocument_UnsupportedVersionRejected tests that a document naming a version
// this plugin does not support is rejected, and the error says which version is supported.
func TestParseAttributesDocument_UnsupportedVersionRejected(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 1, "users": []}`))

	assert.Error(t, err)
	assert.Empty(t, doc.Users)
	assert.Contains(t, err.Error(), "unsupported document version 1")
	assert.Contains(t, err.Error(), "only version 2 is supported")
}

// TestParseAttributesDocument_MissingVersionRejected tests that a document with no version key
// is rejected the same way an unsupported one is.
func TestParseAttributesDocument_MissingVersionRejected(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"users": []}`))

	assert.Error(t, err)
	assert.Empty(t, doc.Users)
	assert.Contains(t, err.Error(), "unsupported document version 0")
}

// TestParseAttributesDocument_UsersAbsent tests that a document with no users list is valid and
// parses with no records, since a document can carry only field definitions.
func TestParseAttributesDocument_UsersAbsent(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2}`))

	require.NoError(t, err)
	assert.Empty(t, doc.Users)
	assert.Empty(t, doc.Fields.User)
}

func TestParseAttributesDocument_NonObjectUserRejected(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "users": ["not an object"]}`))

	assert.Error(t, err)
	assert.Empty(t, doc.Users)
	assert.Contains(t, err.Error(), "not a valid attributes document")
}

func TestParseAttributesDocument_NotJSONRejected(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`not json`))

	assert.Error(t, err)
	assert.Empty(t, doc.Users)
	assert.Contains(t, err.Error(), "not a valid attributes document")
}

func TestParseAttributesDocument_FieldsUser(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "fields": {"user": [
		{"name": "job_title", "display_name": "Job Title", "type": "text"},
		{"name": "start_date", "display_name": "Start Date", "type": "date", "access_mode": "source_only"}
	]}, "users": [{"email": "user1@example.com"}]}`))

	require.NoError(t, err)
	require.Len(t, doc.Fields.User, 2)
	assert.Equal(t, "job_title", doc.Fields.User[0].Name)
	assert.Equal(t, "Job Title", doc.Fields.User[0].DisplayName)
	assert.Equal(t, model.PropertyFieldTypeText, doc.Fields.User[0].Type)
	assert.Equal(t, "start_date", doc.Fields.User[1].Name)
	assert.Equal(t, "source_only", doc.Fields.User[1].AccessMode)
	assert.Len(t, doc.Users, 1)
}

func TestParseAttributesDocument_FieldsChannelAndChannels(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "fields": {"channel": [
		{"name": "sensitivity", "display_name": "Sensitivity", "type": "text"}
	]}, "channels": [
		{"team": "ad-1", "channel": "town-square", "classification": ["Alpha-1"]}
	]}`))

	require.NoError(t, err)
	require.Len(t, doc.Fields.Channel, 1)
	assert.Equal(t, "sensitivity", doc.Fields.Channel[0].Name)
	assert.Equal(t, model.PropertyFieldTypeText, doc.Fields.Channel[0].Type)
	require.Len(t, doc.Channels, 1)
	assert.Equal(t, "ad-1", doc.Channels[0]["team"])
	assert.Equal(t, "town-square", doc.Channels[0]["channel"])
	assert.Equal(t, []interface{}{"Alpha-1"}, doc.Channels[0]["classification"])
	assert.Empty(t, doc.Fields.User)
	assert.Empty(t, doc.Users)
}

func TestParseAttributesDocument_FieldsTemplate(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "fields": {"template": [
		{"name": "clearance", "display_name": "Clearance", "type": "multiselect", "options": [{"name": "CUI"}, {"name": "Secret"}]},
		{"name": "department", "display_name": "Department", "type": "text"}
	], "user": [
		{"name": "user_clearance", "display_name": "User Clearance", "type": "multiselect", "template": "clearance"},
		{"name": "job_title", "display_name": "Job Title", "type": "text"}
	]}}`))

	require.NoError(t, err)
	require.Len(t, doc.Fields.Template, 2)
	assert.Equal(t, "clearance", doc.Fields.Template[0].Name)
	assert.Equal(t, "Clearance", doc.Fields.Template[0].DisplayName)
	assert.Equal(t, model.PropertyFieldTypeMultiselect, doc.Fields.Template[0].Type)
	require.Len(t, doc.Fields.Template[0].Options, 2)
	assert.Equal(t, "CUI", doc.Fields.Template[0].Options[0].Name)
	assert.Equal(t, "department", doc.Fields.Template[1].Name)

	require.Len(t, doc.Fields.User, 2)
	assert.Equal(t, "clearance", doc.Fields.User[0].Template)
	assert.Empty(t, doc.Fields.User[1].Template)
}

func TestParseAttributesDocument_FieldsTemplateAbsent(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "fields": {"user": [
		{"name": "job_title", "display_name": "Job Title", "type": "text"}
	]}}`))

	require.NoError(t, err)
	assert.Empty(t, doc.Fields.Template)
	assert.Empty(t, doc.Fields.User[0].Template)
}

func TestParseAttributesDocument_ChannelKeysAbsent(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "users": [{"email": "user1@example.com"}]}`))

	require.NoError(t, err)
	assert.Nil(t, doc.Fields.Channel)
	assert.Nil(t, doc.Channels)
}

func TestParseAttributesDocument_NonArrayFieldsUserRejected(t *testing.T) {
	doc, err := ParseAttributesDocument([]byte(`{"version": 2, "fields": {"user": "not a list"}}`))

	assert.Error(t, err)
	assert.Empty(t, doc.Users)
	assert.Contains(t, err.Error(), "not a valid attributes document")
}
