package reference_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Mutate the existing public dump owner recipe, never the catalog itself.
func uuidNameInvalidSignature(t *testing.T, dump string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	for _, line := range strings.Split(dump, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, marker))
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]json.RawMessage
		if err := json.Unmarshal(payload, &record); err != nil {
			t.Fatal(err)
		}
		var name string
		if err := json.Unmarshal(record["name"], &name); err != nil {
			t.Fatal(err)
		}
		if name != "uuid-ossp" {
			continue
		}
		var members []map[string]json.RawMessage
		if err := json.Unmarshal(record["members"], &members); err != nil {
			t.Fatal(err)
		}
		for _, member := range members {
			if err := json.Unmarshal(member["name"], &name); err != nil {
				t.Fatal(err)
			}
			if name != "uuid_generate_v5" {
				continue
			}
			var signature string
			if err := json.Unmarshal(member["signature"], &signature); err != nil || signature != "pg_catalog.uuid, pg_catalog.text" {
				t.Fatal("name signature", signature, err)
			}
			member["signature"] = json.RawMessage(`"pg_catalog.text, pg_catalog.text"`)
			data, err := json.Marshal(members)
			if err != nil {
				t.Fatal(err)
			}
			record["members"] = data
			data, err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			return strings.Replace(dump, line, marker+base64.StdEncoding.EncodeToString(data), 1)
		}
		t.Fatal("missing v5 owner record")
	}
	t.Fatal("missing uuid-ossp owner manifest")
	return ""
}
