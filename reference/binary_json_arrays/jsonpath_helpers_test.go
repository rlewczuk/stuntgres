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

// pg_type remains a named native exclusion. Original attribute inquiries retain
// their full PostgreSQL results and exact native cast/mixed-OID boundaries; the
// callers also exercise admitted numeric-OID attribute metadata separately.
func jsonpathReferenceInquiry(b fixture.Backend, s fixture.Step) fixture.Step {
	if b.PG {
		return s
	}
	if offset := strings.Index(s.Query, " FROM pg_catalog.pg_type"); offset >= 0 {
		boundary := fixture.ErrorDiagnostic(s.Query, fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01",
			Message:  `relation "pg_catalog.pg_type" does not exist`,
			Position: fixture.Position(offset + len(" FROM ") + 1),
		})
		boundary.SelectRows, boundary.Prepared, boundary.ErrorStage = true, s.Prepared, "execute"
		if s.Prepared {
			boundary.ErrorStage = "prepare"
		}
		return boundary
	}
	if strings.Contains(s.Query, " FROM pg_catalog.pg_attribute") {
		diagnostic := fixture.Diagnostic{Code: "42883", Message: "operator does not exist: oid = regclass", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: fixture.Position(strings.Index(s.Query, "attrelid=") + len("attrelid") + 1)}
		if strings.Contains(s.Query, "attname::text") {
			diagnostic = fixture.Diagnostic{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: fixture.Position(strings.Index(s.Query, "::text") + 1)}
		}
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
