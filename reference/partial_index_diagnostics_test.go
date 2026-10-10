package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func TestReferencePartialIndexDiagnostics(t *testing.T) {
	// Independently observed PostgreSQL 18.0 predicate diagnostics.
	for _, tc := range []struct {
		q, code, message string
		position         int
	}{
		{"CREATE INDEX ix ON t(n) WHERE (SELECT TRUE)", "0A000", "cannot use subquery in index predicate", 31},
		{"CREATE INDEX ix ON t(n) WHERE (VALUES (TRUE))", "0A000", "cannot use subquery in index predicate", 31},
		{"CREATE INDEX ix ON t(n) WHERE count(*)>0", "42803", "aggregate functions are not allowed in index predicates", 31},
		{"CREATE INDEX ix ON t(n) WHERE (1+sum(n)>0)", "42803", "aggregate functions are not allowed in index predicates", 34},
		{"CREATE INDEX ix ON t(n) WHERE other.n>0", "42P01", `missing FROM-clause entry for table "other"`, 31},
		{"CREATE INDEX ix ON t(n) WHERE missing>0", "42703", `column "missing" does not exist`, 31},
		{"CREATE INDEX ix ON t(n) WHERE n", "42804", "argument of WHERE must be type boolean, not type integer", 31},
		{"CREATE INDEX ix ON t(n) WHERE random()>0", "42P17", "functions in index predicate must be marked IMMUTABLE", 0},
		{"CREATE INDEX ix ON t(n) WHERE current_user='postgres'", "42P17", "functions in index predicate must be marked IMMUTABLE", 0},
		{"CREATE INDEX ix ON t(n) WHERE n=$1", "42P02", "there is no parameter $1", 33},
		{"CREATE INDEX ix ON t(missing) WHERE n", "42804", "argument of WHERE must be type boolean, not type integer", 37},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, held := range []bool{false, true} {
				t.Run(prefix+tc.q+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
					partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
						run(referenceExec("CREATE TABLE t(n INT,active BOOL)", 0))
						run(referenceExec("CREATE TABLE other(n INT)", 0))
						run(referenceExec("INSERT INTO t VALUES(1,TRUE),(2,NULL)", 2))
						var before bytes.Buffer
						if !b.pg {
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
								t.Fatal("failed predicate changed Dump", err, after.String())
							}
						}
						run(referenceExec("CREATE INDEX ix ON t(n) WHERE active", 0))
						run(referenceExec("INSERT INTO t VALUES(3,FALSE)", 1))
					})
				})
			}
		}
	}
	// Failed parsing must not leave the index-expression lexer consumer enabled.
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(n INT,active BOOL)", 0))
		want := controlsReferenceDiagnostic("42601", "syntax error at end of input")
		want.Position = "30"
		run(referenceErrorDiagnostic("CREATE INDEX ix ON t(n) WHERE", want))
		run(referenceExec("INSERT INTO t VALUES(1,TRUE)", 1))
		run(referenceExec("CREATE INDEX ix ON t(n) WHERE active", 0))
		run(referenceSelect("SELECT count(*) AS total FROM t", []string{"total"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

func TestReferencePartialIndexRepeatedWhere(t *testing.T) {
	for _, predicate := range []string{"FALSE", "(SELECT TRUE)"} {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, preceding := range []string{"", "INSERT INTO t VALUES(99,TRUE); "} {
				for _, prepared := range []bool{false, true} {
					// PostgreSQL extended Prepare accepts one command only. Test
					// whole-input prefix atomicity via direct Exec, not that boundary.
					if prepared && preceding != "" {
						continue
					}
					for _, plain := range []bool{false, true} {
						name := prefix + preceding + predicate
						if prepared {
							name += "/prepared"
						} else {
							name += "/direct"
						}
						if plain {
							name += "/plain"
						} else {
							name += "/context"
						}
						t.Run(name, func(t *testing.T) {
							p := newReferenceScenario(t)
							for _, b := range p.backends() {
								t.Run(b.name, func(t *testing.T) {
									run := func(s referenceStep) {
										s.nonContext = plain
										referenceRunStep(t, p.ctx, b.db, b.recorder, s)
									}
									run(referenceExec("CREATE TABLE t(n INT,active BOOL)", 0))
									run(referenceExec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3))
									var before bytes.Buffer
									if !b.pg {
										if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
											t.Fatal(err)
										}
									}

									q := prefix + preceding + "CREATE INDEX partial_repeated_where_ix ON t(n) WHERE " + predicate + " WHERE active"
									// Independently observed PostgreSQL 18.0 syntax rejection
									// is at the second WHERE, before SubLink binding.
									// Positions count UTF8 characters in the complete input, not bytes.
									want := controlsReferenceDiagnostic("42601", `syntax error at or near "WHERE"`)
									want.Position = referencePosition(utf8.RuneCountInString(q[:strings.LastIndex(q, "WHERE")]) + 1)
									s := referenceErrorDiagnostic(q, want)
									s.errorStage = "execute"
									if prepared {
										// Malformed syntax cannot yield a held statement. Assert
										// nil-handle rejection at Prepare, not a semantic Execute error.
										s = referencePrepared(s)
										s.errorStage = "prepare"
									}
									run(s)
									run(referenceSelect("SELECT relname FROM pg_class WHERE relname='partial_repeated_where_ix'", []string{"relname"}, []string{"NAME"}, nil))
									run(referenceSelect("SELECT n,active FROM t ORDER BY n", []string{"n", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
									if !b.pg {
										var after bytes.Buffer
										if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
											t.Fatal("repeated WHERE changed Dump", err, after.String())
										}
									}

									recovery := "CREATE INDEX partial_repeated_where_ix ON t(n) WHERE active"
									if prepared {
										var stmt *sql.Stmt
										var err error
										if plain {
											stmt, err = b.db.Prepare(recovery)
										} else {
											stmt, err = b.db.PrepareContext(p.ctx, recovery)
										}
										if err != nil {
											t.Fatal("same-name recovery Prepare", err)
										}
										defer stmt.Close()
										if plain {
											result, err := stmt.Exec()
											if err != nil {
												t.Fatal("same-name recovery Exec", err)
											}
											count, err := result.RowsAffected()
											if err != nil || count != 0 {
												t.Fatal("same-name recovery count", count, err)
											}
										} else {
											referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: recovery}, b.recorder, referenceExec(recovery, 0))
										}
									} else {
										run(referenceExec(recovery, 0))
									}
									run(referenceSelect("SELECT relname FROM pg_class WHERE relname='partial_repeated_where_ix'", []string{"relname"}, []string{"NAME"}, [][]any{{"partial_repeated_where_ix"}}))
									run(referenceExec("INSERT INTO t VALUES(4,TRUE),(5,FALSE)", 2))
									run(referenceSelect("SELECT n FROM t WHERE active ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}))
									run(referenceSelect("SELECT n,active FROM t ORDER BY n", []string{"n", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(4), true}, {int64(5), false}}))
								})
							}
						})
					}
				}
			}
		}
	}
}
