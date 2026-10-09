package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

// Expectations are independently sourced from REL_18_0 ExecReindex and the
// already observed single-control records. Root must observe this cross-product.
func TestReferenceIndexMaintenanceMalformedOptionPrecedence(t *testing.T) {
	controls := []struct {
		prefix, options string
		diagnostic      referenceDiagnostic
	}{
		{"", "unknown", referenceDiagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "10"}},
		{"", "verbose 'bad'", referenceDiagnostic{Code: "42601", Message: "verbose requires a Boolean value"}},
		{"", "concurrently 'bad'", referenceDiagnostic{Code: "42601", Message: "concurrently requires a Boolean value"}},
		{"", "tablespace", referenceDiagnostic{Code: "42601", Message: "tablespace requires a parameter"}},
		{"", "parallel 0", referenceDiagnostic{Code: "42601", Message: `unrecognized REINDEX option "parallel"`, Position: "10"}},
		{"", "verbose false, unknown", referenceDiagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "25"}},
		{"", "tablespace pg_default, unknown", referenceDiagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "33"}},
		{"/* é */ ", "unknown", referenceDiagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "18"}},
		{"", "unknown, verbose 'bad'", referenceDiagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "10"}},
		{"", "verbose 'bad', unknown", referenceDiagnostic{Code: "42601", Message: "verbose requires a Boolean value"}},
	}
	targets := []struct {
		clause          string
		denied          bool
		validDiagnostic referenceDiagnostic
	}{
		{"INDEX missing", false, referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`}},
		{"TABLE missing", false, referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`}},
		{"INDEX t", false, referenceDiagnostic{Code: "42809", Message: `"t" is not an index`}},
		{"TABLE ix", false, referenceDiagnostic{Code: "42809", Message: `"ix" is not a table or materialized view`}},
		{"INDEX v", false, referenceDiagnostic{Code: "42809", Message: `"v" is not an index`}},
		{"TABLE seq", false, referenceDiagnostic{Code: "42809", Message: `"seq" is not a table or materialized view`}},
		{"INDEX ix", true, referenceDiagnostic{Code: "42501", Message: "permission denied for index ix"}},
		{"TABLE t", true, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}},
	}
	for _, target := range targets {
		t.Run(target.clause, func(t *testing.T) {
			roleReferenceCleanup(t, []string{"im_badcontrol"})
			indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, _ := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
				for _, s := range []referenceStep{
					referenceExec("CREATE USER im_badcontrol", 0),
					referenceExec("CREATE TABLE t(n INT)", 0),
					referenceExec("INSERT INTO t VALUES(1),(1),(NULL)", 3),
					referenceExec("CREATE INDEX ix ON t(n)", 0),
					referenceExec("CREATE INDEX sibling ON t((n+1))", 0),
					referenceExec("CREATE VIEW v AS SELECT n FROM t", 0),
					referenceExec("CREATE SEQUENCE seq", 0),
				} {
					run(s)
				}
				// The PG fixture recreates public without its original PUBLIC USAGE.
				// Admit real namespace lookup, but deliberately grant no MAINTAIN.
				if b.pg {
					run(referenceExec("GRANT USAGE ON SCHEMA public TO im_badcontrol", 0))
				}
				var index int64
				if err := a.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_class WHERE relname='ix'").Scan(&index); err != nil {
					t.Fatal(err)
				}
				var before bytes.Buffer
				if !b.pg {
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
				}
				checkState := func(t *testing.T) {
					t.Helper()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
					if target.denied {
						run(referenceExec("RESET ROLE", 0))
					}
					run(referenceSelect("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
					run(referenceSelect("SELECT oid::bigint,relkind FROM pg_class WHERE relname='ix'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{index, "i"}}))
					for _, c := range []struct{ name, definition string }{{"ix", "CREATE INDEX ix ON public.t USING btree (n)"}, {"sibling", "CREATE INDEX sibling ON public.t USING btree (((n + 1)))"}} {
						s := referenceSelect("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname=$1", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{c.definition}})
						s.args = []any{c.name}
						run(s)
					}
					if !b.pg {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
							t.Fatal("malformed controls changed native state", err, after.String())
						}
					}
					if target.denied {
						run(referenceExec("SET ROLE im_badcontrol", 0))
					}
				}
				if target.denied {
					run(referenceExec("SET ROLE im_badcontrol", 0))
				}
				for _, control := range controls {
					q := control.prefix + "REINDEX (" + control.options + ") " + target.clause
					t.Run(control.prefix+control.options, func(t *testing.T) {
						stmt, err := a.PrepareContext(p.ctx, q)
						if err != nil {
							if stmt != nil {
								stmt.Close()
							}
							t.Fatal("semantic validation must not happen in quiet Prepare", err)
						}
						defer stmt.Close()
						for _, held := range []bool{false, true} {
							for _, query := range []bool{false, true} {
								s := referenceImplicitError(q, control.diagnostic)
								s.errorStage, s.selectRows = "execute", query
								if held {
									referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: stmt, query: q}, b.recorder, s)
								} else {
									referenceRunStep(t, p.ctx, a, b.recorder, s)
								}
								checkState(t)
							}
						}
					})
				}
				// Valid controls keep normal target/ACL admission, not an early native
				// physical-capability error. They are deliberately separate from invalids.
				for _, option := range []string{"verbose false", "concurrently false", "tablespace pg_default"} {
					q := "REINDEX (" + option + ") " + target.clause
					s := referencePrepared(referenceImplicitError(q, target.validDiagnostic))
					s.errorStage = "execute"
					run(s)
					checkState(t)
				}
				if target.denied {
					run(referenceExec("RESET ROLE", 0))
				}
				// Preserve independently intentional native admission ahead of controls.
				for _, readOnly := range []bool{false, true} {
					begin := "BEGIN"
					if readOnly {
						begin = "BEGIN READ ONLY"
					}
					run(referenceExec(begin, 0))
					q := "REINDEX (unknown) " + target.clause
					want := referenceDiagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "10"}
					if !b.pg {
						want = referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
						if readOnly {
							want = referenceDiagnostic{Code: "25006", Message: "cannot execute REINDEX in a read-only transaction"}
						}
					}
					s := referencePrepared(referenceImplicitError(q, want))
					s.errorStage = "execute"
					run(s)
					run(referenceExec("ROLLBACK", 0))
				}
			})
		})
	}
}
