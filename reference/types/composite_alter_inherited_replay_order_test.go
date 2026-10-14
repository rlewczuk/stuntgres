package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterInheritedReplayOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Current declarations reconstruct inherited columns before local ones;
				// an explicit INSERT column list must not determine SELECT * order.
				fixture.CompositeReferenceSetup(t, p, b, c,
					`CREATE TYPE pair AS (note text,extra integer)`,
					`CREATE TABLE parent(note text,extra integer,CONSTRAINT keep_root CHECK(note<>'bad'))`,
					`CREATE TABLE child(v pair,CONSTRAINT keep_local CHECK((v).note<>'blocked')) INHERITS(parent)`,
				)
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				run(fixture.Exec(`INSERT INTO child(note,v,extra) VALUES('first',$1,NULL)`, 1, "(payload,)"))
				check := func() {
					s := fixture.Select(`SELECT * FROM child ORDER BY note`, []string{"note", "extra", "v"}, []string{"TEXT", "INT4", ""}, [][]any{{"first", nil, "(payload,)"}})
					s.Prepared = prepared
					fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, s)
				}
				check()
				for _, tc := range []struct {
					note, value, constraint, detail string
				}{
					{"bad", "(accepted,)", "keep_root", "Failing row contains (bad, 5, (accepted,))."},
					{"ok", "(blocked,)", "keep_local", "Failing row contains (ok, 5, (blocked,))."},
				} {
					want := fixture.CompositeReferenceDiagnostic("23514", `new row for relation "child" violates check constraint "`+tc.constraint+`"`)
					want.Detail = tc.detail
					want.Schema, want.Table, want.Constraint = "public", "child", tc.constraint
					fixture.CompositeReferenceError(t, p, c, "INSERT INTO child(note,v,extra) VALUES('"+tc.note+"',$1,5)", "execute", want, tc.value)
				}
				check()
			})
		})
	}
}
