package main

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"time"
)

// Validate the token stream before typed decoding: encoding/json otherwise
// accepts duplicate keys and case-insensitive field aliases with last wins.
// Exact tags are taken from this format's types, including nested records.
func usageEvidenceFields(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(reflect.Type, int) bool
	value = func(typ reflect.Type, depth int) bool {
		if depth > 16 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		if token == nil {
			return typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice
		}
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ == reflect.TypeOf(time.Time{}) {
			_, ok := token.(string)
			return ok
		}
		switch typ.Kind() {
		case reflect.Struct:
			if token != json.Delim('{') {
				return false
			}
			fields := map[string]reflect.Type{}
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f.Type
			}
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				key, ok := k.(string)
				field, known := fields[key]
				if err != nil || !ok || !known || seen[key] {
					return false
				}
				seen[key] = true
				if !value(field, depth+1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case reflect.Slice:
			if token != json.Delim('[') {
				return false
			}
			for d.More() {
				if !value(typ.Elem(), depth+1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		case reflect.String:
			_, ok := token.(string)
			return ok
		case reflect.Bool:
			_, ok := token.(bool)
			return ok
		case reflect.Int, reflect.Int64:
			_, ok := token.(json.Number)
			return ok
		}
		return false
	}
	if !value(reflect.TypeOf(usageExport{}), 0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
