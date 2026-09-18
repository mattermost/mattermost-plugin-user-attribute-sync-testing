package sync

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

// FieldIDCache stores mappings from external field/option names to Mattermost-generated IDs.
// These IDs are dynamically loaded during plugin activation by creating fields and looking up their IDs.
type FieldIDCache struct {
	// Maps external field names (e.g., "job_title") to Mattermost field IDs
	FieldNameToID map[string]string
	// Maps option names (e.g., "Apples") to Mattermost option IDs for all select/multiselect/rank fields
	// Option names are prefixed with field names to avoid name collision.
	OptionNameToID map[string]string
	// Maps external field names to the type declared in the document, so value sync can tell
	// a rank/select string (write the option ID) from text or date (write the string).
	FieldNameToType map[string]model.PropertyFieldType
}

func NewFieldIDCache() *FieldIDCache {
	return &FieldIDCache{
		FieldNameToID:   make(map[string]string),
		OptionNameToID:  make(map[string]string),
		FieldNameToType: make(map[string]model.PropertyFieldType),
	}
}

// GetFieldID translates an external field name to its Mattermost field ID.
func (c *FieldIDCache) GetFieldID(fieldName string) string {
	return c.FieldNameToID[fieldName]
}

// GetOptionID translates a select/multiselect/rank option name to its Mattermost option ID.
func (c *FieldIDCache) GetOptionID(fieldName, optionName string) string {
	// Keys are prefixes with field name to avoid option name collision
	return c.OptionNameToID[fieldName+"|"+optionName]
}

func (c *FieldIDCache) GetFieldType(fieldName string) model.PropertyFieldType {
	return c.FieldNameToType[fieldName]
}

type FieldDefinition struct {
	// Name is the canonical field identifier. It must match ^[A-Za-z_][A-Za-z0-9_]*$
	// because Mattermost references the name from ABAC policy expressions as
	// user.attributes.<name> (a CEL identifier), so spaces and punctuation are
	// rejected. We also use this name as the lookup key when matching attributes
	// from the external data source — the JSON file's keys must match these names.
	Name string `json:"name"`

	// DisplayName is the human-readable label shown in user-facing UI. Free-form
	// text; no character restrictions.
	DisplayName string `json:"display_name"`

	Type    model.PropertyFieldType                     `json:"type"`
	Options []model.CustomProfileAttributesSelectOption `json:"options,omitempty"`
	// The document may write "public" for the empty-string mode; accessMode()
	// translates that, because the server rejects the literal "public".
	// SharedOnly is only valid for select, multiselect, and rank; on rank, a
	// user sees their own rank and lower.
	AccessMode string `json:"access_mode,omitempty"`

	// Template names an entry in the document's fields.template list; empty means
	// the field is not linked.
	Template string `json:"template,omitempty"`

	Visibility        string `json:"visibility,omitempty"`
	PermissionField   string `json:"permission_field,omitempty"`
	PermissionValues  string `json:"permission_values,omitempty"`
	PermissionOptions string `json:"permission_options,omitempty"`
}

func (d FieldDefinition) visibility() string {
	if d.Visibility == "" {
		return model.PropertyFieldVisibilityAlways
	}
	return d.Visibility
}

func (d FieldDefinition) accessMode() string {
	if d.AccessMode == "" || d.AccessMode == "public" {
		return model.PropertyAccessModePublic
	}
	return d.AccessMode
}

func (d FieldDefinition) permissionField() *model.PermissionLevel {
	return permissionLevelPtr(d.PermissionField)
}

func (d FieldDefinition) permissionValues() *model.PermissionLevel {
	return permissionLevelPtr(d.PermissionValues)
}

func (d FieldDefinition) permissionOptions() *model.PermissionLevel {
	return permissionLevelPtr(d.PermissionOptions)
}

func permissionLevelPtr(value string) *model.PermissionLevel {
	level := model.PermissionLevelSysadmin
	if value != "" {
		level = model.PermissionLevel(value)
	}
	return &level
}

