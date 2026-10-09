package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceExpressionIndexExplicitInsertAtomicity(t *testing.T) {
	for _, plain := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(map[bool]string{false: "context", true: "plain"}[plain]+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { s.nonContext = plain; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(n INT)", 0))
						run(referenceExec("INSERT INTO t VALUES(2)", 1))
						run(referenceExec("CREATE INDEX ix ON t((10/n))", 0))
						var tx *sql.Tx
						var err error
						if plain {
							tx, err = b.db.Begin()
						} else {
							tx, err = b.db.BeginTx(p.ctx, nil)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						inside := func(s referenceStep) { s.nonContext = plain; referenceRunStep(t, p.ctx, tx, b.recorder, s) }
						inside(referenceExec("INSERT INTO t VALUES(4)", 1))
						inside(referenceExec("SAVEPOINT before_bad_insert", 0))
						const bad = "INSERT INTO t VALUES(5),(0),(10)"
						failure := referenceImplicitError(bad, referenceDiagnostic{Code: "22012", Message: "division by zero"})
						failure.errorStage = "execute"
						failure.nonContext = plain
						if held {
							var stmt *sql.Stmt
							if plain {
								stmt, err = tx.Prepare(bad)
							} else {
								stmt, err = tx.PrepareContext(p.ctx, bad)
							}
							if err != nil {
								t.Fatal("quiet INSERT Prepare", err)
							}
							defer stmt.Close()
							if plain {
								result, err := stmt.Exec()
								want := controlsReferenceDiagnostic("22012", "division by zero")
								referenceAssertDiagnostic(t, err, &want)
								if result != nil {
									t.Fatal("failed held INSERT published result")
								}
							} else {
								referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: tx, statement: stmt, query: bad}, b.recorder, failure)
							}
						} else {
							inside(failure)
						}
						inside(referenceExec("ROLLBACK TO before_bad_insert", 0))
						inside(referenceSelect("SELECT n,10/n AS k FROM t ORDER BY n", []string{"n", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}, {int64(4), int64(2)}}))
						inside(referenceExec("INSERT INTO t VALUES(5)", 1))
						inside(referenceExec("RELEASE before_bad_insert", 0))
						if err := tx.Commit(); err != nil {
							t.Fatal("valid recovered COMMIT", err)
						}
						run(referenceSelect("SELECT n,10/n AS k FROM t ORDER BY n", []string{"n", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}, {int64(4), int64(2)}, {int64(5), int64(2)}}))
					})
				}
			})
		}
	}
}

func TestReferenceExpressionIndexTimeZoneVolatility(t *testing.T) {
	for _, tc := range []struct {
		name, typ, seed, suffix, want string
		immutable                     bool
	}{
		{"timestamp_text", "TIMESTAMP", "2004-07-01 12:00:00", "AT TIME ZONE 'UTC'", "2004-07-01 12:00:00+00", true},
		{"timestamptz_text", "TIMESTAMPTZ", "2004-07-01 12:00:00+00", "AT TIME ZONE 'UTC'", "2004-07-01 12:00:00", true},
		{"timestamp_interval", "TIMESTAMP", "2004-07-01 12:00:00", "AT TIME ZONE INTERVAL '02:00'", "2004-07-01 10:00:00+00", true},
		{"timestamptz_interval", "TIMESTAMPTZ", "2004-07-01 12:00:00+00", "AT TIME ZONE INTERVAL '02:00'", "2004-07-01 14:00:00", true},
		{"timetz_interval", "TIMETZ", "12:00:00+00", "AT TIME ZONE INTERVAL '02:00'", "14:00:00+02", true},
		{"timestamp_local", "TIMESTAMP", "2004-07-01 12:00:00", "AT LOCAL", "", false},
		{"timestamptz_local", "TIMESTAMPTZ", "2004-07-01 12:00:00+00", "AT LOCAL", "", false},
		{"timetz_local", "TIMETZ", "12:00:00+00", "AT LOCAL", "", false},
		{"timetz_text", "TIMETZ", "12:00:00+00", "AT TIME ZONE 'UTC'", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
					run(referenceExec("SET TIME ZONE 'UTC'", 0))
					run(referenceExec("CREATE TABLE t(id INT,v "+tc.typ+")", 0))
					run(referenceExec("INSERT INTO t VALUES(1,'"+tc.seed+"'),(2,NULL)", 2))
					query := "CREATE INDEX ix ON t((v " + tc.suffix + "))"
					if !tc.immutable {
						s := referenceImplicitError(query, referenceDiagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"})
						s.errorStage = "execute"
						run(s)
						run(referencePrepared(s))
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
						run(referenceExec("CREATE INDEX ix ON t(v)", 0))
						return
					}
					run(referencePrepared(referenceExec(query, 0)))
					selected := "SELECT id,(v " + tc.suffix + ")::text AS k FROM t ORDER BY id"
					run(referenceSelect(selected, []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), tc.want}, {int64(2), nil}}))
					run(referenceExec("INSERT INTO t VALUES(3,'"+tc.seed+"')", 1))
					want := referenceSelect(selected, []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), tc.want}, {int64(2), nil}, {int64(3), tc.want}})
					run(want)
					if !b.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(dump.String(), "AT TIME ZONE") {
							t.Fatal("lost timezone definition", dump.String())
						}
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
							t.Fatal(err)
						}
						run(want)
					}
				})
			}
		})
	}
}

func TestReferenceExpressionIndexTypeReparsePositions(t *testing.T) {
	for _, tc := range []struct {
		name, create, seed, index, alter, selectSQL string
		position                                    int
		columns                                     []string
		rows                                        [][]any
	}{
		{"quoted_unicode", "CREATE TABLE \"Té\"(n INT,\"Cé\" INT)", "INSERT INTO \"Té\" VALUES(7,1)", "CREATE INDEX \"Í x\" ON \"Té\"(n DESC NULLS LAST,(\"Cé\"+1))", "ALTER TABLE \"Té\" ALTER \"Cé\" TYPE TEXT USING \"Cé\"::text", "SELECT n,\"Cé\" FROM \"Té\"", 74, []string{"n", "Cé"}, [][]any{{int64(7), int64(1)}}},
		{"earlier_function", "CREATE TABLE t(c INT,v TEXT)", "INSERT INTO t VALUES(1,'Alpha')", "CREATE INDEX ix ON t(lower(v),(c+1))", "ALTER TABLE t ALTER c TYPE TEXT USING c::text", "SELECT c,v FROM t", 56, []string{"c", "v"}, [][]any{{int64(1), "Alpha"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec(tc.create, 0))
					run(referenceExec(tc.seed, 1))
					run(referenceExec(tc.index, 0))
					var before bytes.Buffer
					if !b.pg {
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
					}
					s := referenceImplicitError(tc.alter, referenceDiagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: referencePosition(tc.position)})
					s.errorStage = "execute"
					run(s)
					run(referencePrepared(s))
					types := []string{"INT4", "INT4"}
					if tc.name == "earlier_function" {
						types[1] = "TEXT"
					}
					run(referenceSelect(tc.selectSQL, tc.columns, types, tc.rows))
					if !b.pg {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
							t.Fatal("failed TYPE changed definition", err, after.String())
						}
					}
				})
			}
		})
	}
}
