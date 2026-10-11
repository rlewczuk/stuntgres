package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func typedTableScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool, func(fixture.Step))) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					body(t, p, b, c, prepared, run)
				})
			}
		})
	}
}

func typedTableError(code, message string) fixture.Diagnostic {
	return fixture.CompositeReferenceDiagnostic(code, message)
}

func typedTableAttachment(table, source string, attached bool) fixture.Step {
	q := "SELECT reloftype=" + "'" + source + "'::regtype::oid AS attached FROM pg_class WHERE oid='" + table + "'::regclass::oid"
	if !attached {
		q = "SELECT reloftype=0 AS attached FROM pg_class WHERE oid='" + table + "'::regclass::oid"
	}
	return fixture.Select(q, []string{"attached"}, []string{"BOOL"}, [][]any{{true}})
}

func typedTablePairRows(table string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT * FROM "+table+" ORDER BY id", []string{"id", "note"}, []string{"INT4", "VARCHAR"}, rows)
}

func typedTableRestriction(source string) fixture.Diagnostic {
	d := typedTableError("2BP01", `cannot alter type "`+source+`" because it is the type of a typed table`)
	d.Hint = "Use ALTER ... CASCADE to alter the typed tables too."
	return d
}