func updateField(
	client *pluginapi.Client,
	groupID string,
	existingField *model.PropertyField,
	def FieldDefinition,
	cache *FieldIDCache,
) (*model.PropertyField, error) {
	client.Log.Info("Field exists, updating to match definition",
		"field_id", existingField.ID,
		"name", def.Name)

	// A linked field's type, options, and access mode belong to its template:
	// the server rejects an update that changes them, and a linked definition
	// declares none, so writing them would send empty values read as changes.
	// LinkedFieldID is likewise left as the server returned it — a link can
	// only be set at create.
	linked := def.Template != ""
	if !linked {
		existingField.Type = def.Type
	}
	existingField.Attrs[model.PropertyFieldAttrVisibility] = def.visibility()
	existingField.Attrs[model.PropertyFieldAttrDisplayName] = def.DisplayName
	existingField.Attrs[model.PropertyAttrsProtected] = true
	if !linked {
		existingField.Attrs[model.PropertyAttrsAccessMode] = def.accessMode()
	}
	// See createField for why the permission-level default is sysadmin.
	existingField.PermissionField = def.permissionField()
	existingField.PermissionValues = def.permissionValues()
	existingField.PermissionOptions = def.permissionOptions()

	if !linked && def.Type.SupportsOptions() {
		options, err := buildOptionsArr(def, cache)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to update existing field %s", def.Name)
		}
		existingField.Attrs[model.PropertyFieldAttributeOptions] = options
	}

	updatedField, err := client.Property.UpdatePropertyField(groupID, existingField)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to update existing field %s", def.Name)
	}

	client.Log.Info("Updated field successfully", "field_id", updatedField.ID, "name", def.Name)
	return updatedField, nil
}

func buildOptionsArr(def FieldDefinition, cache *FieldIDCache) ([]interface{}, error) {
	options := make([]model.CustomProfileAttributesSelectOption, len(def.Options))
	for i, option := range def.Options {
		// Add in ID if it's already in the cache, otherwise Mattermost will generate a new one
		// Don't pass in a cache if you don't need it (like when creating a new field)
		if cache != nil {
			id := cache.GetOptionID(def.Name, option.Name)
			if id != "" {
				option.ID = id
			}
		}

		if def.Type == model.PropertyFieldTypeRank && option.Rank == nil {
			return nil, fmt.Errorf("missing Rank value for option %s on field %s", option.Name, def.Name)
		}
		options[i] = option
	}

	raw, err := json.Marshal(options)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal options for field %s", def.Name)
	}

	// The []interface{} type is a registered type in the pluginAPI's RPC call and will ensure the data gets through intact.
	var optionsArr []interface{}
	if err := json.Unmarshal(raw, &optionsArr); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal options for field %s", def.Name)
	}
	return optionsArr, nil
}

