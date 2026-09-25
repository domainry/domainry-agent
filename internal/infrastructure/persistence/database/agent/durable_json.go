package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var durableJSONTimeType = reflect.TypeOf(time.Time{})
var durableJSONRawMessageType = reflect.TypeOf(json.RawMessage{})
var durableJSONUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// marshalDurableJSON keeps Go/domain time.Time values while ensuring their
// durable JSON representation is always a UTC Unix-millisecond number.
func marshalDurableJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	document, err := decodeDurableJSONDocument(raw)
	if err != nil {
		return nil, err
	}
	document = encodeDurableJSONTimes(reflect.ValueOf(value), document)
	return json.Marshal(document)
}

// unmarshalDurableJSON is intentionally strict: time.Time destinations accept
// only the numeric millisecond form produced by marshalDurableJSON.
func unmarshalDurableJSON(raw []byte, destination any) error {
	if destination == nil || reflect.TypeOf(destination).Kind() != reflect.Pointer {
		return fmt.Errorf("durable JSON destination must be a pointer")
	}
	normalized, err := normalizeDurableJSONRaw(reflect.TypeOf(destination).Elem(), json.RawMessage(raw))
	if err != nil {
		return err
	}
	return json.Unmarshal(normalized, destination)
}

func decodeDurableJSONDocument(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	return document, nil
}

func encodeDurableJSONTimes(value reflect.Value, document any) any {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return document
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return document
	}
	if value.Type() == durableJSONRawMessageType {
		return value.Interface().(json.RawMessage)
	}
	if value.Type() == durableJSONTimeType {
		instant := value.Interface().(time.Time)
		if instant.IsZero() {
			return int64(0)
		}
		return instant.UTC().UnixMilli()
	}
	switch value.Kind() {
	case reflect.Struct:
		object, ok := document.(map[string]any)
		if !ok {
			return document
		}
		typeValue := value.Type()
		for index := 0; index < value.NumField(); index++ {
			fieldType := typeValue.Field(index)
			if fieldType.PkgPath != "" {
				continue
			}
			if fieldType.Anonymous && fieldType.Tag.Get("json") == "" {
				if normalized, ok := encodeDurableJSONTimes(value.Field(index), object).(map[string]any); ok {
					object = normalized
				}
				continue
			}
			name, included := durableJSONFieldName(fieldType)
			if !included {
				continue
			}
			if child, exists := object[name]; exists {
				object[name] = encodeDurableJSONTimes(value.Field(index), child)
			}
		}
		return object
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return document
		}
		items, ok := document.([]any)
		if !ok {
			return document
		}
		for index := 0; index < value.Len() && index < len(items); index++ {
			items[index] = encodeDurableJSONTimes(value.Index(index), items[index])
		}
		return items
	case reflect.Map:
		object, ok := document.(map[string]any)
		if !ok || value.Type().Key().Kind() != reflect.String {
			return document
		}
		iterator := value.MapRange()
		for iterator.Next() {
			key := iterator.Key().String()
			if child, exists := object[key]; exists {
				object[key] = encodeDurableJSONTimes(iterator.Value(), child)
			}
		}
		return object
	default:
		return document
	}
}

