package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
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
					partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
						run(referenceExec("CREATE TABLE t(n INT,active BOOL)", 0))
						run(referenceExec("INSERT INTO t VALUES(1,TRUE),(2,NULL)", 2))
						var before bytes.Buffer
						if !b.pg {
							if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
								t.Fatal(err)
							}
						}
						q := prefix + "CREATE INDEX ix ON t(n) WHERE " + tc.predicate
						want := controlsReferenceDiagnostic(tc.code, tc.message)
						position := 31
						if prefix != "" {
							position += 6
						}
						want.Position = referencePosition(position)
						s := referenceErrorDiagnostic(q, want)
						s.errorStage = "execute"
						if held {
							stmt, err := b.db.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal("utility Prepare must be quiet", err)
							}
							defer stmt.Close()
							referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
						} else {
							run(s)
						}
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
						run(referenceSelect("SELECT n,active FROM t ORDER BY n", []string{"n", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}}))
						if !b.pg {
							var after bytes.Buffer
							if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
								t.Fatal("window predicate failure changed Dump", err, after.String())
							}
						}
						run(referenceExec("CREATE INDEX ix ON t(n) WHERE active", 0))
						run(referenceExec("INSERT INTO t VALUES(3,FALSE)", 1))
					})
				})
			}
		}
	}
}
