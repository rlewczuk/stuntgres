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
	"stuntgres/internal/engine/catalog"
)

func TestExplainStructuredPresentation(t *testing.T) {
	stringsWant := []string{"none", "true", "1", "a,b:c'd\"<&", "line\nend", "", "&", "<"}
	for _, format := range []string{"json", "xml", "yaml"} {
		t.Run(format, func(t *testing.T) {
			r := &Renderer{options: Options{Format: format}}
			tree := &Tree{nodeType: "Project", label: "not a document", children: []*Tree{{nodeType: "Values"}}}
			r.property(tree, "Output", stringsWant)
			r.property(tree, "Typed Data", map[string]any{"Integer": 7, "Zero": int64(0), "Boolean": true, "Empty List": []any{}, "Empty Map": map[string]any{}, "Nil": nil})
			r.repeated(tree.children[0], "Values", []string{"1", "'a,b'"})
			r.repeated(tree.children[0], "Values", []string{"2", "NULL"})
			r.repeated(tree, "Grouping Sets", []string{"a", "b"})
			r.repeated(tree, "Grouping Sets", []string{})
			if err := r.foreignProperties(tree, []catalog.ForeignExplainProperty{{Name: "Node Type", Value: "data not structure"}, {Name: "same", Value: "first"}, {Name: "same", Value: "second"}}); err != nil {
				t.Fatal(err)
			}
			document := []map[string]any{{"Plan": r.structuredNode(tree)}}
			before, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := encodeStructured(format, document)
			if err != nil {
				t.Fatal(err)
			}
			if format == "xml" {
				var root struct {
					XMLName xml.Name
					Queries []struct {
						Plan struct {
							NodeType string   `xml:"Node-Type"`
							Output   []string `xml:"Output>Item"`
							Typed    struct {
								Integer   string `xml:"Integer"`
								Zero      string `xml:"Zero"`
								Boolean   string `xml:"Boolean"`
								EmptyList string `xml:"Empty-List"`
								EmptyMap  string `xml:"Empty-Map"`
								Nil       string `xml:"Nil"`
							} `xml:"Typed-Data"`
							Provider []struct{ Name, Value string } `xml:"Provider-Properties>Item"`
							Groups   []struct {
								Items []string `xml:"Item"`
							} `xml:"Grouping-Sets>Item"`
							Children []struct {
								Values []struct {
									Items []string `xml:"Item"`
								} `xml:"Values>Item"`
							} `xml:"Plans>Plan"`
						} `xml:"Plan"`
					} `xml:"Query"`
				}
				d := xml.NewDecoder(bytes.NewReader(encoded))
				if err := d.Decode(&root); err != nil {
					t.Fatal(err)
				}
				if _, err := d.Token(); err != io.EOF {
					t.Fatal("XML not fully consumed", err)
				}
				if root.XMLName.Local != "explain" || root.XMLName.Space != explainNamespace || len(root.Queries) != 1 {
					t.Fatal("incorrect XML namespace/container", string(encoded))
				}
				p := root.Queries[0].Plan
				for _, label := range []string{"Empty-List", "Empty-Map", "Nil"} {
					if !bytes.Contains(encoded, []byte("<"+label+"></"+label+">")) {
						t.Fatal("XML omitted an explicit empty/nil property", label, string(encoded))
					}
				}
				if p.NodeType != "Project" || !reflect.DeepEqual(p.Output, stringsWant) || p.Typed.Integer != "7" || p.Typed.Zero != "0" || p.Typed.Boolean != "true" || p.Typed.Nil != "" || p.Typed.EmptyMap != "" || p.Typed.EmptyList != "" {
					t.Fatal("XML scalar/escaping/empty lexical contract", string(encoded))
				}
				if len(p.Provider) != 3 || p.Provider[0].Name != "Node Type" || p.Provider[1].Value != "first" || p.Provider[2].Value != "second" || len(p.Groups) != 2 || len(p.Groups[0].Items) != 2 || len(p.Groups[1].Items) != 0 || len(p.Children) != 1 || len(p.Children[0].Values) != 2 || len(p.Children[0].Values[0].Items) != 2 {
					t.Fatal("XML nested lists/provider multiplicity lost", string(encoded))
				}
			} else {
				var decoded []map[string]any
				if format == "json" {
					err = json.Unmarshal(encoded, &decoded)
				} else {
					err = yaml.Unmarshal(encoded, &decoded)
				}
				if err != nil || len(decoded) != 1 {
					t.Fatal("not one sequence entry", string(encoded), err)
				}
				p := decoded[0]["Plan"].(map[string]any)
				out := p["Output"].([]any)
				for i, want := range stringsWant {
					if out[i] != want {
						t.Fatal("string scalar coerced/escaped incorrectly", out)
					}
				}
				typed := p["Typed Data"].(map[string]any)
				if typed["Boolean"] != true || typed["Nil"] != nil || len(typed["Empty List"].([]any)) != 0 || len(typed["Empty Map"].(map[string]any)) != 0 {
					t.Fatal("typed/empty values changed", typed)
				}
				if format == "json" {
					if typed["Integer"] != float64(7) || typed["Zero"] != float64(0) {
						t.Fatal(typed)
					}
				} else {
					if typed["Integer"] != 7 || typed["Zero"] != 0 {
						t.Fatal("YAML integers became strings/floats", typed)
					}
				}
				props := p["Provider Properties"].([]any)
				if len(props) != 3 || props[1].(map[string]any)["Value"] != "first" || props[2].(map[string]any)["Value"] != "second" {
					t.Fatal("duplicate provider labels merged", props)
				}
				groups := p["Grouping Sets"].([]any)
				if len(groups) != 2 || len(groups[0].([]any)) != 2 || len(groups[1].([]any)) != 0 {
					t.Fatal("grouping sets flattened", groups)
				}
				values := p["Plans"].([]any)[0].(map[string]any)["Values"].([]any)
				if len(values) != 2 || len(values[0].([]any)) != 2 || values[0].([]any)[1] != "'a,b'" {
					t.Fatal("VALUES boundaries flattened", values)
				}
			}
			after, err := json.Marshal(document)
			if err != nil || !bytes.Equal(before, after) || stringsWant[3] != "a,b:c'd\"<&" || tree.debug != nil {
				t.Fatal("encoder mutated detached source properties", err)
			}
		})
	}
	for _, debug := range []bool{false, true} {
		r := &Renderer{options: Options{Format: "text", Debug: debug}}
		tree := &Tree{}
		r.property(tree, "Output", stringsWant)
		r.repeated(tree, "Values", stringsWant)
		if tree.properties != nil || tree.debug != nil {
			t.Fatal("unrequested TEXT properties/debug were allocated")
		}
	}
	if _, err := encodeStructured("unsupported", nil); err == nil || !strings.Contains(err.Error(), "invalid structured EXPLAIN format") {
		t.Fatal("invalid internal format admitted", err)
	}
}
