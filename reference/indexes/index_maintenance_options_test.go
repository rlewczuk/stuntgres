package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexMaintenanceOptions(t *testing.T) {
	cases := []struct{ clause, code, message, detail string }{
		{"SET(unknown=1)", "22023", `unrecognized parameter "unknown"`, ""},
		{"SET(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
		{"SET(fillfactor=101)", "22023", `value 101 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
		{"SET(fillfactor=-1)", "22023", `value -1 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
		{"SET(fillfactor='bad')", "22023", `invalid value for integer option "fillfactor": bad`, ""},
		{"SET(fillfactor)", "22023", `invalid value for integer option "fillfactor": true`, ""},
		{"SET(deduplicate_items='bad')", "22023", `invalid value for boolean option "deduplicate_items": bad`, ""},
		{"SET(fillfactor=80,fillfactor=90)", "22023", `parameter "fillfactor" specified more than once`, ""},
		{"RESET(fillfactor=80)", "42601", "RESET must not include values for parameters", ""},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(c.clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(n INT)", 0))
					run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
					s := fixture.ImplicitError("ALTER INDEX ix "+c.clause, fixture.Diagnostic{Code: c.code, Message: c.message, Detail: c.detail})
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (n)"}}))
				})
			})
		}
	}
	physical := []struct {
		clause, options string
		reloptions      any
	}{
		{"SET(fillfactor=10)", " WITH (fillfactor='10')", "{fillfactor=10}"},
		{"SET(fillfactor=80)", " WITH (fillfactor='80')", "{fillfactor=80}"},
		{"SET(fillfactor=100)", " WITH (fillfactor='100')", "{fillfactor=100}"},
		{"SET(fillfactor=80.0)", " WITH (fillfactor='80.0')", "{fillfactor=80.0}"},
		{"SET(deduplicate_items=on)", " WITH (deduplicate_items='on')", "{deduplicate_items=on}"},
		{"SET(deduplicate_items=off)", " WITH (deduplicate_items=off)", "{deduplicate_items=off}"},
		{"SET(deduplicate_items=true)", " WITH (deduplicate_items='true')", "{deduplicate_items=true}"},
		{"SET(deduplicate_items=false)", " WITH (deduplicate_items='false')", "{deduplicate_items=false}"},
		{"SET(deduplicate_items=1)", " WITH (deduplicate_items='1')", "{deduplicate_items=1}"},
		{"SET(deduplicate_items=0)", " WITH (deduplicate_items='0')", "{deduplicate_items=0}"},
		{"SET(deduplicate_items='yes')", " WITH (deduplicate_items=yes)", "{deduplicate_items=yes}"},
		{"SET(deduplicate_items='no')", " WITH (deduplicate_items=no)", "{deduplicate_items=no}"},
		{"SET(deduplicate_items='t')", " WITH (deduplicate_items=t)", "{deduplicate_items=t}"},
		{"SET(deduplicate_items='f')", " WITH (deduplicate_items=f)", "{deduplicate_items=f}"},
		{"SET(deduplicate_items)", " WITH (deduplicate_items='true')", "{deduplicate_items=true}"},
		{"RESET(fillfactor)", "", nil}, {"RESET(deduplicate_items)", "", nil},
		{"RESET(unknown)", "", nil}, {"RESET(toast.fillfactor)", "", nil}, {"RESET(fillfactor,fillfactor)", "", nil},
	}
	for _, c := range physical {
		t.Run(c.clause, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(1),(NULL)", 3))
				run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
				var before bytes.Buffer
				if !b.PG {
					if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
						t.Fatal(err)
					}
				}
				q := "ALTER INDEX ix " + c.clause
				s := fixture.Exec(q, 0)
				if !b.PG {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "index storage parameters are unsupported"})
				}
				run(fixture.Prepared(s))
				if b.PG {
					run(fixture.Select("SELECT reloptions::text,pg_get_indexdef(oid) FROM pg_class WHERE relname='ix'", []string{"reloptions", "pg_get_indexdef"}, []string{"TEXT", "TEXT"}, [][]any{{c.reloptions, "CREATE INDEX ix ON public.t USING btree (n)" + c.options}}))
				} else {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
						t.Fatal("physical request changed native state", err, after.String())
					}
				}
				run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
			})
		})
	}
}

