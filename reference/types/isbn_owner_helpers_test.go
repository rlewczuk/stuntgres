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
	if err := json.Unmarshal(record["members"], &members); err != nil || len(members) != 21 {
		t.Fatal("actual isn owner inventory must contain twenty-one members", members, err)
	}
	bySlot := make(map[int]map[string]json.RawMessage)
	for _, member := range members {
		var slot int
		if err := json.Unmarshal(member["slot"], &slot); err != nil || bySlot[slot] != nil {
			t.Fatal("invalid actual member slot", slot, err)
		}
		bySlot[slot] = member
	}
	for slot := range 21 {
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
	case "isbn13_missing_member":
		for i, member := range members {
			if string(member["slot"]) == "8" {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "isbn13_duplicate_slot":
		bySlot[8]["slot"] = json.RawMessage(`5`)
	case "isbn13_wrong_type_role":
		bySlot[6]["name"] = json.RawMessage(`"isbn"`)
	case "isbn13_wrong_output_signature":
		bySlot[8]["signature"] = json.RawMessage(`"isbn"`)
	case "ismn_missing_member":
		for i, member := range members {
			if string(member["slot"]) == "11" {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "ismn_duplicate_slot":
		bySlot[11]["slot"] = json.RawMessage(`8`)
	case "ismn_wrong_type_role":
		bySlot[9]["name"] = json.RawMessage(`"isbn13"`)
	case "ismn_wrong_output_signature":
		bySlot[11]["signature"] = json.RawMessage(`"isbn13"`)
	case "ismn13_missing_member":
		for i, member := range members {
			if string(member["slot"]) == "14" {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "ismn13_duplicate_slot":
		bySlot[14]["slot"] = json.RawMessage(`11`)
	case "ismn13_wrong_type_role":
		bySlot[12]["name"] = json.RawMessage(`"ismn"`)
	case "ismn13_wrong_input_name":
		bySlot[13]["name"] = json.RawMessage(`"ismn_in"`)
	case "ismn13_wrong_output_signature":
		bySlot[14]["signature"] = json.RawMessage(`"ismn"`)
	case "issn_missing_member":
		for i, member := range members {
			if string(member["slot"]) == "17" {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "issn_duplicate_slot":
		bySlot[17]["slot"] = json.RawMessage(`14`)
	case "issn_wrong_type_role":
		bySlot[15]["name"] = json.RawMessage(`"ismn13"`)
	case "issn_wrong_output_signature":
		bySlot[17]["signature"] = json.RawMessage(`"ismn13"`)
	case "issn13_missing_member":
		for i, member := range members {
			if string(member["slot"]) == "20" {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "issn13_duplicate_slot":
		bySlot[20]["slot"] = json.RawMessage(`17`)
	case "issn13_wrong_type_role":
		bySlot[18]["name"] = json.RawMessage(`"issn"`)
	case "issn13_wrong_output_signature":
		bySlot[20]["signature"] = json.RawMessage(`"issn"`)
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
