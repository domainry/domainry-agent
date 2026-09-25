package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

var durableJSONTimeType = reflect.TypeOf(time.Time{})
var durableJSONRawMessageType = reflect.TypeOf(json.RawMessage{})
var durableJSONUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
var durableJSONMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
var durableJSONEncodeTimeCache sync.Map
var durableJSONDecodeTimeCache sync.Map

// marshalDurableJSON keeps Go/domain time.Time values while ensuring their
// durable JSON representation is always a UTC Unix-millisecond number.
func marshalDurableJSON(value any) ([]byte, error) {
	if !durableJSONNeedsTime(reflect.TypeOf(value), true) {
		return json.Marshal(value)
	}
	return json.Marshal(encodeDurableJSONValue(reflect.ValueOf(value)))
}

// unmarshalDurableJSON is intentionally strict: time.Time destinations accept
// only the numeric millisecond form produced by marshalDurableJSON.
func unmarshalDurableJSON(raw []byte, destination any) error {
	if destination == nil || reflect.TypeOf(destination).Kind() != reflect.Pointer {
		return fmt.Errorf("durable JSON destination must be a pointer")
	}
	target := reflect.TypeOf(destination).Elem()
	if !durableJSONNeedsTime(target, false) {
		return json.Unmarshal(raw, destination)
	}
	normalized, err := normalizeDurableJSONRaw(target, json.RawMessage(raw))
	if err != nil {
		return err
	}
	return json.Unmarshal(normalized, destination)
}

// encodeDurableJSONValue converts only declared Go time.Time values before
// serialization. Raw JSON belongs to its producer and is never inspected.
func encodeDurableJSONValue(value reflect.Value) any {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}
	if !durableJSONNeedsTime(value.Type(), true) {
		return value.Interface()
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
	if value.Type().Implements(durableJSONMarshalerType) {
		return value.Interface()
	}
	switch value.Kind() {
	case reflect.Struct:
		return encodeDurableJSONStruct(value)
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return value.Interface()
		}
		if value.Kind() == reflect.Slice && value.IsNil() {
			return nil
		}
		items := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			items[index] = encodeDurableJSONValue(value.Index(index))
		}
		return items
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return value.Interface()
		}
		if value.IsNil() {
			return nil
		}
		object := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			object[iterator.Key().String()] = encodeDurableJSONValue(iterator.Value())
		}
		return object
	default:
		return value.Interface()
	}
}

func encodeDurableJSONStruct(value reflect.Value) map[string]any {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil
	}
	object := make(map[string]any)
	typeValue := value.Type()
	for index := 0; index < value.NumField(); index++ {
		fieldType := typeValue.Field(index)
		if fieldType.PkgPath != "" {
			continue
		}
		if fieldType.Anonymous && fieldType.Tag.Get("json") == "" {
			for key, child := range encodeDurableJSONStruct(value.Field(index)) {
				object[key] = child
			}
			continue
		}
		name, included := durableJSONFieldName(fieldType)
		if !included || durableJSONOmitEmpty(fieldType) && durableJSONEmpty(value.Field(index)) {
			continue
		}
		object[name] = encodeDurableJSONValue(value.Field(index))
	}
	return object
}

// Inspect declared Go types, never JSON field names or opaque raw bytes.
// Dynamic interfaces are traversed on write; on read they carry no declared
// time.Time destination and can use the standard decoder unchanged.
func durableJSONNeedsTime(target reflect.Type, forWrite bool) bool {
	if target == nil {
		return false
	}
	cache := &durableJSONDecodeTimeCache
	if forWrite {
		cache = &durableJSONEncodeTimeCache
	}
	if cached, ok := cache.Load(target); ok {
		return cached.(bool)
	}
	result := durableJSONNeedsTimeType(target, forWrite, map[reflect.Type]bool{})
	cache.Store(target, result)
	return result
}

func durableJSONNeedsTimeType(target reflect.Type, forWrite bool, seen map[reflect.Type]bool) bool {
	if target == nil || target == durableJSONRawMessageType {
		return false
	}
	if target == durableJSONTimeType {
		return true
	}
	if target.Kind() == reflect.Pointer {
		return durableJSONNeedsTimeType(target.Elem(), forWrite, seen)
	}
	if forWrite && target.Implements(durableJSONMarshalerType) {
		return false
	}
	if !forWrite && reflect.PointerTo(target).Implements(durableJSONUnmarshalerType) {
		return false
	}
	if seen[target] {
		return true
	}
	seen[target] = true
	defer delete(seen, target)
	switch target.Kind() {
	case reflect.Interface:
		return forWrite
	case reflect.Struct:
		for index := 0; index < target.NumField(); index++ {
			field := target.Field(index)
			if field.PkgPath == "" {
				if _, included := durableJSONFieldName(field); included && durableJSONNeedsTimeType(field.Type, forWrite, seen) {
					return true
				}
			}
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		return durableJSONNeedsTimeType(target.Elem(), forWrite, seen)
	}
	return false
}

func durableJSONOmitEmpty(field reflect.StructField) bool {
	for _, option := range strings.Split(field.Tag.Get("json"), ",")[1:] {
		if option == "omitempty" || option == "omitzero" {
			return true
		}
	}
	return false
}

func durableJSONEmpty(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return value.IsZero()
	default:
		return false
	}
}

func normalizeDurableJSONRaw(target reflect.Type, raw json.RawMessage) (json.RawMessage, error) {
	if !durableJSONNeedsTime(target, false) {
		return raw, nil
	}
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
