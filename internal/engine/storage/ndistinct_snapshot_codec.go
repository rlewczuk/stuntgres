package storage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

func EncodeNDistinctSnapshot(record NDistinctSnapshot) (string, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return "-- " + NDistinctSnapshotMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}

// validateSnapshotJSON rejects duplicate keys and NULL at every nesting level,
// including nested usage records; the typed decoder rejects unknown fields.
func validateSnapshotJSON(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("null field")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate key")
			}
			seen[s] = true
			if err := validateSnapshotJSON(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := validateSnapshotJSON(d); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid JSON")
	}
	_, err = d.Token()
	return err
}

// DecodeNDistinctSnapshot is called only for genuine line comments. A marker
// in SQL data or a block comment cannot grant native import authority.
func DecodeNDistinctSnapshot(comment string) (NDistinctSnapshot, bool, error) {
	var record NDistinctSnapshot
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-ndistinct-") {
		return record, false, nil
	}
	invalid := func() (NDistinctSnapshot, bool, error) {
		return record, true, fmt.Errorf("stuntgres: invalid ndistinct snapshot")
	}
	marker, payload, ok := strings.Cut(text, " ")
	if !ok || marker != NDistinctSnapshotMarker {
		return invalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || !utf8.Valid(data) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := validateSnapshotJSON(decoder); err != nil {
		return invalid()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid()
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return invalid()
	}
	// Every field is mandatory, including false/zero-valued fields. Nested
	// usages have the same rule; NULL was already rejected recursively.
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(data, &actual); err != nil || len(actual) != 10 {
		return invalid()
	}
	var usages []map[string]json.RawMessage
	if err := json.Unmarshal(actual["usages"], &usages); err != nil {
		return invalid()
	}
	for _, usage := range usages {
		if len(usage) != 5 {
			return invalid()
		}
	}
	if err := record.Validate(); err != nil {
		return invalid()
	}
	return record, true, nil
}