func createField(
	client *pluginapi.Client,
	groupID string,
	objectType string,
	def FieldDefinition,
	linkedFieldID *string,
) (*model.PropertyField, error) {
	client.Log.Info("Field does not exist, creating", "name", def.Name)

	// Shared_only rejects a member-level PermissionValues (members edit their own
	// value): anyone could pick any value and fake sharing it. PermissionValues defaults
	// to sysadmin to clear that check. PermissionField and PermissionOptions are
	// pinned to sysadmin by the server for access_control fields, and default
	// the same way so all three read identically.
	field := &model.PropertyField{
		GroupID:           groupID,
		Name:              def.Name,
		Type:              def.Type,
		PermissionField:   def.permissionField(),
		PermissionValues:  def.permissionValues(),
		PermissionOptions: def.permissionOptions(),

		// ObjectType declares what kind of object this field describes ("user"
		// or "channel"). Mattermost uses it to route field queries — for
		// example, the user-profile UI asks for fields with ObjectType=user, and
		// ABAC policy evaluation looks up user.attributes.<field> against the
		// same set.
		ObjectType: objectType,

		// TargetType declares the scope at which the field definition lives.
		// "system" means the field is defined once globally and applies to every
		// object of its type on the server. The other options ("team", "channel")
		// would scope the field to a specific team or channel, which is not what
		// we want for org-wide attributes. With TargetType=system, TargetID must
		// be empty (the system has no per-entity ID).
		TargetType: string(model.PropertyFieldTargetLevelSystem),

		// Non-nil only for a field linked to a template; the server then copies
		// type, options, and access mode from the template.
		LinkedFieldID: linkedFieldID,

		Attrs: model.StringInterface{
			// DisplayName is the user-facing label rendered in profile cards and the
			// System Console. Mattermost's Name field is a CEL identifier and can't
			// contain spaces or punctuation, so anything human-readable lives here.
			model.PropertyFieldAttrDisplayName: def.DisplayName,

			// Visibility is UI-only; AccessMode is who can read via API.
			model.PropertyFieldAttrVisibility: def.visibility(),

			// Protected means only this plugin can:
			//   - Modify field structure (add/remove options, change field type)
			//   - Write/update values
			// This prevents users and admins from manually editing data that should be
			// synchronized from an external source. Required for non-public access modes.
			model.PropertyAttrsProtected: true,
		},
	}

	// A linked field inherits access mode and options from its template: the
	// server overwrites whatever is sent, and rejects a create that sends
	// options for a graph-typed template.
	if linkedFieldID == nil {
		// See FieldDefinition.AccessMode for details on the three modes.
		field.Attrs[model.PropertyAttrsAccessMode] = def.accessMode()
	}

	// Select / Multiselect / Rank fields need their options defined
	if linkedFieldID == nil && def.Type.SupportsOptions() {
		// No need to pass in a cache when creating a new field
		options, err := buildOptionsArr(def, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create field %s", def.Name)
		}
		field.Attrs[model.PropertyFieldAttributeOptions] = options
	}

	createdField, err := client.Property.CreatePropertyField(field)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create field %s", def.Name)
	}

	client.Log.Info("Created field successfully", "field_id", createdField.ID, "name", def.Name)
	return createdField, nil
}

// An admin-created field has no source_plugin_id; a non-string attribute is
// treated the same — both look unowned.
func fieldSourcePluginID(field *model.PropertyField) string {
	raw, ok := field.Attrs[model.PropertyAttrsSourcePluginID]
	if !ok {
		return ""
	}
	id, ok := raw.(string)
	if !ok {
		return ""
	}
	return id
}

func isFieldOwnedByPlugin(
	client *pluginapi.Client,
	existingField *model.PropertyField,
	pluginID string,
	def FieldDefinition,
) bool {
	sourceID := fieldSourcePluginID(existingField)
	if sourceID == pluginID {
		return true
	}
	if sourceID == "" {
		client.Log.Error("Field already exists but has no source_plugin_id (likely created by admin)",
			"field_name", def.Name,
			"field_id", existingField.ID)
		return false
	}

	client.Log.Error("Field already exists but is owned by another plugin",
		"field_name", def.Name,
		"field_id", existingField.ID,
		"owner_plugin_id", sourceID)
	return false
}

