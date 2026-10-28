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

func EncodeExtendedStatisticsSnapshot(record ExtendedStatisticsSnapshot) (string, error) {
	if err := record.Validate(); err != nil {
		return "", err
	}
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

// DecodeExtendedStatisticsSnapshot only accepts genuine native line comments.
// The retained marker keeps existing column-only dump bytes unchanged.
func DecodeExtendedStatisticsSnapshot(comment string) (ExtendedStatisticsSnapshot, bool, error) {
	var record ExtendedStatisticsSnapshot
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-ndistinct-") {
		return record, false, nil
	}
	invalid := func() (ExtendedStatisticsSnapshot, bool, error) {
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
	if err := json.Unmarshal(data, &actual); err != nil {
		return invalid()
	}
	expectedFields := 10
	if _, exists := actual["expressions"]; exists {
		expectedFields++
	}
	if _, exists := actual["dependencies"]; exists {
		expectedFields++
	}
	if raw, exists := actual["mcv"]; exists {
		expectedFields++
		if err := validateMCVSnapshotJSON(raw); err != nil {
			return invalid()
		}
	}
	if len(actual) != expectedFields {
		return invalid()
	}
	for _, key := range []string{"schema", "name", "relation_schema", "relation_name", "keys", "kinds", "inherited", "usages", "subsets", "counts"} {
		if _, exists := actual[key]; !exists {
			return invalid()
		}
	}
	if raw, exists := actual["dependencies"]; exists {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return invalid()
		}
		for _, item := range items {
			if len(item) != 2 {
				return invalid()
			}
			if _, ok := item["attributes"]; !ok {
				return invalid()
			}
			if _, ok := item["degree"]; !ok {
				return invalid()
			}
		}
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
	if raw, exists := actual["expressions"]; exists {
		var expressions []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &expressions); err != nil || len(expressions) == 0 {
			return invalid()
		}
		for _, expression := range expressions {
			if len(expression) != 11 {
				return invalid()
			}
			var usage map[string]json.RawMessage
			if err := json.Unmarshal(expression["usage"], &usage); err != nil || len(usage) != 5 {
				return invalid()
			}
			var inputs []map[string]json.RawMessage
			if err := json.Unmarshal(expression["input_usages"], &inputs); err != nil {
				return invalid()
			}
			for _, usage := range inputs {
				if len(usage) != 5 {
					return invalid()
				}
			}
			var mcv []map[string]json.RawMessage
			if err := json.Unmarshal(expression["mcv"], &mcv); err != nil {
				return invalid()
			}
			for _, item := range mcv {
				if len(item) != 2 {
					return invalid()
				}
			}
		}
	}
	if err := record.Validate(); err != nil {
		return invalid()
	}
	return record, true, nil
}
