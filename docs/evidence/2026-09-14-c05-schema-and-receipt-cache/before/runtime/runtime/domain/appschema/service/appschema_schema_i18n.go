package service

import (
	"context"
	"fmt"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func (s *ApplicationSchemaDomainService) ForPrincipalLocale(ctx context.Context, principal principalmodel.Principal, locale string) appschemamodel.ApplicationSchemaSnapshot {
	snapshot := s.schema.SchemaForPrincipal(ctx, principal)
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return snapshot
	}
	if s.metadata == nil {
		return snapshot
	}
	values, err := s.metadata.List(ctx, metadatasdk.LocalizedTextQuery{WorkspaceID: principal.WorkspaceID, Locale: locale})
	if err != nil || len(values) == 0 {
		return snapshot
	}
	lookup := localizedTextLookup(values)
	localize := func(entityType string, entityKey string, property string, fallback string) string {
		if value := lookup[localizedTextLookupKey(entityType, entityKey, property)]; value != "" {
			return value
		}
		return fallback
	}
	snapshot.Name = localize("app", "app", "name", snapshot.Name)
	for objectIndex := range snapshot.Objects {
		object := &snapshot.Objects[objectIndex]
		object.Name = localize("object", object.Key, "name", object.Name)
		object.Description = localize("object", object.Key, "description", object.Description)
		for fieldIndex := range object.Fields {
			field := &object.Fields[fieldIndex]
			fieldKey := strings.Trim(strings.TrimSpace(object.Key)+"."+strings.TrimSpace(field.Key), ".")
			field.Name = localize("field", fieldKey, "name", field.Name)
			field.Options = localizeSchemaValueOptions(field.Options, lookup, "field_option", fieldKey)
		}
		for validationIndex := range object.Validations {
			validation := &object.Validations[validationIndex]
			key := strings.TrimSpace(validation.Key)
			if key == "" {
				key = strings.Trim(strings.Join([]string{object.Key, validation.Type, validation.FieldKey, strings.Join(validation.Fields, "_")}, "."), ".")
			}
			validation.Message = localize("validation", key, "message", validation.Message)
		}
	}
	for index := range snapshot.Actions {
		action := &snapshot.Actions[index]
		action.Label = localize("action", action.Key, "label", action.Label)
		for fieldIndex := range action.PayloadFields {
			field := &action.PayloadFields[fieldIndex]
			field.Name = localize("action_payload_field", strings.Trim(action.Key+"."+field.Key, "."), "name", field.Name)
		}
	}
	for index := range snapshot.GuardedWrites {
		contract := &snapshot.GuardedWrites[index]
		contract.Label = localize("action", contract.ActionKey, "label", contract.Label)
	}
	for index := range snapshot.Workflows {
		workflow := &snapshot.Workflows[index]
		workflow.Name = localize("workflow", workflow.Key, "name", workflow.Name)
	}
	for index := range snapshot.Dictionaries {
		dictionary := &snapshot.Dictionaries[index]
		dictionary.Name = localize("dictionary", dictionary.Key, "name", dictionary.Name)
		dictionary.Description = localize("dictionary", dictionary.Key, "description", dictionary.Description)
		for itemIndex := range dictionary.Items {
			item := &dictionary.Items[itemIndex]
			itemKey := strings.Trim(dictionary.Key+"."+firstNonEmptySchemaI18n(item.Key, item.Value), ".")
			item.Label = localize("dictionary_item", itemKey, "label", item.Label)
			item.Description = localize("dictionary_item", itemKey, "description", item.Description)
		}
	}
	for index := range snapshot.Reports {
		report := &snapshot.Reports[index]
		report.Name = localize("report", report.Key, "name", report.Name)
	}
	for index := range snapshot.Skills {
		skill := &snapshot.Skills[index]
		skill.Name = localize("skill", skill.Key, "name", skill.Name)
		skill.Description = localize("skill", skill.Key, "description", skill.Description)
	}
	for index := range snapshot.Agents {
		agent := &snapshot.Agents[index]
		agent.Name = localize("agent", agent.Key, "name", agent.Name)
		agent.Description = localize("agent", agent.Key, "description", agent.Description)
	}
	snapshot.SchemaHash = snapshot.SchemaHash + ":" + locale
	return snapshot
}

func localizedTextLookup(values []metadatasdk.LocalizedText) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		out[localizedTextLookupKey(value.EntityType, value.EntityKey, value.Property)] = value.Text
	}
	return out
}

func localizedTextLookupKey(entityType string, entityKey string, property string) string {
	return strings.TrimSpace(entityType) + "\x00" + strings.TrimSpace(entityKey) + "\x00" + strings.TrimSpace(property)
}

func localizeSchemaValueOptions(value any, lookup map[string]string, entityType string, parentKey string) any {
	switch typed := value.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for _, option := range typed {
			out = append(out, localizeSchemaValueOption(option, lookup, entityType, parentKey))
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		changed := false
		for _, item := range typed {
			option, ok := item.(map[string]any)
			if !ok {
				out = append(out, item)
				continue
			}
			out = append(out, localizeSchemaValueOption(option, lookup, entityType, parentKey))
			changed = true
		}
		if changed {
			return out
		}
	}
	return value
}

func localizeSchemaValueOption(option map[string]any, lookup map[string]string, entityType string, parentKey string) map[string]any {
	out := make(map[string]any, len(option))
	for key, value := range option {
		out[key] = value
	}
	optionKey := strings.TrimSpace(fmt.Sprint(out["value"]))
	if optionKey == "" || optionKey == "<nil>" {
		optionKey = strings.TrimSpace(fmt.Sprint(out["key"]))
	}
	entityKey := strings.Trim(strings.TrimSpace(parentKey)+"."+optionKey, ".")
	if label := lookup[localizedTextLookupKey(entityType, entityKey, "label")]; label != "" {
		out["label"] = label
	}
	if description := lookup[localizedTextLookupKey(entityType, entityKey, "description")]; description != "" {
		out["description"] = description
	}
	return out
}

func firstNonEmptySchemaI18n(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