// syncSingleField creates or updates one field. existingField is the lookup
// result for def.Name, nil when no field of that name and object type exists.
// templateIDs maps template names to their server-generated field IDs.
func syncSingleField(
	client *pluginapi.Client,
	groupID string,
	pluginID string,
	objectType string,
	def FieldDefinition,
	templateIDs map[string]string,
	existingField *model.PropertyField,
	cache *FieldIDCache,
) (string, bool, error) {
	// A template name the map does not contain fails this field only. Creating
	// the field unlinked instead would create something the document never
	// asked for.
	var linkedFieldID *string
	if def.Template != "" {
		id, ok := templateIDs[def.Template]
		if !ok {
			return "", false, errors.Errorf("field %s links to unknown template %s", def.Name, def.Template)
		}
		linkedFieldID = &id
	}

	var field *model.PropertyField
	created := false
	if existingField != nil {
		if !isFieldOwnedByPlugin(client, existingField, pluginID, def) {
			return "", false, errors.Errorf(
				"field %s already exists but is not managed by this plugin",
				def.Name)
		}

		// The server already assigned IDs to this field's options, and the cache is
		// empty on every activation. Load them before building the update payload so
		// buildOptionsArr sends the existing IDs back - otherwise the server mints new
		// ones and every stored value is left pointing at an option that no
		// longer exists.
		if def.Type.SupportsOptions() && len(def.Options) > 0 {
			if err := extractOptionIDs(client, existingField, def, cache); err != nil {
				client.Log.Warn("Failed to read existing option IDs",
					"name", def.Name,
					"field_id", existingField.ID,
					"error", err.Error())
				// Don't fail the sync - the server will generate new option IDs
			}
		}

		var err error
		field, err = updateField(client, groupID, existingField, def, cache)
		if err != nil {
			return "", false, err
		}
	} else {
		var err error
		field, err = createField(client, groupID, objectType, def, linkedFieldID)
		if err != nil {
			return "", false, err
		}
		created = true
	}

	cache.FieldNameToID[def.Name] = field.ID

	if def.Type.SupportsOptions() && len(def.Options) > 0 {
		if err := extractOptionIDs(client, field, def, cache); err != nil {
			client.Log.Error("Failed to extract option IDs",
				"name", def.Name,
				"field_id", field.ID,
				"error", err.Error())
		}
	}

	return field.ID, created, nil
}

// extractOptionIDs extracts option IDs from a field into the cache (if applicable).
// Avoids adding duplicate options with the same name.
func extractOptionIDs(
	client *pluginapi.Client,
	field *model.PropertyField,
	def FieldDefinition,
	cache *FieldIDCache,
) error {
	// Extract option IDs from the field attributes
	optionsRaw, ok := field.Attrs[model.PropertyFieldAttributeOptions]
	if !ok {
		return errors.New("field has no options attribute")
	}

	// Convert options to JSON and back to extract IDs
	optionsJSON, err := json.Marshal(optionsRaw)
	if err != nil {
		return errors.Wrap(err, "failed to marshal options")
	}

	var options []map[string]interface{}
	if err := json.Unmarshal(optionsJSON, &options); err != nil {
		return errors.Wrap(err, "failed to unmarshal options")
	}

	// Build option name to ID mapping for all supported fields
	for _, opt := range options {
		name, nameOk := opt["name"].(string)
		id, idOk := opt["id"].(string)
		if !nameOk || !idOk {
			continue
		}

		// Prefix field name to avoid option name collision
		optionKey := def.Name + "|" + name
		// Avoid duplicate option names - only add if not already in cache
		if _, exists := cache.OptionNameToID[optionKey]; !exists {
			cache.OptionNameToID[optionKey] = id
		}
	}

	client.Log.Debug("Extracted option IDs",
		"field_name", def.Name,
		"option_count", len(options))

	return nil
}

//nolint:revive
func SyncFields(client *pluginapi.Client, groupID, pluginID, objectType string, defs []FieldDefinition, templateIDs map[string]string, summary *Summary) (*FieldIDCache, error) {
	client.Log.Info("Syncing field definitions", "field_count", len(defs))

	cache := NewFieldIDCache()

	var failedFields []string

	// GetPropertyFieldByName is not object-type aware, so existing fields are
	// looked up with an ObjectTypes-filtered search instead. A failed search is
	// not "the fields do not exist" — creating anyway would duplicate them — so
	// every def is skipped.
	fields, err := searchGroupFields(client, groupID, []string{objectType})
	if err != nil {
		client.Log.Error("Failed to look up existing fields, skipping field sync",
			"object_type", objectType,
			"error", err.Error())
		for _, def := range defs {
			failedFields = append(failedFields, def.Name)
		}
	} else {
		existing := make(map[string]*model.PropertyField, len(fields))
		for _, field := range fields {
			existing[field.Name] = field
		}

		for _, def := range defs {
			_, created, err := syncSingleField(client, groupID, pluginID, objectType, def, templateIDs, existing[def.Name], cache)
			if err != nil {
				client.Log.Error("Failed to sync field",
					"name", def.Name,
					"error", err.Error())
				failedFields = append(failedFields, def.Name)
				continue
			}
			if created {
				summary.FieldsCreated++
			} else {
				summary.FieldsUpdated++
			}
			cache.FieldNameToType[def.Name] = def.Type
		}
	}

	if len(failedFields) > 0 {
		client.Log.Warn("Some fields failed to sync",
			"failed_count", len(failedFields),
			"failed_fields", failedFields)
	}
	summary.FieldsSkipped += len(failedFields)

	client.Log.Info("Field sync completed",
		"total", len(defs),
		"failed", len(failedFields),
		"fields_cached", len(cache.FieldNameToID),
		"options_cached", len(cache.OptionNameToID))

	return cache, nil
}

