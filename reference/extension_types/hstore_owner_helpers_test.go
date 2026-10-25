package reference_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Mutate only the selected role in the actual emitted ownership record.
func hstoreInvalidOwnerDump(t *testing.T, dump []byte, kind string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v2 "
	script := string(dump)
	if strings.Count(script, marker) != 1 {
		t.Fatal("expected one actual hstore owner record")
	}
	start := strings.Index(script, marker)
	endOffset := strings.IndexByte(script[start:], '\n')
	if endOffset < 0 {
		t.Fatal("unterminated actual owner record")
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
	if string(record["name"]) != `"hstore"` {
		t.Fatal("wrong actual owner record", string(data))
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil || len(members) != 3 {
		t.Fatalf("actual bounded owner inventory %s/%v", data, err)
	}
	bySlot := make(map[int]map[string]json.RawMessage)
	for _, member := range members {
		var slot int
		if err := json.Unmarshal(member["slot"], &slot); err != nil || bySlot[slot] != nil {
			t.Fatal("invalid actual slot", slot, err)
		}
		bySlot[slot] = member
	}
	if bySlot[0] == nil || bySlot[1] == nil || bySlot[2] == nil {
		t.Fatal("missing actual owner role")
	}
	switch kind {
	case "owner_type":
		bySlot[0]["name"] = json.RawMessage(`"query_int"`)
	case "owner_signature":
		bySlot[2]["signature"] = json.RawMessage(`"65000"`)
	case "owner_unknown":
		bySlot[1]["kind"] = json.RawMessage(`"unknown"`)
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
	return script[:start] + marker + base64.StdEncoding.EncodeToString(data) + script[end:]
}
