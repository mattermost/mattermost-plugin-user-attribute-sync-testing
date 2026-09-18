package sync

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestSyncClient(t *testing.T) (*pluginapi.Client, *plugintest.API) {
	t.Helper()

	api := &plugintest.API{}
	t.Cleanup(func() { api.AssertExpectations(t) })
	mockSyncLogs(api)

	return pluginapi.NewClient(api, &plugintest.Driver{}), api
}

func mockSyncLogs(api *plugintest.API) {
	const maxLogFields = 8
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

func TestSyncDocument(t *testing.T) {
	groupID := "test-group-id"
	pluginID := "test-plugin-id"

	t.Run("creates a field then writes a value for it in one call", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "job_title" && f.ID == ""
		})).Return(&model.PropertyField{ID: "generated_id_1", Name: "job_title", Type: model.PropertyFieldTypeText}, nil)
		mockEmptyFieldSearch(api, groupID)

		user := &model.User{Id: "user1", Email: "user1@example.com"}
		api.On("GetUserByEmail", "user1@example.com").Return(user, nil)
		api.On("UpsertPropertyValues", mock.MatchedBy(func(values []*model.PropertyValue) bool {
			return len(values) == 1 && values[0].FieldID == "generated_id_1" && values[0].TargetID == "user1"
		})).Return([]*model.PropertyValue{}, nil)

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				User: []FieldDefinition{{
					Name:        "job_title",
					DisplayName: "Job Title",
					Type:        model.PropertyFieldTypeText,
					AccessMode:  model.PropertyAccessModePublic,
				}},
			},
			Users: []map[string]interface{}{
				{"email": "user1@example.com", "job_title": "Engineer"},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 1, summary.FieldsCreated)
		assert.Equal(t, 0, summary.FieldsUpdated)
		assert.Equal(t, 0, summary.FieldsDeleted)
		assert.Equal(t, 0, summary.FieldsSkipped)
		assert.Equal(t, 1, summary.UsersSynced)
		assert.Equal(t, 0, summary.UsersSkipped)
		assert.Equal(t, 0, summary.ChannelsSynced)
		assert.Equal(t, 0, summary.ChannelsSkipped)
	})

	t.Run("creates a graph field then writes a graph value for it in one call", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "classification" && f.Type == model.PropertyFieldTypeGraph
		})).Return(&model.PropertyField{
			ID:   "generated_graph_id",
			Name: "classification",
			Type: model.PropertyFieldTypeGraph,
			Attrs: model.StringInterface{
				model.PropertyFieldAttributeOptions: []interface{}{
					map[string]interface{}{"id": "opt_id_a", "name": "Alpha"},
					map[string]interface{}{"id": "opt_id_b", "name": "Beta"},
				},
			},
		}, nil)
		mockEmptyFieldSearch(api, groupID)

		user := &model.User{Id: "user1", Email: "user1@example.com"}
		api.On("GetUserByEmail", "user1@example.com").Return(user, nil)
		api.On("UpsertPropertyValues", mock.MatchedBy(func(values []*model.PropertyValue) bool {
			if len(values) != 1 || values[0].FieldID != "generated_graph_id" || values[0].TargetID != "user1" {
				return false
			}
			var optionIDs []string
			if err := json.Unmarshal(values[0].Value, &optionIDs); err != nil {
				return false
			}
			return assert.ObjectsAreEqual([]string{"opt_id_a", "opt_id_b"}, optionIDs)
		})).Return([]*model.PropertyValue{}, nil)

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				User: []FieldDefinition{{
					Name:        "classification",
					DisplayName: "Classification",
					Type:        model.PropertyFieldTypeGraph,
					Options: []model.CustomProfileAttributesSelectOption{
						{Name: "Alpha"},
						{Name: "Beta"},
					},
				}},
			},
			Users: []map[string]interface{}{
				{"email": "user1@example.com", "classification": []interface{}{"Alpha", "Beta"}},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 1, summary.FieldsCreated)
		assert.Equal(t, 0, summary.FieldsSkipped)
		assert.Equal(t, 1, summary.UsersSynced)
		assert.Equal(t, 0, summary.UsersSkipped)
	})

	t.Run("nil error still reports skipped fields and users", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "job_title"
		})).Return(&model.PropertyField{ID: "generated_id_1", Name: "job_title", Type: model.PropertyFieldTypeText}, nil)
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "broken"
		})).Return(nil, errors.New("create failed"))
		mockEmptyFieldSearch(api, groupID)

		api.On("GetUserByEmail", "user1@example.com").Return(&model.User{Id: "user1", Email: "user1@example.com"}, nil)
		api.On("UpsertPropertyValues", mock.Anything).Return([]*model.PropertyValue{}, nil)

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				User: []FieldDefinition{
					{
						Name:        "job_title",
						DisplayName: "Job Title",
						Type:        model.PropertyFieldTypeText,
						AccessMode:  model.PropertyAccessModePublic,
					},
					{
						Name:        "broken",
						DisplayName: "Broken",
						Type:        model.PropertyFieldTypeText,
						AccessMode:  model.PropertyAccessModePublic,
					},
				},
			},
			Users: []map[string]interface{}{
				{"email": "user1@example.com", "job_title": "Engineer"},
				{"job_title": "No Email"},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 1, summary.FieldsCreated)
		assert.Equal(t, 1, summary.FieldsSkipped)
		assert.Equal(t, 1, summary.UsersSynced)
		assert.Equal(t, 1, summary.UsersSkipped)
		assert.Equal(t, 0, summary.ChannelsSynced)
		assert.Equal(t, 0, summary.ChannelsSkipped)
	})

	t.Run("runs the deletion scan once per document", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		omitted := &model.PropertyField{
			ID:         model.NewId(),
			Name:       "old_title",
			ObjectType: model.PropertyFieldObjectTypeUser,
			CreateAt:   1,
			Attrs:      model.StringInterface{model.PropertyAttrsSourcePluginID: pluginID},
		}
		api.On("SearchPropertyFields", groupID, mock.MatchedBy(func(opts model.PropertyFieldSearchOpts) bool {
			return len(opts.ObjectTypes) == 1 && opts.ObjectTypes[0] == model.PropertyFieldObjectTypeUser
		})).Return([]*model.PropertyField{}, nil)
		api.On("SearchPropertyFields", groupID, mock.MatchedBy(func(opts model.PropertyFieldSearchOpts) bool {
			return len(opts.ObjectTypes) == 0
		})).Return([]*model.PropertyField{omitted}, nil).Once()
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "job_title"
		})).Return(&model.PropertyField{ID: "generated_id_1", Name: "job_title", Type: model.PropertyFieldTypeText}, nil)
		mock.InOrder(
			api.On("DeletePropertyValuesForField", groupID, omitted.ID).Return(nil).Once(),
			api.On("DeletePropertyField", groupID, omitted.ID).Return(nil).Once(),
		)

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				User: []FieldDefinition{{
					Name:        "job_title",
					DisplayName: "Job Title",
					Type:        model.PropertyFieldTypeText,
					AccessMode:  model.PropertyAccessModePublic,
				}},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 1, summary.FieldsCreated)
		assert.Equal(t, 1, summary.FieldsDeleted)
	})

	t.Run("creates channel fields with an Enterprise Advanced license", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		api.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced})
		mockEmptyFieldSearch(api, groupID)
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "department" &&
				f.ObjectType == model.PropertyFieldObjectTypeChannel &&
				f.TargetType == string(model.PropertyFieldTargetLevelSystem)
		})).Return(&model.PropertyField{ID: "generated_channel_id", Name: "department", Type: model.PropertyFieldTypeText}, nil)

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				Channel: []FieldDefinition{{
					Name:        "department",
					DisplayName: "Department",
					Type:        model.PropertyFieldTypeText,
				}},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 1, summary.FieldsCreated)
		assert.Equal(t, 0, summary.FieldsSkipped)
	})

	t.Run("creates a channel field then writes a channel value for it in one call", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		api.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced})
		mockEmptyFieldSearch(api, groupID)
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.Name == "department" && f.ObjectType == model.PropertyFieldObjectTypeChannel
		})).Return(&model.PropertyField{ID: "generated_channel_id", Name: "department", Type: model.PropertyFieldTypeText}, nil)

		channel := &model.Channel{Id: "channel1", Name: "town-square"}
		api.On("GetChannelByNameForTeamName", "ad-1", "town-square", false).Return(channel, nil)
		api.On("UpsertPropertyValues", mock.MatchedBy(func(values []*model.PropertyValue) bool {
			return len(values) == 1 &&
				values[0].FieldID == "generated_channel_id" &&
				values[0].TargetType == model.PropertyValueTargetTypeChannel &&
				values[0].TargetID == "channel1"
		})).Return([]*model.PropertyValue{}, nil)

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				Channel: []FieldDefinition{{
					Name:        "department",
					DisplayName: "Department",
					Type:        model.PropertyFieldTypeText,
				}},
			},
			Channels: []map[string]interface{}{
				{"team": "ad-1", "channel": "town-square", "department": "Engineering"},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 1, summary.FieldsCreated)
		assert.Equal(t, 0, summary.FieldsSkipped)
		assert.Equal(t, 1, summary.ChannelsSynced)
		assert.Equal(t, 0, summary.ChannelsSkipped)
	})

	t.Run("creates a template before the user field that links to it", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		mockEmptyFieldSearch(api, groupID)
		mock.InOrder(
			api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
				return f.Name == "clearance" &&
					f.ObjectType == model.PropertyFieldObjectTypeTemplate &&
					f.LinkedFieldID == nil
			})).Return(&model.PropertyField{
				ID:   "template_id",
				Name: "clearance",
				Type: model.PropertyFieldTypeRank,
				Attrs: model.StringInterface{
					model.PropertyFieldAttributeOptions: []interface{}{
						map[string]interface{}{"id": "opt_id_cui", "name": "CUI", "rank": 1},
						map[string]interface{}{"id": "opt_id_secret", "name": "Secret", "rank": 3},
					},
				},
			}, nil).Once(),
			api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
				return f.Name == "clearance" &&
					f.ObjectType == model.PropertyFieldObjectTypeUser &&
					f.LinkedFieldID != nil && *f.LinkedFieldID == "template_id"
			})).Return(&model.PropertyField{ID: "generated_user_id", Name: "clearance"}, nil).Once(),
		)

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				Template: []FieldDefinition{{
					Name:        "clearance",
					DisplayName: "Clearance",
					Type:        model.PropertyFieldTypeRank,
					Options: []model.CustomProfileAttributesSelectOption{
						{Name: "CUI", Rank: model.NewPointer(1)},
						{Name: "Secret", Rank: model.NewPointer(3)},
					},
				}},
				User: []FieldDefinition{{
					Name:        "clearance",
					DisplayName: "Clearance",
					Template:    "clearance",
				}},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 2, summary.FieldsCreated)
		assert.Equal(t, 0, summary.FieldsSkipped)
	})

	t.Run("writes the template's option ID for user and channel values on linked fields", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		api.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced})
		mockEmptyFieldSearch(api, groupID)

		templateOptions := []interface{}{
			map[string]interface{}{"id": "opt_id_cui", "name": "CUI", "rank": 1},
			map[string]interface{}{"id": "opt_id_secret", "name": "Secret", "rank": 3},
		}
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.ObjectType == model.PropertyFieldObjectTypeTemplate && f.Name == "clearance_levels"
		})).Return(&model.PropertyField{
			ID:   "template_id",
			Name: "clearance_levels",
			Type: model.PropertyFieldTypeRank,
			Attrs: model.StringInterface{
				model.PropertyFieldAttributeOptions: templateOptions,
			},
		}, nil).Once()
		// The server copies the template's type and options onto each linked
		// field it creates.
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.ObjectType == model.PropertyFieldObjectTypeUser &&
				f.LinkedFieldID != nil && *f.LinkedFieldID == "template_id"
		})).Return(&model.PropertyField{
			ID:   "user_field_id",
			Name: "clearance",
			Type: model.PropertyFieldTypeRank,
			Attrs: model.StringInterface{
				model.PropertyFieldAttributeOptions: templateOptions,
			},
		}, nil).Once()
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.ObjectType == model.PropertyFieldObjectTypeChannel &&
				f.LinkedFieldID != nil && *f.LinkedFieldID == "template_id"
		})).Return(&model.PropertyField{
			ID:   "channel_field_id",
			Name: "clearance",
			Type: model.PropertyFieldTypeRank,
			Attrs: model.StringInterface{
				model.PropertyFieldAttributeOptions: templateOptions,
			},
		}, nil).Once()

		user := &model.User{Id: "user1", Email: "user1@example.com"}
		api.On("GetUserByEmail", "user1@example.com").Return(user, nil)
		channel := &model.Channel{Id: "channel1", Name: "town-square"}
		api.On("GetChannelByNameForTeamName", "ad-1", "town-square", false).Return(channel, nil)

		// Both values are the rank string "Secret"; both must write the
		// template's option ID for it.
		secretValue, err := json.Marshal("opt_id_secret")
		require.NoError(t, err)
		api.On("UpsertPropertyValues", mock.MatchedBy(func(values []*model.PropertyValue) bool {
			return len(values) == 1 &&
				values[0].FieldID == "user_field_id" &&
				values[0].TargetID == "user1" &&
				assert.ObjectsAreEqual(json.RawMessage(secretValue), values[0].Value)
		})).Return([]*model.PropertyValue{}, nil).Once()
		api.On("UpsertPropertyValues", mock.MatchedBy(func(values []*model.PropertyValue) bool {
			return len(values) == 1 &&
				values[0].FieldID == "channel_field_id" &&
				values[0].TargetID == "channel1" &&
				assert.ObjectsAreEqual(json.RawMessage(secretValue), values[0].Value)
		})).Return([]*model.PropertyValue{}, nil).Once()

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				Template: []FieldDefinition{{
					Name:        "clearance_levels",
					DisplayName: "Clearance Levels",
					Type:        model.PropertyFieldTypeRank,
					Options: []model.CustomProfileAttributesSelectOption{
						{Name: "CUI", Rank: model.NewPointer(1)},
						{Name: "Secret", Rank: model.NewPointer(3)},
					},
				}},
				User: []FieldDefinition{{
					Name:        "clearance",
					DisplayName: "Clearance",
					Template:    "clearance_levels",
				}},
				Channel: []FieldDefinition{{
					Name:        "clearance",
					DisplayName: "Clearance",
					Template:    "clearance_levels",
				}},
			},
			Users: []map[string]interface{}{
				{"email": "user1@example.com", "clearance": "Secret"},
			},
			Channels: []map[string]interface{}{
				{"team": "ad-1", "channel": "town-square", "clearance": "Secret"},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 3, summary.FieldsCreated)
		assert.Equal(t, 0, summary.FieldsSkipped)
		assert.Equal(t, 1, summary.UsersSynced)
		assert.Equal(t, 0, summary.UsersSkipped)
		assert.Equal(t, 1, summary.ChannelsSynced)
		assert.Equal(t, 0, summary.ChannelsSkipped)
	})

	t.Run("a value naming the template is skipped as an unknown field", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		mockEmptyFieldSearch(api, groupID)
		templateOptions := []interface{}{
			map[string]interface{}{"id": "opt_id_cui", "name": "CUI", "rank": 1},
			map[string]interface{}{"id": "opt_id_secret", "name": "Secret", "rank": 3},
		}
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.ObjectType == model.PropertyFieldObjectTypeTemplate
		})).Return(&model.PropertyField{
			ID:   "template_id",
			Name: "clearance_levels",
			Type: model.PropertyFieldTypeRank,
			Attrs: model.StringInterface{
				model.PropertyFieldAttributeOptions: templateOptions,
			},
		}, nil).Once()
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.ObjectType == model.PropertyFieldObjectTypeUser &&
				f.LinkedFieldID != nil && *f.LinkedFieldID == "template_id"
		})).Return(&model.PropertyField{
			ID:   "user_field_id",
			Name: "clearance",
			Type: model.PropertyFieldTypeRank,
			Attrs: model.StringInterface{
				model.PropertyFieldAttributeOptions: templateOptions,
			},
		}, nil).Once()

		user := &model.User{Id: "user1", Email: "user1@example.com"}
		api.On("GetUserByEmail", "user1@example.com").Return(user, nil)
		// Templates hold no values: the record's "clearance_levels" entry is
		// skipped, and only the linked field's value is written.
		api.On("UpsertPropertyValues", mock.MatchedBy(func(values []*model.PropertyValue) bool {
			return len(values) == 1 && values[0].FieldID == "user_field_id"
		})).Return([]*model.PropertyValue{}, nil).Once()

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				Template: []FieldDefinition{{
					Name:        "clearance_levels",
					DisplayName: "Clearance Levels",
					Type:        model.PropertyFieldTypeRank,
					Options: []model.CustomProfileAttributesSelectOption{
						{Name: "CUI", Rank: model.NewPointer(1)},
						{Name: "Secret", Rank: model.NewPointer(3)},
					},
				}},
				User: []FieldDefinition{{
					Name:        "clearance",
					DisplayName: "Clearance",
					Template:    "clearance_levels",
				}},
			},
			Users: []map[string]interface{}{
				{"email": "user1@example.com", "clearance": "Secret", "clearance_levels": "Secret"},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 2, summary.FieldsCreated)
		assert.Equal(t, 1, summary.UsersSynced)
		assert.Equal(t, 0, summary.UsersSkipped)
	})

	t.Run("creates templates and linked user fields below Enterprise Advanced", func(t *testing.T) {
		client, api := newTestSyncClient(t)

		api.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise})
		mockEmptyFieldSearch(api, groupID)
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.ObjectType == model.PropertyFieldObjectTypeTemplate
		})).Return(&model.PropertyField{ID: "template_id", Name: "clearance", Type: model.PropertyFieldTypeText}, nil).Once()
		api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
			return f.ObjectType == model.PropertyFieldObjectTypeUser &&
				f.LinkedFieldID != nil && *f.LinkedFieldID == "template_id"
		})).Return(&model.PropertyField{ID: "generated_user_id", Name: "clearance"}, nil).Once()

		doc := AttributesDocument{
			Version: SupportedDocumentVersion,
			Fields: FieldSchema{
				Template: []FieldDefinition{{
					Name:        "clearance",
					DisplayName: "Clearance",
					Type:        model.PropertyFieldTypeText,
				}},
				User: []FieldDefinition{{
					Name:        "clearance",
					DisplayName: "Clearance",
					Template:    "clearance",
				}},
				Channel: []FieldDefinition{{
					Name:        "department",
					DisplayName: "Department",
					Type:        model.PropertyFieldTypeText,
				}},
			},
		}

		summary, err := SyncDocument(client, groupID, pluginID, doc)
		require.NoError(t, err)
		assert.Equal(t, 2, summary.FieldsCreated)
		assert.Equal(t, 1, summary.FieldsSkipped)
	})

	t.Run("skips channel fields below Enterprise Advanced", func(t *testing.T) {
		licenses := map[string]*model.License{
			"no license": nil,
			"enterprise": {SkuShortName: model.LicenseShortSkuEnterprise},
		}
		for name, license := range licenses {
			t.Run(name, func(t *testing.T) {
				client, api := newTestSyncClient(t)

				api.On("GetLicense").Return(license)
				api.On("SearchPropertyFields", groupID, mock.MatchedBy(func(opts model.PropertyFieldSearchOpts) bool {
					return len(opts.ObjectTypes) == 1 && opts.ObjectTypes[0] == model.PropertyFieldObjectTypeUser
				})).Return([]*model.PropertyField{}, nil)
				// A channel field a licensed run created survives a downgrade:
				// the keep-set comes from the document, not from what synced.
				existingChannelField := &model.PropertyField{
					ID:         model.NewId(),
					Name:       "department",
					ObjectType: model.PropertyFieldObjectTypeChannel,
					CreateAt:   1,
					Attrs:      model.StringInterface{model.PropertyAttrsSourcePluginID: pluginID},
				}
				api.On("SearchPropertyFields", groupID, mock.MatchedBy(func(opts model.PropertyFieldSearchOpts) bool {
					return len(opts.ObjectTypes) == 0
				})).Return([]*model.PropertyField{existingChannelField}, nil)
				api.On("CreatePropertyField", mock.MatchedBy(func(f *model.PropertyField) bool {
					return f.Name == "job_title"
				})).Return(&model.PropertyField{ID: "generated_id_1", Name: "job_title", Type: model.PropertyFieldTypeText}, nil)

				doc := AttributesDocument{
					Version: SupportedDocumentVersion,
					Fields: FieldSchema{
						User: []FieldDefinition{{
							Name:        "job_title",
							DisplayName: "Job Title",
							Type:        model.PropertyFieldTypeText,
						}},
						Channel: []FieldDefinition{{
							Name:        "department",
							DisplayName: "Department",
							Type:        model.PropertyFieldTypeText,
						}},
					},
				}

				summary, err := SyncDocument(client, groupID, pluginID, doc)
				require.NoError(t, err)
				assert.Equal(t, 1, summary.FieldsCreated)
				assert.Equal(t, 1, summary.FieldsSkipped)
				assert.Equal(t, 0, summary.FieldsDeleted)
			})
		}
	})
}