const fieldSearchPerPage = 100

// searchGroupFields pages through every field in the group whose object type is
// in objectTypes. An empty objectTypes returns every field in the group.
func searchGroupFields(client *pluginapi.Client, groupID string, objectTypes []string) ([]*model.PropertyField, error) {
	var all []*model.PropertyField
	var cursor model.PropertyFieldSearchCursor
	for {
		opts := model.PropertyFieldSearchOpts{
			PerPage:     fieldSearchPerPage,
			ObjectTypes: objectTypes,
			Cursor:      cursor,
		}
		fields, err := client.Property.SearchPropertyFields(groupID, opts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to search property fields")
		}
		all = append(all, fields...)

		if len(fields) < fieldSearchPerPage {
			return all, nil
		}
		last := fields[len(fields)-1]
		cursor = model.PropertyFieldSearchCursor{
			PropertyFieldID: last.ID,
			CreateAt:        last.CreateAt,
		}
	}
}

// Values are deleted before the field so the server can authorize the value
// delete against a live field.
//
// The keep-set is keyed by object type and name: a user field, a channel
// field, and a template may share a name, and the document may define one
// without the others.
func DeleteOmittedFields(client *pluginapi.Client, groupID, pluginID string, userDefs, channelDefs, templateDefs []FieldDefinition, summary *Summary) {
	named := make(map[string]struct{}, len(userDefs)+len(channelDefs)+len(templateDefs))
	for _, def := range userDefs {
		named[model.PropertyFieldObjectTypeUser+"|"+def.Name] = struct{}{}
	}
	for _, def := range channelDefs {
		named[model.PropertyFieldObjectTypeChannel+"|"+def.Name] = struct{}{}
	}
	for _, def := range templateDefs {
		named[model.PropertyFieldObjectTypeTemplate+"|"+def.Name] = struct{}{}
	}

	fields, err := searchGroupFields(client, groupID, nil)
	if err != nil {
		client.Log.Error("Failed to search property fields for deletion", "error", err.Error())
		return
	}

	for _, field := range fields {
		if _, keep := named[field.ObjectType+"|"+field.Name]; keep {
			continue
		}

		owner := fieldSourcePluginID(field)
		if owner != pluginID {
			client.Log.Debug("Skipping field not owned by this plugin",
				"field_name", field.Name,
				"field_id", field.ID,
				"owner_plugin_id", owner)
			continue
		}

		if err := client.Property.DeletePropertyValuesForField(groupID, field.ID); err != nil {
			client.Log.Error("Failed to delete values for omitted field",
				"field_name", field.Name,
				"field_id", field.ID,
				"error", err.Error())
			continue
		}
		if err := client.Property.DeletePropertyField(groupID, field.ID); err != nil {
			client.Log.Error("Failed to delete omitted field",
				"field_name", field.Name,
				"field_id", field.ID,
				"error", err.Error())
			continue
		}
		summary.FieldsDeleted++
		client.Log.Info("Deleted omitted field",
			"field_name", field.Name,
			"field_id", field.ID)
	}
}
