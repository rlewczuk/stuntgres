package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// All RANGE scenarios share the actual fixture backend/recorder and assertion routes.
func referenceRangeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func referenceRangeError(query, code, message string, fields fixture.Diagnostic) fixture.Step {
	fields.Code, fields.Message = code, message
	s := fixture.ImplicitError(query, fields)
	s.ErrorStage = "execute"
	return s
}

// Position predictions count SQL characters, including quoted UTF8 identifiers.
// Each caller supplies the exact independently chosen deciding occurrence.
func referenceRangePosition(query, token string) string {
	i := strings.Index(query, token)
	if i < 0 {
		panic("range diagnostic token absent")
	}
	return strconv.Itoa(utf8.RuneCountInString(query[:i]) + 1)
}

func referenceRangeRows(query string, values ...int64) fixture.Step {
	rows := make([][]any, len(values))
	for i, value := range values {
		rows[i] = []any{value}
	}
	return fixture.Select(query, []string{"k"}, []string{"INT4"}, rows)
}

func referenceRangeMiss(query, table, keys, values string) fixture.Step {
	detail := "Partition key of the failing row contains (" + keys + ") = (" + values + ")."
	return referenceRangeError(query, "23514", "no partition of relation \""+table+"\" found for row", fixture.Diagnostic{Schema: "public", Table: table, Detail: detail})
}

func referenceRangeLeafMiss(query, table, values string) fixture.Step {
	return referenceRangeError(query, "23514", "new row for relation \""+table+"\" violates partition constraint", fixture.Diagnostic{Schema: "public", Table: table, Detail: "Failing row contains (" + values + ")."})
}

func referenceRangeMetadata(pg bool, names, types []string, nullable []bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.TableDDLColumnTypes(t, pg, columns, names, types, nullable)
		for _, column := range columns {
			wantLength, wantLengthKnown := int64(0), false
			if column.DatabaseTypeName() == "TEXT" {
				wantLength, wantLengthKnown = math.MaxInt64, true
			}
			if n, known := column.Length(); n != wantLength || known != wantLengthKnown {
				t.Fatalf("length got %d/%t want %d/%t", n, known, wantLength, wantLengthKnown)
			}
			if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatalf("unexpected decimal metadata %d/%d/%t", precision, scale, known)
			}
		}
	}
}
