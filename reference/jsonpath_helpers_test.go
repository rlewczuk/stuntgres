package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// Keep catalog identity separate from lib/pq's missing OID metadata entries.
func jsonpathReferenceStep(t *testing.T, b referenceBackend, s referenceStep) referenceStep {
	t.Helper()
	if !s.selectRows || s.diagnostic != nil {
		return s
	}
	families := append([]string(nil), s.types...)
	s.types = append([]string(nil), s.types...)
	s.rawTypes = families
	for i, family := range families {
		if b.pg && (family == "JSONPATH" || family == "_JSONPATH") {
			s.types[i] = ""
		}
	}
	previous := s.checkMetadata
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
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
			if b.pg {
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
func jsonpathReferenceInquiry(b referenceBackend, s referenceStep) referenceStep {
	if b.pg {
		return s
	}
	for _, relation := range []string{"pg_catalog.pg_type", "pg_catalog.pg_attribute"} {
		if offset := strings.Index(s.query, " FROM "+relation); offset >= 0 {
			boundary := referenceErrorDiagnostic(s.query, referenceDiagnostic{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01",
				Message:  `relation "` + relation + `" does not exist`,
				Position: referencePosition(offset + len(" FROM ") + 1),
			})
			boundary.selectRows = true
			boundary.prepared = s.prepared
			boundary.errorStage = "execute"
			if s.prepared {
				boundary.errorStage = "prepare"
			}
			return boundary
		}
	}
	panic("JSONPath identity inquiry must name pg_catalog.pg_type or pg_catalog.pg_attribute")
}

// Shared direct/prepared literal and bind routes use independent case outputs.
func jsonpathReferenceOutput(t *testing.T, p *referencePair, b referenceBackend, input, output string, prepared bool) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceSelect("SELECT "+jsonReferenceQuote(input)+"::jsonpath AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{output}}),
		referenceSelect("SELECT $1::jsonpath AS v", []string{"v"}, []string{"JSONPATH"}, [][]any{{output}}, input),
	} {
		if prepared {
			s = referencePrepared(s)
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, jsonpathReferenceStep(t, b, s))
	}
}