func normalizeDurableJSONRaw(target reflect.Type, raw json.RawMessage) (json.RawMessage, error) {
	for target.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return raw, nil
		}
		target = target.Elem()
	}
	if target == durableJSONRawMessageType {
		return raw, nil
	}
	if target == durableJSONTimeType {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		number, ok := value.(json.Number)
		if !ok {
			return nil, fmt.Errorf("durable time must be a Unix-millisecond number")
		}
		millis, err := number.Int64()
		if err != nil {
			return nil, fmt.Errorf("durable time must be an integer: %w", err)
		}
		if millis == 0 {
			return json.RawMessage(`"0001-01-01T00:00:00Z"`), nil
		}
		return json.Marshal(time.UnixMilli(millis).UTC().Format(time.RFC3339Nano))
	}
	if reflect.PointerTo(target).Implements(durableJSONUnmarshalerType) {
		return raw, nil
	}
	switch target.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return raw, nil
		}
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			if field.PkgPath != "" {
				continue
			}
			if field.Anonymous && field.Tag.Get("json") == "" {
				embedded, err := normalizeDurableJSONRaw(field.Type, raw)
				if err != nil {
					return nil, err
				}
				var updated map[string]json.RawMessage
				if json.Unmarshal(embedded, &updated) == nil {
					object = updated
				}
				continue
			}
			name, included := durableJSONFieldName(field)
			if !included {
				continue
			}
			child, exists := object[name]
			if !exists {
				continue
			}
			normalized, err := normalizeDurableJSONRaw(field.Type, child)
			if err != nil {
				return nil, fmt.Errorf("decode durable JSON field %s: %w", name, err)
			}
			object[name] = normalized
		}
		return json.Marshal(object)
	case reflect.Slice, reflect.Array:
		if target.Elem().Kind() == reflect.Uint8 {
			return raw, nil
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return raw, nil
		}
		for index := range items {
			normalized, err := normalizeDurableJSONRaw(target.Elem(), items[index])
			if err != nil {
				return nil, err
			}
			items[index] = normalized
		}
		return json.Marshal(items)
	case reflect.Map:
		if target.Key().Kind() != reflect.String {
			return raw, nil
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return raw, nil
		}
		for key, child := range object {
			normalized, err := normalizeDurableJSONRaw(target.Elem(), child)
			if err != nil {
				return nil, err
			}
			object[key] = normalized
		}
		return json.Marshal(object)
	default:
		return raw, nil
	}
}

func decodeDurableJSONTimes(target reflect.Type, document any) (any, error) {
	for target.Kind() == reflect.Pointer {
		if document == nil {
			return nil, nil
		}
		target = target.Elem()
	}
	if target == durableJSONTimeType {
		number, ok := document.(json.Number)
		if !ok {
			return nil, fmt.Errorf("durable time must be a Unix-millisecond number")
		}
		millis, err := number.Int64()
		if err != nil {
			return nil, fmt.Errorf("durable time must be an integer: %w", err)
		}
		if millis == 0 {
			return "0001-01-01T00:00:00Z", nil
		}
		return time.UnixMilli(millis).UTC().Format(time.RFC3339Nano), nil
	}
	switch target.Kind() {
	case reflect.Struct:
		object, ok := document.(map[string]any)
		if !ok {
			return document, nil
		}
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			if field.PkgPath != "" {
				continue
			}
			if field.Anonymous && field.Tag.Get("json") == "" {
				normalized, err := decodeDurableJSONTimes(field.Type, object)
				if err != nil {
					return nil, err
				}
				if updated, ok := normalized.(map[string]any); ok {
					object = updated
				}
				continue
			}
			name, included := durableJSONFieldName(field)
			if !included {
				continue
			}
			child, exists := object[name]
			if !exists {
				continue
			}
			normalized, err := decodeDurableJSONTimes(field.Type, child)
			if err != nil {
				return nil, fmt.Errorf("decode durable JSON field %s: %w", name, err)
			}
			object[name] = normalized
		}
		return object, nil
	case reflect.Slice, reflect.Array:
		if target.Elem().Kind() == reflect.Uint8 {
			return document, nil
		}
		items, ok := document.([]any)
		if !ok {
			return document, nil
		}
		for index := range items {
			normalized, err := decodeDurableJSONTimes(target.Elem(), items[index])
			if err != nil {
				return nil, err
			}
			items[index] = normalized
		}
		return items, nil
	case reflect.Map:
		object, ok := document.(map[string]any)
		if !ok || target.Key().Kind() != reflect.String {
			return document, nil
		}
		for key, child := range object {
			normalized, err := decodeDurableJSONTimes(target.Elem(), child)
			if err != nil {
				return nil, err
			}
			object[key] = normalized
		}
		return object, nil
	default:
		return document, nil
	}
}

func durableJSONFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		name = field.Name
	}
	return name, true
}
