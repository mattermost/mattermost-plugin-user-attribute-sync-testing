package sync

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// formatStringValue converts text or date values to the JSON format required by PropertyService.
func formatStringValue(value string) (json.RawMessage, error) {
	marshaled, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal string value: %w", err)
	}

	return json.RawMessage(marshaled), nil
}

// formatMultiselectValue converts multiselect option names to option IDs in JSON format.
// Multiselect fields store arrays of option IDs, not human-readable names.
// Works with any multiselect field that has options in the cache.
func formatMultiselectValue(fieldName string, values []string, cache *FieldIDCache) (json.RawMessage, error) {
	// Translate option names to IDs
	optionIDs := make([]string, 0, len(values))
	for _, optionName := range values {
		optionID := cache.GetOptionID(fieldName, optionName)
		if optionID == "" {
			return nil, fmt.Errorf("unknown option %q for field %s", optionName, fieldName)
		}
		optionIDs = append(optionIDs, optionID)
	}

	marshaled, err := json.Marshal(optionIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal multiselect value: %w", err)
	}

	return json.RawMessage(marshaled), nil
}

func formatOptionValue(fieldName, value string, cache *FieldIDCache) (json.RawMessage, error) {
	// Translate option name to ID
	optionID := cache.GetOptionID(fieldName, value)
	if optionID == "" {
		return nil, fmt.Errorf("unknown option %q for field %s", value, fieldName)
	}

	return formatStringValue(optionID)
}

// buildPropertyValue creates a single PropertyValue for one attribute of a target.
// identityKeys are the record's identity fields (email on a user, team/channel on a
// channel); they map the record to a Mattermost object and are never written as
// attributes. label identifies the record in log lines.
// Returns nil, nil for fields that should be skipped.
// Returns nil, error for fields that fail validation or formatting.
func buildPropertyValue(
	api *pluginapi.Client,
	targetType string,
	targetID string,
	identityKeys []string,
	label string,
	groupID string,
	fieldName string,
	fieldValue interface{},
	cache *FieldIDCache,
) (*model.PropertyValue, error) {
	if slices.Contains(identityKeys, fieldName) {
		return nil, nil
	}

	fieldID := cache.GetFieldID(fieldName)
	if fieldID == "" {
		api.Log.Warn("Unknown field name, skipping",
			"field_name", fieldName,
			"record", label)
		return nil, nil
	}

	// Format value based on type
	var formattedValue json.RawMessage
	var formatErr error

	switch v := fieldValue.(type) {
	case []interface{}:
		// Multiselect - convert to string array
		stringValues := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				stringValues = append(stringValues, str)
			}
		}
		formattedValue, formatErr = formatMultiselectValue(fieldName, stringValues, cache)

	case []string:
		// Multiselect - already string array
		formattedValue, formatErr = formatMultiselectValue(fieldName, v, cache)

	case string:
		fieldType := cache.GetFieldType(fieldName)
		if fieldType == model.PropertyFieldTypeRank || fieldType == model.PropertyFieldTypeSelect {
			formattedValue, formatErr = formatOptionValue(fieldName, v, cache)
		} else {
			formattedValue, formatErr = formatStringValue(v)
		}

	default:
		api.Log.Warn("Unsupported field value type, skipping field",
			"field_name", fieldName,
			"record", label,
			"value_type", fmt.Sprintf("%T", fieldValue))
		return nil, nil
	}

	if formatErr != nil {
		api.Log.Warn("Failed to format field value, skipping field",
			"field_name", fieldName,
			"record", label,
			"error", formatErr.Error())
		return nil, nil
	}

	propertyValue := &model.PropertyValue{
		GroupID:    groupID,
		TargetType: targetType,
		TargetID:   targetID,
		FieldID:    fieldID,
		Value:      formattedValue,
	}

	return propertyValue, nil
}

