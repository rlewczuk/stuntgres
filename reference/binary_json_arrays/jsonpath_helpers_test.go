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

// These two named inquiry relations are outside the native catalog surface,
// even where other pg_catalog views exist. PostgreSQL still checks every
// expected identity field; native checks the complete missing-relation error.
func jsonpathReferenceInquiry(b fixture.Backend, s fixture.Step) fixture.Step {
	if b.PG {
		return s
	}
	for _, relation := range []string{"pg_catalog.pg_type", "pg_catalog.pg_attribute"} {
		if offset := strings.Index(s.Query, " FROM "+relation); offset >= 0 {
			boundary := fixture.ErrorDiagnostic(s.Query, fixture.Diagnostic{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01",
				Message:  `relation "` + relation + `" does not exist`,
				Position: fixture.Position(offset + len(" FROM ") + 1),
			})
			boundary.SelectRows = true
			boundary.Prepared = s.Prepared
			boundary.ErrorStage = "execute"
			if s.Prepared {
				boundary.ErrorStage = "prepare"
			}
			return boundary
		}
	}
	panic("JSONPath identity inquiry must name pg_catalog.pg_type or pg_catalog.pg_attribute")
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
