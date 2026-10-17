package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Keep catalog identity separate from lib/pq's missing OID metadata entries.
func jsonpathReferenceStep(t *testing.T, b fixture.Backend, s fixture.Step) fixture.Step {
	t.Helper()
	if !s.SelectRows || s.Diagnostic != nil {
		return s
	}
	families := append([]string(nil), s.Types...)
	s.Types = append([]string(nil), s.Types...)
	s.RawTypes = families
	for i, family := range families {
		if b.PG && (family == "JSONPATH" || family == "_JSONPATH") {
			s.Types[i] = ""
		}
	}
	previous := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		if previous != nil {
			previous(t, rows)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, family := range families {
			if family != "JSONPATH" && family != "_JSONPATH" {
				continue
			}
			scan := reflect.TypeFor[string]()
			if b.PG {
				scan = reflect.TypeFor[any]()
			}
			if types[i].ScanType() != scan {
				t.Fatalf("%s ScanType=%v want=%v", family, types[i].ScanType(), scan)
			}
		}
	}
	return s
}

// Catalog inquiries are shared. Existing explicit NAME/CHAR-to-TEXT cast
// boundaries remain independent of pg_type source availability.
func jsonpathReferenceInquiry(b fixture.Backend, s fixture.Step) fixture.Step {
	if b.PG {
		return s
	}
	if strings.Contains(s.Query, " FROM pg_catalog.pg_attribute") || strings.Contains(s.Query, " FROM pg_catalog.pg_type") {
		if !strings.Contains(s.Query, "::text") {
			return s
		}
		diagnostic := fixture.Diagnostic{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: fixture.Position(strings.Index(s.Query, "::text") + 1)}
		boundary := fixture.ImplicitError(s.Query, diagnostic, s.Args...)
		boundary.SelectRows, boundary.Prepared, boundary.ErrorStage = true, s.Prepared, "execute"
		if s.Prepared {
			boundary.ErrorStage = "prepare"
		}
		return boundary
	}
	panic("JSONPath identity inquiry must name a recognized catalog relation")
}

// Shared direct/prepared literal and bind routes use independent case outputs.
func jsonpathReferenceOutput(t *testing.T, p *fixture.Pair, b fixture.Backend, input, output string, prepared bool) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT "+jsonReferenceQuote(input)+"::jsonpath AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{output}}),
		fixture.Select("SELECT $1::jsonpath AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{output}}, input),
	} {
		if prepared {
			s = fixture.Prepared(s)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, jsonpathReferenceStep(t, b, s))
	}
}
