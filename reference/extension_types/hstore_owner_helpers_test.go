package reference_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Mutate only the selected role in the actual emitted ownership record.
func hstoreInvalidOwnerDump(t *testing.T, dump []byte, kind string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	script := string(dump)
	start, end, data := fixture.CatalogExtensionOwnerDirective(t, script, "hstore", true)
	var err error
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if string(record["name"]) != `"hstore"` {
		t.Fatal("wrong actual owner record", string(data))
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil || len(members) != 67 {
		t.Fatalf("actual bounded owner inventory %s/%v", data, err)
	}
	bySlot := make(map[int]map[string]json.RawMessage)
	ordinary := 0
	for _, member := range members {
		if member["declaration"] == nil {
			ordinary++
			if string(member["kind"]) != `"type"` || string(member["name"]) != `"_hstore"` || string(member["extension"]) != `"hstore"` || string(member["owner"]) != `""` {
				t.Fatal("generated array membership metadata", member)
			}
			continue
		}
		var slot int
		if err := json.Unmarshal(member["declaration"], &slot); err != nil || bySlot[slot] != nil {
			t.Fatal("invalid actual slot", slot, err)
		}
		bySlot[slot] = member
	}
	if ordinary != 1 {
		t.Fatal("generated companion membership count", ordinary)
	}
	if bySlot[11] == nil || string(bySlot[11]["kind"]) != `"cast"` || string(bySlot[11]["owner"]) != `""` || bySlot[11]["acl"] != nil {
		t.Fatal("ownerless cast membership metadata", bySlot[11])
	}
	var schema, typeName string
	if err := json.Unmarshal(bySlot[0]["schema"], &schema); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bySlot[0]["name"], &typeName); err != nil {
		t.Fatal(err)
	}
	portable := map[string]string{"hstore": schema + "." + typeName, "2275": "pg_catalog.cstring", "25": "pg_catalog.text", "1009": "pg_catalog.text[]", "20": "bigint", "2249": "pg_catalog.record", "2283": "pg_catalog.anyelement", "0": "NONE"}
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
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore,20"},
		{"operator", "hstore,hstore"},
		{"operator", "hstore,hstore"},
		{"operator", "hstore,hstore"},
		{"operator", "hstore,hstore"},
		{"operator", "hstore,hstore"},
		{"operator", "hstore,hstore"},
		{"operator_family", "btree"},
		{"operator_class", "btree"},
		{"operator_family", "hash"},
		{"operator_class", "hash"},
		{"procedure", "25,25"},
		{"procedure", "1009,1009"},
		{"procedure", "1009"},
		{"procedure", "hstore,25"},
		{"procedure", "hstore,1009"},
		{"procedure", "hstore,25"},
		{"procedure", "hstore,25"},
		{"operator", "hstore,25"},
		{"operator", "hstore,1009"},
		{"procedure", "hstore,1009"},
		{"procedure", "hstore,1009"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{"operator", "hstore,25"},
		{"operator", "hstore,1009"},
		{"operator", "hstore,1009"},
		{"operator", "hstore,hstore"},
		{"operator", "hstore,hstore"},
		{"procedure", "2249"},
		{"procedure", "2283,hstore"},
		{"operator", "2283,hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,25"},
		{"procedure", "hstore,1009"},
		{"procedure", "hstore,hstore"},
		{"procedure", "hstore,1009"},
		{"operator", "hstore,hstore"},
		{"operator", "hstore,25"},
		{"operator", "hstore,1009"},
		{"operator", "hstore,hstore"},
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
		if want.kind == "procedure" || want.kind == "operator" {
			parts := strings.Split(want.signature, ",")
			for i, part := range parts {
				spelling, ok := portable[part]
				if !ok {
					t.Fatal("missing independent symbolic formal", part)
				}
				parts[i] = spelling
			}
			want.signature = strings.Join(parts, ", ")
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
		bySlot[8]["declaration"] = json.RawMessage(`3`)
	case "json_signature":
		bySlot[5]["signature"] = json.RawMessage(`"25"`)
	case "json_loose_kind":
		bySlot[6]["kind"] = json.RawMessage(`"type"`)
	case "jsonb_signature":
		bySlot[9]["signature"] = json.RawMessage(`"25"`)
	case "jsonb_loose_kind":
		bySlot[10]["kind"] = json.RawMessage(`"type"`)
	case "jsonb_duplicate":
		bySlot[10]["declaration"] = json.RawMessage(`9`)
	case "jsonb_missing":
		for i, member := range members {
			var slot int
			if member["declaration"] == nil {
				continue
			}
			if err := json.Unmarshal(member["declaration"], &slot); err != nil {
				t.Fatal(err)
			}
			if slot == 10 {
				members = append(members[:i], members[i+1:]...)
				break
			}
		}
	case "jsonb_cast_owner":
		bySlot[11]["owner"] = bySlot[0]["owner"]
	default:
		mutated := false
		for slot := 31; slot <= 65; slot++ {
			if kind == fmt.Sprintf("signature_%d", slot) {
				bySlot[slot]["signature"] = json.RawMessage(`"25"`)
				mutated = true
			}
			if kind == fmt.Sprintf("kind_%d", slot) {
				bySlot[slot]["kind"] = json.RawMessage(`"type"`)
				mutated = true
			}
		}
		if !mutated {
			t.Fatal("unknown owner mutation", kind)
		}
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