// Whitespace controls use the full pinned PG18 records from tests-2. Known
// false controls are excluded syntax, not invented concurrent physical effects.
func TestReferenceIndexMaintenanceOptionControls(t *testing.T) {
	cases := []struct {
		query                      string
		diagnostic                 fixture.Diagnostic
		nativeMessage, nativeToken string
		statistics                 bool
	}{
		{query: "ALTER INDEX ix SET(toast.fillfactor=80)", nativeMessage: "index storage parameters are unsupported"},
		{query: "ALTER INDEX ix RESET(toast.fillfactor)", nativeMessage: "index storage parameters are unsupported"},
		{query: "REINDEX (verbose 'bad') INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: "verbose requires a Boolean value"}},
		{query: "REINDEX (verbose '0') INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: "verbose requires a Boolean value"}},
		{query: "REINDEX (tablespace) INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: "tablespace requires a parameter"}},
		{query: "REINDEX (verbose false) INDEX ix", nativeMessage: "REINDEX options are unsupported", nativeToken: "verbose"},
		{query: "REINDEX (verbose true) TABLE t", nativeMessage: "REINDEX options are unsupported", nativeToken: "verbose"},
		{query: "REINDEX (concurrently false) INDEX ix", nativeMessage: "REINDEX options are unsupported", nativeToken: "concurrently"},
		{query: "REINDEX (concurrently false) TABLE t", nativeMessage: "REINDEX options are unsupported", nativeToken: "concurrently"},
		{query: "REINDEX (concurrently true) INDEX ix", nativeMessage: "concurrent index operations are unsupported", nativeToken: "concurrently"},
		{query: "REINDEX (tablespace pg_default) INDEX ix", nativeMessage: "index tablespaces are unsupported", nativeToken: "tablespace"},
		{query: "REINDEX (tablespace pg_default) TABLE t", nativeMessage: "index tablespaces are unsupported", nativeToken: "tablespace"},
		{query: "REINDEX (parallel 0) INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "parallel"`, Position: "10"}},
		{query: "REINDEX (parallel -1) INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "parallel"`, Position: "10"}},
		{query: "REINDEX (verbose) INDEX ix", nativeMessage: "REINDEX options are unsupported", nativeToken: "verbose"},
		{query: "REINDEX (concurrently) INDEX ix", nativeMessage: "concurrent index operations are unsupported", nativeToken: "concurrently"},
		{query: "REINDEX (verbose 'yes') INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: "verbose requires a Boolean value"}},
		{query: "REINDEX (verbose 0) INDEX ix", nativeMessage: "REINDEX options are unsupported", nativeToken: "verbose"},
		{query: "ALTER INDEX ei ALTER COLUMN 1 SET STATISTICS 10", nativeMessage: "index statistics are unsupported", nativeToken: "STATISTICS", statistics: true},
		{query: "REINDEX DATABASE CURRENT_DATABASE", nativeMessage: "REINDEX target is unsupported", nativeToken: "DATABASE"},
		{query: "REINDEX SYSTEM CURRENT_DATABASE", nativeMessage: "REINDEX target is unsupported", nativeToken: "SYSTEM"},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(c.query+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("CREATE INDEX ix ON t(n)", 0), fixture.Exec("CREATE INDEX ei ON t((n+1))", 0)} {
						run(s)
					}
					var before bytes.Buffer
					if !b.PG {
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
					}
					q := c.query
					if strings.HasSuffix(q, " CURRENT_DATABASE") {
						var db string
						if err := b.DB.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); err != nil {
							t.Fatal(err)
						}
						q = strings.TrimSuffix(q, "CURRENT_DATABASE") + `"` + strings.ReplaceAll(db, `"`, `""`) + `"`
					}
					s := fixture.Exec(q, 0)
					if c.diagnostic.Code != "" {
						s = fixture.ImplicitError(q, c.diagnostic)
						s.ErrorStage = "execute"
					}
					if !b.PG && c.nativeMessage != "" {
						position := ""
						if c.nativeToken != "" {
							position = fixture.Position(strings.Index(q, c.nativeToken) + 1)
						}
						s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: c.nativeMessage, Position: position})
						s.ErrorStage = "execute"
					}
					if prepared {
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal("semantic control failed quiet Prepare", err)
						}
						defer stmt.Close()
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
					} else {
						run(s)
					}
					if b.PG {
						run(fixture.Select("SELECT relname,reloptions::text,reltablespace::bigint,pg_get_indexdef(oid) FROM pg_class WHERE relname IN ('ix','ei') ORDER BY relname", []string{"relname", "reloptions", "reltablespace", "pg_get_indexdef"}, []string{"NAME", "TEXT", "INT8", "TEXT"}, [][]any{
							{"ei", nil, int64(0), "CREATE INDEX ei ON public.t USING btree (((n + 1)))"}, {"ix", nil, int64(0), "CREATE INDEX ix ON public.t USING btree (n)"},
						}))
						var target any
						if c.statistics {
							target = int64(10)
						}
						run(fixture.Select("SELECT attstattarget FROM pg_attribute WHERE attrelid='ei'::regclass AND attnum=1", []string{"attstattarget"}, []string{"INT2"}, [][]any{{target}}))
					} else {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
							t.Fatal("rejected option changed state", err, after.String())
						}
						run(fixture.Select("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (n)"}}))
						run(fixture.Select("SELECT pg_get_indexdef('ei'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ei ON public.t USING btree (((n + 1)))"}}))
					}
					run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, nil))
					// SAME session is healthy after semantic failure and future key work still
					// observes committed data; the statistics success remains PG-only physical.
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(fixture.Prepared(fixture.Exec("REINDEX INDEX ix", 0)))
					run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		}
	}
}

