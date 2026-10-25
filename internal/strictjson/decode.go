// Package strictjson rejects ambiguous management JSON before changing state.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

func Decode(reader io.Reader, limit int64, target any) error {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return errors.New("management JSON exceeds limit")
	}
	scan := json.NewDecoder(bytes.NewReader(data))
	scan.UseNumber()
	if err = value(scan, 0); err != nil {
		return err
	}
	if _, err = scan.Token(); err != io.EOF {
		return errors.New("trailing management JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return errors.New("invalid management JSON")
	}
	return nil
}
func value(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("management JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return errors.New("invalid management JSON")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return errors.New("invalid management member")
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("invalid management member")
			}
			name = foldName(name)
			if seen[name] {
				return errors.New("duplicate management member")
			}
			seen[name] = true
			if e = value(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return errors.New("invalid management object")
		}
	case '[':
		for d.More() {
			if err = value(d, depth+1); err != nil {
				return err
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return errors.New("invalid management array")
		}
	default:
		return errors.New("unexpected management delimiter")
	}
	return nil
}

// Match encoding/json's Unicode case-insensitive field-name equivalence,
// including long-s and Kelvin-sign aliases, not only ASCII/lowercase spelling.
func foldName(name string) string {
	var out strings.Builder
	for _, r := range name {
		least := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < least {
				least = next
			}
		}
		out.WriteRune(least)
	}
	return out.String()
}
