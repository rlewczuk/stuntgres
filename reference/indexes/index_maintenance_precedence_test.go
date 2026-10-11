package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations are independently sourced from REL_18_0 ExecReindex and the
// already observed single-control records. Root must observe this cross-product.
func TestReferenceIndexMaintenanceMalformedOptionPrecedence(t *testing.T) {
	controls := []struct {
		prefix, options string
		diagnostic      fixture.Diagnostic
	}{
		{"", "unknown", fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "10"}},
		{"", "verbose 'bad'", fixture.Diagnostic{Code: "42601", Message: "verbose requires a Boolean value"}},
		{"", "concurrently 'bad'", fixture.Diagnostic{Code: "42601", Message: "concurrently requires a Boolean value"}},
		{"", "tablespace", fixture.Diagnostic{Code: "42601", Message: "tablespace requires a parameter"}},
		{"", "parallel 0", fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "parallel"`, Position: "10"}},
		{"", "verbose false, unknown", fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "25"}},
		{"", "tablespace pg_default, unknown", fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "33"}},
		{"/* é */ ", "unknown", fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "18"}},
		{"", "unknown, verbose 'bad'", fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "10"}},
		{"", "verbose 'bad', unknown", fixture.Diagnostic{Code: "42601", Message: "verbose requires a Boolean value"}},
	}
	targets := []struct {
		clause          string
		denied          bool
		validDiagnostic fixture.Diagnostic
	}{
		{"INDEX missing", false, fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`}},
		{"TABLE missing", false, fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`}},
		{"INDEX t", false, fixture.Diagnostic{Code: "42809", Message: `"t" is not an index`}},
		{"TABLE ix", false, fixture.Diagnostic{Code: "42809", Message: `"ix" is not a table or materialized view`}},
		{"INDEX v", false, fixture.Diagnostic{Code: "42809", Message: `"v" is not an index`}},
		{"TABLE seq", false, fixture.Diagnostic{Code: "42809", Message: `"seq" is not a table or materialized view`}},
		{"INDEX ix", true, fixture.Diagnostic{Code: "42501", Message: "permission denied for index ix"}},
		{"TABLE t", true, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}},
	}
	for _, target := range targets {
		t.Run(target.clause, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"im_badcontrol"})
			indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
				for _, s := range []fixture.Step{
					fixture.Exec("CREATE USER im_badcontrol", 0),
					fixture.Exec("CREATE TABLE t(n INT)", 0),
					fixture.Exec("INSERT INTO t VALUES(1),(1),(NULL)", 3),
					fixture.Exec("CREATE INDEX ix ON t(n)", 0),
					fixture.Exec("CREATE INDEX sibling ON t((n+1))", 0),
					fixture.Exec("CREATE VIEW v AS SELECT n FROM t", 0),
					fixture.Exec("CREATE SEQUENCE seq", 0),
				} {
					run(s)
				}
				// The PG fixture recreates public without its original PUBLIC USAGE.
				// Admit real namespace lookup, but deliberately grant no MAINTAIN.
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO im_badcontrol", 0))
				}
				var index int64
				if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='ix'").Scan(&index); err != nil {
					t.Fatal(err)
				}
				var before bytes.Buffer
				if !b.PG {
					if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
						t.Fatal(err)
					}
				}
				checkState := func(t *testing.T) {
					t.Helper()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					if target.denied {
						run(fixture.Exec("RESET ROLE", 0))
					}
					run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
					run(fixture.Select("SELECT oid::bigint,relkind FROM pg_class WHERE relname='ix'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{index, "i"}}))
					for _, c := range []struct{ name, definition string }{{"ix", "CREATE INDEX ix ON public.t USING btree (n)"}, {"sibling", "CREATE INDEX sibling ON public.t USING btree (((n + 1)))"}} {
						s := fixture.Select("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname=$1", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{c.definition}})
						s.Args = []any{c.name}
						run(s)
					}
					if !b.PG {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
							t.Fatal("malformed controls changed native state", err, after.String())
						}
					}
					if target.denied {
						run(fixture.Exec("SET ROLE im_badcontrol", 0))
					}
				}
				if target.denied {
					run(fixture.Exec("SET ROLE im_badcontrol", 0))
				}
				for _, control := range controls {
					q := control.prefix + "REINDEX (" + control.options + ") " + target.clause
					t.Run(control.prefix+control.options, func(t *testing.T) {
						stmt, err := a.PrepareContext(p.Ctx, q)
						if err != nil {
							if stmt != nil {
								stmt.Close()
							}
							t.Fatal("semantic validation must not happen in quiet Prepare", err)
						}
						defer stmt.Close()
						for _, held := range []bool{false, true} {
							for _, query := range []bool{false, true} {
								s := fixture.ImplicitError(q, control.diagnostic)
								s.ErrorStage, s.SelectRows = "execute", query
								if held {
									fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}, b.Recorder, s)
								} else {
									fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
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
					s := fixture.Prepared(fixture.ImplicitError(q, target.validDiagnostic))
					s.ErrorStage = "execute"
					run(s)
					checkState(t)
				}
				if target.denied {
					run(fixture.Exec("RESET ROLE", 0))
				}
				// Preserve independently intentional native admission ahead of controls.
				for _, readOnly := range []bool{false, true} {
					begin := "BEGIN"
					if readOnly {
						begin = "BEGIN READ ONLY"
					}
					run(fixture.Exec(begin, 0))
					q := "REINDEX (unknown) " + target.clause
					want := fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "10"}
					if !b.PG {
						want = fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
						if readOnly {
							want = fixture.Diagnostic{Code: "25006", Message: "cannot execute REINDEX in a read-only transaction"}
						}
					}
					s := fixture.Prepared(fixture.ImplicitError(q, want))
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Exec("ROLLBACK", 0))
				}
			})
		})
	}
}