func TestReferenceIndexMaintenanceCapabilityControls(t *testing.T) {
	cases := []struct {
		query, message, token string
		pgDiagnostic          fixture.Diagnostic
		pgDrop                bool
	}{
		{query: "ALTER INDEX ix SET TABLESPACE pg_default", message: "index tablespaces are unsupported", token: "TABLESPACE"},
		{query: "REINDEX INDEX CONCURRENTLY ix", message: "concurrent index operations are unsupported", token: "CONCURRENTLY"},
		{query: "DROP INDEX CONCURRENTLY ix", message: "concurrent index operations are unsupported", token: "CONCURRENTLY", pgDrop: true},
		{query: "REINDEX SCHEMA public", message: "REINDEX target is unsupported", token: "SCHEMA"},
		{query: "REINDEX DATABASE stuntgres", message: "REINDEX target is unsupported", token: "DATABASE", pgDiagnostic: fixture.Diagnostic{Code: "0A000", Message: "can only reindex the currently open database"}},
		{query: "REINDEX SYSTEM stuntgres", message: "REINDEX target is unsupported", token: "SYSTEM", pgDiagnostic: fixture.Diagnostic{Code: "0A000", Message: "can only reindex the currently open database"}},
		{query: "ALTER INDEX ix ALTER COLUMN 1 SET STATISTICS 10", message: "index statistics are unsupported", token: "STATISTICS", pgDiagnostic: fixture.Diagnostic{Code: "0A000", Message: `cannot alter statistics on non-expression column "n" of index "ix"`, Hint: "Alter statistics on table column instead."}},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(c.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(n INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
					var before bytes.Buffer
					if !b.PG {
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
					}
					s := fixture.Exec(c.query, 0)
					if b.PG && c.pgDiagnostic.Code != "" {
						s = fixture.ImplicitError(c.query, c.pgDiagnostic)
					}
					if !b.PG {
						s = fixture.ImplicitError(c.query, fixture.Diagnostic{Code: "0A000", Message: c.message, Position: fixture.Position(strings.Index(c.query, c.token) + 1)})
					}
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					if b.PG {
						expected := [][]any{{nil, int64(0), "CREATE INDEX ix ON public.t USING btree (n)"}}
						if c.pgDrop {
							expected = nil
						}
						run(fixture.Select("SELECT reloptions::text,reltablespace::bigint,pg_get_indexdef(oid) FROM pg_class WHERE relname='ix'", []string{"reloptions", "reltablespace", "pg_get_indexdef"}, []string{"TEXT", "INT8", "TEXT"}, expected))
					} else {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != before.String() {
							t.Fatal("capability rejection changed state", err, after.String())
						}
					}
					run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		}
	}
}
