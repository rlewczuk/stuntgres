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
	if err := json.Unmarshal(record["members"], &members); err != nil || len(members) != 30 {
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
	// Slot 11 is the ownerless hstore-to-jsonb cast, not an exported owner.
	if bySlot[11] != nil {
		t.Fatal("ownerless cast has an owner record")
	}
	for slot, want := range []struct{ kind, signature string }{
		{"type", ""},
		{"procedure", "2275"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{"operator", "0,hstore"},
		{"operator", "0,hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{}, // Ownerless cast.
		{"procedure", "hstore,hstore"}, // eq
		{"procedure", "hstore,hstore"}, // ne
		{"procedure", "hstore,hstore"}, // lt
		{"procedure", "hstore,hstore"}, // le
		{"procedure", "hstore,hstore"}, // gt
		{"procedure", "hstore,hstore"}, // ge
		{"procedure", "hstore,hstore"}, // cmp
		{"procedure", "hstore"},        // hash
		{"procedure", "hstore,20"},     // hash_extended
		{"operator", "hstore,hstore"}, // =
		{"operator", "hstore,hstore"}, // <>
		{"operator", "hstore,hstore"}, // #<#
		{"operator", "hstore,hstore"}, // #<=#
		{"operator", "hstore,hstore"}, // #>#
		{"operator", "hstore,hstore"}, // #>=#
		{"operator_family", "btree"},
		{"operator_class", "btree"},
		{"operator_family", "hash"},
		{"operator_class", "hash"},
	} {
		if slot == 11 {
			continue
		}
		member := bySlot[slot]
		var kind, signature string
		if member == nil {
			t.Fatal("missing actual owner role", slot)
		}
		if err := json.Unmarshal(member["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		if raw := member["signature"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &signature); err != nil {
				t.Fatal(err)
			}
		}
		if kind != want.kind || signature != want.signature {
			t.Fatalf("owner role %d=%s/%s want %s/%s", slot, kind, signature, want.kind, want.signature)
		}
	}
	switch kind {
	case "owner_type":
		bySlot[0]["name"] = json.RawMessage(`"query_int"`)
	case "owner_signature":
		bySlot[2]["signature"] = json.RawMessage(`"65000"`)
	case "owner_unknown":
		bySlot[1]["kind"] = json.RawMessage(`"unknown"`)
	case "array_signature":
		bySlot[3]["signature"] = json.RawMessage(`"25"`)
	case "matrix_kind":
		bySlot[4]["kind"] = json.RawMessage(`"type"`)
	case "operator_signature":
		bySlot[7]["signature"] = json.RawMessage(`"hstore,0"`)
	case "operator_slot":
		bySlot[8]["slot"] = json.RawMessage(`3`)
	case "json_signature":
		bySlot[5]["signature"] = json.RawMessage(`"25"`)
	case "json_loose_kind":
		bySlot[6]["kind"] = json.RawMessage(`"type"`)
	case "jsonb_signature":
		bySlot[9]["signature"] = json.RawMessage(`"25"`)
	case "jsonb_loose_kind":
		bySlot[10]["kind"] = json.RawMessage(`"type"`)
	case "jsonb_duplicate":
		bySlot[10]["slot"] = json.RawMessage(`9`)
	case "jsonb_missing":
		for i, member := range members {
			var slot int
			if err := json.Unmarshal(member["slot"], &slot); err != nil {
				t.Fatal(err)
			}
			if slot == 10 {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "jsonb_cast_owner":
		members = append(members, map[string]json.RawMessage{"slot": json.RawMessage(`11`), "kind": json.RawMessage(`"cast"`), "name": json.RawMessage(`"hstore_jsonb"`)})
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
