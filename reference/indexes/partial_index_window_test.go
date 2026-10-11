package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartialIndexWindowDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		predicate, code, message string
	}{
		{"row_number() OVER ()>0", "42P20", "window functions are not allowed in index predicates"},
		{"count(*) OVER ()>0", "42P20", "window functions are not allowed in index predicates"},
		{"row_number()>0", "42809", "window function row_number requires an OVER clause"},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, held := range []bool{false, true} {
				t.Run(prefix+tc.predicate+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
					partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE t(n INT,active BOOL)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,NULL)", 2))
						var before bytes.Buffer
						if !b.PG {
							if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
								t.Fatal(err)
							}
						}
						q := prefix + "CREATE INDEX ix ON t(n) WHERE " + tc.predicate
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						position := 31
						if prefix != "" {
							position += 6
						}
						want.Position = fixture.Position(position)
						s := fixture.ErrorDiagnostic(q, want)
						s.ErrorStage = "execute"
						if held {
							stmt, err := b.DB.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal("utility Prepare must be quiet", err)
							}
							defer stmt.Close()
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
						} else {
							run(s)
						}
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
						run(fixture.Select("SELECT n,active FROM t ORDER BY n", []string{"n", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}}))
						if !b.PG {
							var after bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
								t.Fatal("window predicate failure changed Dump", err, after.String())
							}
						}
						run(fixture.Exec("CREATE INDEX ix ON t(n) WHERE active", 0))
						run(fixture.Exec("INSERT INTO t VALUES(3,FALSE)", 1))
					})
				})
			}
		}
	}
}
