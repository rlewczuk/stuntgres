package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestExplainTextPublication(t *testing.T) {
	ctx := context.Background()
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	text := types[sqltype.Text]
	if text == nil {
		t.Fatal("missing genuine builtin TEXT descriptor")
	}
	schema := []catalog.Column{{Name: "QUERY PLAN", Usage: text.Usage()}}
	planning, execution := 1500*time.Microsecond, 2250*time.Microsecond
	cases := []struct {
		name        string
		lines, want []string
		summaries   Summaries
	}{
		{name: "no-lines", want: []string{}},
		{name: "empty-record", lines: []string{""}, want: []string{""}},
		{name: "no-newline", lines: []string{"alpha", "beta"}, want: []string{"alpha", "beta"}},
		{name: "mixed-boundaries", lines: []string{"alpha\nbeta", "", "\ngamma\n", "delta"}, want: []string{"alpha", "beta", "", "", "gamma", "", "delta"}},
		{name: "only-delimiters", lines: []string{"\n\n"}, want: []string{"", "", ""}},
		{name: "content-not-normalized", lines: []string{"alpha\r\né\u2028ß", "\r"}, want: []string{"alpha\r", "é\u2028ß", "\r"}},
		{name: "planning-summary", lines: []string{"alpha\nbeta"}, summaries: Summaries{Planning: &planning}, want: []string{"alpha", "beta", "Planning Time: 1.500 ms"}},
		{name: "execution-summary", summaries: Summaries{Execution: &execution}, want: []string{"Execution Time: 2.250 ms"}},
		{name: "both-summaries", lines: []string{"alpha\n"}, summaries: Summaries{Planning: &planning, Execution: &execution}, want: []string{"alpha", "", "Planning Time: 1.500 ms", "Execution Time: 2.250 ms"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Spare capacity is deliberately observable: summary publication
			// must not overwrite the caller's unexposed backing elements.
			backing := append(append([]string(nil), tc.lines...), "unused-one", "unused-two")
			lines := backing[:len(tc.lines)]
			before := append([]string(nil), backing...)
			rows, err := Output(ctx, schema, Options{Format: "text"}, nil, lines, tc.summaries)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(tc.want) {
				t.Fatal("wrong physical row count", len(rows), tc.want)
			}
			got := make([]string, len(rows))
			for i, row := range rows {
				if len(row) != 1 || row[0].IsNull() || row[0].Type() != text {
					t.Fatal("physical row lost non-NULL descriptor-owned TEXT value", i, row)
				}
				got[i], err = row[0].Text()
				if err != nil || strings.Contains(got[i], "\n") {
					t.Fatal("TEXT row is not one physical LF-delimited line", i, got[i], err)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatal("physical line content/order differs from independent expectation", got, tc.want)
			}
			if tc.summaries == (Summaries{}) && strings.Join(got, "\n") != strings.Join(tc.lines, "\n") {
				t.Fatal("physical publication changed logical text content", got, tc.lines)
			}
			if !reflect.DeepEqual(backing, before) || len(lines) != len(tc.lines) {
				t.Fatal("Output mutated caller logical lines or spare backing", backing, before)
			}
		})
	}

	// The structured branch must continue to publish one descriptor-coded
	// document, preserving raw identifier text rather than TEXT row segments.
	names := []string{"first\nline", "\nleading", "trailing\n", "first\n\nline", "", "quote\"\n<&"}
	alias := "\nrange\n\nalias\n"
	group := map[string]any{"Scopes": []any{map[string]any{"Scope ID": 1, "Kind": "query", "Entries": []any{map[string]any{"Entry ID": 1, "Kind": "relation", "Range Name": alias, "Columns": names}}}}}
	before, err := json.Marshal(group)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "xml", "yaml"} {
		t.Run("structured-"+format, func(t *testing.T) {
			typ := text
			if format == "json" {
				typ = types[sqltype.JSON]
			}
			if format == "xml" {
				typ = types[sqltype.XML]
			}
			if typ == nil {
				t.Fatal("missing genuine structured descriptor", format)
			}
			renderer := &Renderer{options: Options{Format: format}, trees: []*Tree{{nodeType: "Project"}}, rangeTable: group}
			lines := []string{"not\na structured document", "unexposed backing"}
			rows, err := Output(ctx, []catalog.Column{{Name: "QUERY PLAN", Usage: typ.Usage()}}, renderer.options, []*Renderer{renderer}, lines[:1], Summaries{})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0].IsNull() || rows[0][0].Type() != typ {
				t.Fatal("structured publication is not one non-NULL descriptor-owned document", rows)
			}
			encoded, err := typ.Output(ctx, rows[0][0])
			if err != nil {
				t.Fatal(err)
			}
			if format == "xml" {
				var root struct {
					XMLName xml.Name
					Queries []struct {
						Entries []struct {
							Name    string   `xml:"Range-Name"`
							Columns []string `xml:"Columns>Item"`
						} `xml:"Embedded-Range-Table>Scopes>Item>Entries>Item"`
					} `xml:"Query"`
				}
				decoder := xml.NewDecoder(strings.NewReader(encoded))
				if err := decoder.Decode(&root); err != nil {
					t.Fatal(err)
				}
				if _, err := decoder.Token(); err != io.EOF {
					t.Fatal("trailing XML document", err)
				}
				if root.XMLName.Space != explainNamespace || len(root.Queries) != 1 || len(root.Queries[0].Entries) != 1 {
					t.Fatal("XML document ownership/namespace changed", encoded)
				}
				entry := root.Queries[0].Entries[0]
				// No TrimSpace: leading/trailing LF are identifier data too.
				if entry.Name != alias || !reflect.DeepEqual(entry.Columns, names) {
					t.Fatal("XML changed exact identifier character data", entry)
				}
			} else {
				var document []map[string]any
				if format == "json" {
					err = json.Unmarshal([]byte(encoded), &document)
				} else {
					decoder := yaml.NewDecoder(strings.NewReader(encoded))
					err = decoder.Decode(&document)
					var extra any
					if err == nil && decoder.Decode(&extra) != io.EOF {
						t.Fatal("trailing YAML document")
					}
				}
				if err != nil || len(document) != 1 {
					t.Fatal("invalid structured document", err, encoded)
				}
				scopes := document[0]["Embedded Range Table"].(map[string]any)["Scopes"].([]any)
				entries := scopes[0].(map[string]any)["Entries"].([]any)
				entry := entries[0].(map[string]any)
				columns := entry["Columns"].([]any)
				if entry["Range Name"] != alias || len(columns) != len(names) {
					t.Fatal("structured identifier boundaries changed", entry)
				}
				for i, name := range names {
					if columns[i] != name {
						t.Fatal("structured identifier text changed", i, columns[i], name)
					}
				}
			}
			if !reflect.DeepEqual(lines, []string{"not\na structured document", "unexposed backing"}) {
				t.Fatal("structured Output touched TEXT logical lines", lines)
			}
			after, err := json.Marshal(group)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("Output mutated detached structured identifiers", err)
			}
		})
	}
}
