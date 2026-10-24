package reference_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Mutate one actual emitted record, preserving all genuine SQL and unrelated
// fields. A missing member is not the same as removing an optional comment.
func isbnInvalidOwnerDump(t *testing.T, script, kind string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v2 "
	if strings.Count(script, marker) != 1 {
		t.Fatal("expected one actual isn owner record")
	}
	start := strings.Index(script, marker)
	endOffset := strings.IndexByte(script[start:], '\n')
	if endOffset < 0 {
		t.Fatal("unterminated actual isn owner record")
	}
	end := start + endOffset
	data, err := base64.StdEncoding.DecodeString(script[start+len(marker) : end])
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(record["name"], &name); err != nil || name != "isn" {
		t.Fatal("actual owner record is not isn", name, err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil || len(members) != 6 {
		t.Fatal("actual isn owner inventory must contain six members", members, err)
	}
	bySlot := make(map[int]map[string]json.RawMessage)
	for _, member := range members {
		var slot int
		if err := json.Unmarshal(member["slot"], &slot); err != nil || bySlot[slot] != nil {
			t.Fatal("invalid actual member slot", slot, err)
		}
		bySlot[slot] = member
	}
	for slot := range 6 {
		if bySlot[slot] == nil {
			t.Fatal("missing actual manifest slot", slot)
		}
	}
	switch kind {
	case "missing_member":
		for i, member := range members {
			if string(member["slot"]) == "5" {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "duplicate_slot":
		bySlot[5]["slot"] = json.RawMessage(`2`)
	case "wrong_type_role":
		bySlot[3]["name"] = json.RawMessage(`"ean13"`)
	case "wrong_output_signature":
		bySlot[5]["signature"] = json.RawMessage(`"ean13"`)
	case "wrong_version":
		record["version"] = json.RawMessage(`"0.0"`)
	default:
		t.Fatal("unknown owner mutation", kind)
	}
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	// Place the invalid directive after the real extension/consumer/data work,
	// so refusal also proves the entire staged replacement is unpublished.
	return script[:start] + script[end+1:] + marker + base64.StdEncoding.EncodeToString(data) + "\n"
}