// buildPropertyValues creates PropertyValue objects for a single record's attributes.
func buildPropertyValues(api *pluginapi.Client, targetType, targetID string, identityKeys []string, label, groupID string, attrs map[string]interface{}, cache *FieldIDCache) ([]*model.PropertyValue, error) {
	values := make([]*model.PropertyValue, 0, len(attrs))

	for fieldName, fieldValue := range attrs {
		propertyValue, err := buildPropertyValue(api, targetType, targetID, identityKeys, label, groupID, fieldName, fieldValue, cache)
		if err != nil {
			return nil, err
		}
		if propertyValue != nil {
			values = append(values, propertyValue)
		}
	}

	return values, nil
}

//nolint:revive
func SyncUsers(api *pluginapi.Client, groupID string, users []map[string]interface{}, cache *FieldIDCache, summary *Summary) error {
	for _, userAttrs := range users {
		email, ok := userAttrs["email"].(string)
		if !ok || email == "" {
			api.Log.Warn("User object missing email field, skipping")
			summary.UsersSkipped++
			continue
		}

		user, err := api.User.GetByEmail(email)
		if err != nil {
			api.Log.Warn("User not found by email, skipping",
				"email", email,
				"error", err.Error())
			summary.UsersSkipped++
			continue
		}

		values, err := buildPropertyValues(api, model.PropertyValueTargetTypeUser, user.Id, []string{"email"}, email, groupID, userAttrs, cache)
		if err != nil {
			api.Log.Error("Failed to build property values, skipping user",
				"user_email", email,
				"error", err.Error())
			summary.UsersSkipped++
			continue
		}

		if len(values) == 0 {
			api.Log.Debug("No property values to sync for user", "email", email)
			summary.UsersSkipped++
			continue
		}

		_, err = api.Property.UpsertPropertyValues(values)
		if err != nil {
			api.Log.Error("Failed to upsert property values, skipping user",
				"user_email", email,
				"value_count", len(values),
				"error", err.Error())
			summary.UsersSkipped++
			continue
		}

		summary.UsersSynced++
		api.Log.Debug("Successfully synced user attributes",
			"email", email,
			"attribute_count", len(values))
	}

	return nil
}

//nolint:revive
func SyncChannels(api *pluginapi.Client, groupID string, channels []map[string]interface{}, cache *FieldIDCache, summary *Summary) error {
	for _, channelAttrs := range channels {
		teamName, teamOk := channelAttrs["team"].(string)
		channelName, channelOk := channelAttrs["channel"].(string)
		if !teamOk || teamName == "" || !channelOk || channelName == "" {
			api.Log.Warn("Channel object missing team or channel field, skipping")
			summary.ChannelsSkipped++
			continue
		}
		label := teamName + "/" + channelName

		channel, err := api.Channel.GetByNameForTeamName(teamName, channelName, false)
		if err != nil {
			api.Log.Warn("Channel not found by team and channel name, skipping",
				"team", teamName,
				"channel", channelName,
				"error", err.Error())
			summary.ChannelsSkipped++
			continue
		}

		values, err := buildPropertyValues(api, model.PropertyValueTargetTypeChannel, channel.Id, []string{"team", "channel"}, label, groupID, channelAttrs, cache)
		if err != nil {
			api.Log.Error("Failed to build property values, skipping channel",
				"channel", label,
				"error", err.Error())
			summary.ChannelsSkipped++
			continue
		}

		if len(values) == 0 {
			api.Log.Debug("No property values to sync for channel", "channel", label)
			summary.ChannelsSkipped++
			continue
		}

		_, err = api.Property.UpsertPropertyValues(values)
		if err != nil {
			api.Log.Error("Failed to upsert property values, skipping channel",
				"channel", label,
				"value_count", len(values),
				"error", err.Error())
			summary.ChannelsSkipped++
			continue
		}

		summary.ChannelsSynced++
		api.Log.Debug("Successfully synced channel attributes",
			"channel", label,
			"attribute_count", len(values))
	}

	return nil
}
