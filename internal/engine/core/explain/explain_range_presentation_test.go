package explain

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestExplainRangePresentation(t *testing.T) {
	// This pure encoder supplement intentionally supplies detached values. The
	// composed owner units independently prove their actual binding provenance.
	permissions := []any{
		map[string]any{"Principal": "001", "Privilege": "SELECT", "Columns": []string{"a&<", "true"}, "Whole Relation": false},
		map[string]any{"Principal": "001", "Privilege": "SELECT", "Columns": []string{}, "Whole Relation": false},
		map[string]any{"Principal": "001", "Privilege": "SELECT", "Columns": []string{}, "Whole Relation": false},
	}
	group := map[string]any{"Scopes": []any{
		map[string]any{"Scope ID": 1, "Kind": "query", "Entries": []any{map[string]any{"Entry ID": 1, "Kind": "relation", "Range Name": "odd alias", "Columns": []string{"001", "a&<", "true"}, "Occurrence": 0, "Relation OID": uint32(42), "Schema": "public", "Relation Name": "a&<", "Relation Kind": "r", "Role": "source", "Required Permissions": permissions}}},
		map[string]any{"Scope ID": 2, "Parent Scope ID": 1, "Kind": "subplan", "Entries": []any{}, "Outer Scope IDs": []any{1}},
	}}
	before, err := json.Marshal(group)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "xml", "yaml"} {
		t.Run(format, func(t *testing.T) {
			document := []map[string]any{{"Plan": map[string]any{"Node Type": "Project"}, "Embedded Range Table": group}}
			encoded, err := encodeStructured(format, document)
			if err != nil {
				t.Fatal(err)
			}
			if format == "xml" {
				var root struct {
					XMLName xml.Name
					Queries []struct {
						Range struct {
							Scopes []struct {
								ID      string   `xml:"Scope-ID"`
								Parent  string   `xml:"Parent-Scope-ID"`
								Outer   []string `xml:"Outer-Scope-IDs>Item"`
								Entries []struct {
									Occurrence  string
									OID         string   `xml:"Relation-OID"`
									Name        string   `xml:"Range-Name"`
									Columns     []string `xml:"Columns>Item"`
									Permissions []struct {
										Principal string
										Whole     string   `xml:"Whole-Relation"`
										Columns   []string `xml:"Columns>Item"`
									} `xml:"Required-Permissions>Item"`
								} `xml:"Entries>Item"`
							} `xml:"Scopes>Item"`
						} `xml:"Embedded-Range-Table"`
					} `xml:"Query"`
				}
				decoder := xml.NewDecoder(bytes.NewReader(encoded))
				if err := decoder.Decode(&root); err != nil {
					t.Fatal(err)
				}
				if _, err := decoder.Token(); err != io.EOF {
					t.Fatal("trailing XML", err)
				}
				if root.XMLName.Space != explainNamespace || len(root.Queries) != 1 {
					t.Fatal("XML namespace/query ownership", string(encoded))
				}
				scopes := root.Queries[0].Range.Scopes
				if len(scopes) != 2 || scopes[0].ID != "1" || scopes[0].Parent != "" || scopes[1].ID != "2" || scopes[1].Parent != "1" || len(scopes[1].Entries) != 0 || !reflect.DeepEqual(scopes[1].Outer, []string{"1"}) {
					t.Fatal("XML scope boundaries", string(encoded))
				}
				e := scopes[0].Entries[0]
				if e.Occurrence != "0" || e.OID != "42" || e.Name != "odd alias" || !reflect.DeepEqual(e.Columns, []string{"001", "a&<", "true"}) || len(e.Permissions) != 3 || e.Permissions[0].Principal != "001" || e.Permissions[0].Whole != "false" || len(e.Permissions[1].Columns) != 0 || len(e.Permissions[2].Columns) != 0 {
					t.Fatal("XML typed/empty/multiplicity contract", string(encoded))
				}
			} else {
				var decoded []map[string]any
				if format == "json" {
					err = json.Unmarshal(encoded, &decoded)
				} else {
					decoder := yaml.NewDecoder(bytes.NewReader(encoded))
					err = decoder.Decode(&decoded)
					var extra any
					if err == nil && decoder.Decode(&extra) != io.EOF {
						t.Fatal("trailing YAML document")
					}
				}
				if err != nil || len(decoded) != 1 {
					t.Fatal(err, string(encoded))
				}
				scopes := decoded[0]["Embedded Range Table"].(map[string]any)["Scopes"].([]any)
				entry := scopes[0].(map[string]any)["Entries"].([]any)[0].(map[string]any)
				if !reflect.DeepEqual(entry["Columns"], []any{"001", "a&<", "true"}) || len(scopes[1].(map[string]any)["Entries"].([]any)) != 0 {
					t.Fatal("structured string/empty list boundaries", decoded)
				}
				if format == "json" {
					if entry["Occurrence"] != float64(0) || entry["Relation OID"] != float64(42) {
						t.Fatal("JSON identities became strings", entry)
					}
				} else if entry["Occurrence"] != 0 || entry["Relation OID"] != 42 {
					t.Fatal("YAML identities lost integer types", entry)
				}
				requests := entry["Required Permissions"].([]any)
				if len(requests) != 3 || requests[0].(map[string]any)["Whole Relation"] != false || !reflect.DeepEqual(requests[1], requests[2]) {
					t.Fatal("permission records merged/coerced", requests)
				}
			}
		})
	}
	var lines []string
	rangeLines(group, "  ", "", &lines)
	text := strings.Join(lines, "\n")
	for _, want := range []string{`Range Name: "odd alias"`, `Relation Name: "a&<"`, `Principal: "001"`, "Occurrence: 0", "Scope ID: 2", "Whole Relation: false"} {
		if !strings.Contains(text, want) {
			t.Fatal("TEXT lost owned range scalar/identifier", want, text)
		}
	}
	after, err := json.Marshal(group)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("presentation mutated detached range facts", err)
	}
	// No host or binding exists here: disabled capture must not traverse or
	// allocate a collector even when called by the command's common path.
	disabled := &Renderer{options: Options{Format: "json"}}
	if err := disabled.captureRanges(nil, Bound{}); err != nil || disabled.rangeTable != nil {
		t.Fatal("disabled collector allocated/traversed", err)
	}
}
