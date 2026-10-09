package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceExpressionIndexDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		q, code, message string
		position         int
	}{
		{"CREATE INDEX ix ON t(lower(missing))", "42703", `column "missing" does not exist`, 28},
		{"CREATE INDEX ix ON t((count(*)))", "42803", "aggregate functions are not allowed in index expressions", 23},
		{"CREATE INDEX ix ON t(((SELECT n FROM t)))", "0A000", "cannot use subquery in index expression", 23},
		{"CREATE INDEX ix ON t(((VALUES (1))))", "0A000", "cannot use subquery in index expression", 23},
		{"CREATE INDEX ix ON t((1+sum(n)))", "42803", "aggregate functions are not allowed in index expressions", 25},
		{"CREATE INDEX ix ON t((1+(SELECT n FROM t)))", "0A000", "cannot use subquery in index expression", 25},
		{"CREATE INDEX ix ON t(lower(other.v))", "42P01", `missing FROM-clause entry for table "other"`, 28},
		{"CREATE INDEX ix ON t(random())", "42P17", "functions in index expression must be marked IMMUTABLE", 0},
		{"CREATE INDEX ix ON t((current_user))", "42P17", "functions in index expression must be marked IMMUTABLE", 0},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, held := range []bool{false, true} {
				t.Run(prefix+tc.q+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
							run(referenceExec("CREATE TABLE t(n INT,v TEXT)", 0))
							run(referenceExec("INSERT INTO t VALUES(1,'Alpha'),(2,NULL)", 2))
							var before bytes.Buffer
							if !backend.pg {
								if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
									t.Fatal(err)
								}
							}
							pos := tc.position
							if pos > 0 && prefix != "" {
								pos += 6
							}
							want := controlsReferenceDiagnostic(tc.code, tc.message)
							want.Position = referencePosition(pos)
							q := prefix + tc.q
							s := referenceErrorDiagnostic(q, want)
							if held && backend.pg {
								// Pinned PostgreSQL utility Prepare is quiet; rejection occurs at Execute.
								s = referencePrepared(s)
								s.errorStage = "execute"
								run(s)
							} else if held {
								stmt, err := backend.db.PrepareContext(p.ctx, q)
								if err != nil {
									t.Fatal("Prepare must be quiet", err)
								}
								defer stmt.Close()
								s.errorStage = "execute"
								referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}, backend.recorder, s)
							} else {
								s.errorStage = "execute"
								run(s)
							}
							run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
							run(referenceSelect("SELECT n,v FROM t ORDER BY n", []string{"n", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Alpha"}, {int64(2), nil}}))
							if !backend.pg {
								var after bytes.Buffer
								if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
									t.Fatal("failed definition changed Dump", err, after.String())
								}
							}
							run(referenceExec("CREATE INDEX ix ON t(lower(v))", 0))
						})
					}
				})
			}
		}
	}
}
