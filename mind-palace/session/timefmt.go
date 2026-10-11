package session

import (
	"reflect"
	"strings"
	"time"
)

// timestampLayout is RFC 3339 in UTC with exactly three fractional digits, the
// form the session tools have always printed.
const timestampLayout = "2006-01-02T15:04:05.000Z"

var timeType = reflect.TypeOf(time.Time{})

// JSONTimes returns v as plain maps, slices and scalars with every time.Time
// replaced by its timestampLayout string, ready for JSON encoding. Structs
// become maps keyed by their json tag names; nil maps, slices and pointers
// stay nil so they still encode as null.
func JSONTimes(v any) any {
	return jsonTimes(reflect.ValueOf(v))
}

func jsonTimes(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return jsonTimes(v.Elem())
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			out[it.Key().String()] = jsonTimes(it.Value())
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = jsonTimes(v.Index(i))
		}
		return out
	case reflect.Struct:
		if v.Type() == timeType {
			return v.Interface().(time.Time).UTC().Format(timestampLayout)
		}
		return structTimes(v)
	}
	return v.Interface()
}

func structTimes(v reflect.Value) map[string]any {
	out := map[string]any{}
	for i := range v.NumField() {
		field := v.Type().Field(i)
		name, opts, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if strings.Contains(opts, "omitempty") && v.Field(i).IsZero() {
			continue
		}
		out[name] = jsonTimes(v.Field(i))
	}
	return out
}
