package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexMaintenanceDiagnostics(t *testing.T) {
	cases := []struct{ query, code, message, hint string }{
		{"DROP INDEX missing", "42704", `index "missing" does not exist`, ""},
		{"DROP INDEX absent_schema.ix", "3F000", `schema "absent_schema" does not exist`, ""},
		{"DROP INDEX t", "42809", `"t" is not an index`, "Use DROP TABLE to remove a table."},
		{"DROP INDEX seq", "42809", `"seq" is not an index`, "Use DROP SEQUENCE to remove a sequence."},
		{"DROP INDEX v", "42809", `"v" is not an index`, "Use DROP VIEW to remove a view."},
		{"ALTER INDEX missing RENAME TO fresh", "42P01", `relation "missing" does not exist`, ""},
		{"ALTER INDEX ix RENAME TO ix", "42P07", `relation "ix" already exists`, ""},
		{"ALTER INDEX ix RENAME TO t", "42P07", `relation "t" already exists`, ""},
		{"ALTER INDEX ix RENAME TO seq", "42P07", `relation "seq" already exists`, ""},
		{"ALTER INDEX ix RENAME TO v", "42P07", `relation "v" already exists`, ""},
		{"REINDEX INDEX missing", "42P01", `relation "missing" does not exist`, ""},
		{"REINDEX TABLE missing", "42P01", `relation "missing" does not exist`, ""},
		{"REINDEX INDEX t", "42809", `"t" is not an index`, ""},
		{"REINDEX TABLE ix", "42809", `"ix" is not a table or materialized view`, ""},
		{"DROP INDEX ix,missing", "42704", `index "missing" does not exist`, ""},
		{"DROP INDEX ix,t", "42809", `"t" is not an index`, "Use DROP TABLE to remove a table."},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			for _, plain := range []bool{false, true} {
				t.Run(c.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/context", true: "/plain"}[plain], func(t *testing.T) {
					indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						run := func(s fixture.Step) { s.NonContext = plain; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						for _, q := range []string{"CREATE TABLE t(n INT)", "CREATE INDEX ix ON t(n)", "CREATE SEQUENCE seq", "CREATE VIEW v AS SELECT n FROM t"} {
							run(fixture.Exec(q, 0))
						}
						s := fixture.ImplicitError(c.query, fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint})
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, [][]any{{"ix"}}))
					})
				})
			}
		}
	}
}

// Complete records below were acquired from the pinned PG18 root oracle.
func TestReferenceIndexMaintenanceDiagnosticPrecedence(t *testing.T) {
	cases := []struct {
		query      string
		diagnostic fixture.Diagnostic
		parse      bool
		changed    string
	}{
		{query: "DROP INDEX ix,ix", changed: "drop"},
		{query: "DROP INDEX ix,ix CASCADE", changed: "drop"},
		{query: "DROP INDEX IF EXISTS ix,missing,ix", changed: "drop"},
		{query: "DROP INDEX absent_schema.ix", diagnostic: fixture.Diagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`}},
		{query: "ALTER INDEX absent_schema.ix RENAME TO fresh", diagnostic: fixture.Diagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`}},
		{query: "DROP INDEX public.u CASCADE", diagnostic: fixture.Diagnostic{Code: "2BP01", Message: "cannot drop index u because constraint u on table t requires it", Hint: "You can drop constraint u on table t instead."}},
		{query: "DROP INDEX ix,u CASCADE", diagnostic: fixture.Diagnostic{Code: "2BP01", Message: "cannot drop index u because constraint u on table t requires it", Hint: "You can drop constraint u on table t instead."}},
		{query: "ALTER INDEX u RENAME TO check_name", diagnostic: fixture.Diagnostic{Code: "42710", Message: `constraint "check_name" for relation "t" already exists`}},
		{query: "ALTER INDEX u RENAME TO nn_name", diagnostic: fixture.Diagnostic{Code: "42710", Message: `constraint "nn_name" for relation "t" already exists`}},
		{query: "ALTER INDEX u RENAME TO ix", diagnostic: fixture.Diagnostic{Code: "42P07", Message: `relation "ix" already exists`}},
		{query: "ALTER INDEX t RENAME TO table_alias", changed: "table"},
		{query: "ALTER INDEX seq RENAME TO sequence_alias", changed: "sequence"},
		{query: "ALTER INDEX v RENAME TO view_alias", changed: "view"},
		{query: "ALTER INDEX t SET(fillfactor=80)", diagnostic: fixture.Diagnostic{Code: "42809", Message: `"t" is not an index`}},
		{query: "ALTER INDEX t RESET(fillfactor)", diagnostic: fixture.Diagnostic{Code: "42809", Message: `"t" is not an index`}},
		{query: "ALTER INDEX missing SET(fillfactor=9)", diagnostic: fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`}},
		{query: "ALTER INDEX missing SET(unknown=1)", diagnostic: fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`}},
		{query: "ALTER INDEX IF EXISTS missing SET(fillfactor=9)"},
		{query: "ALTER INDEX IF EXISTS missing RESET(fillfactor=80)"},
		{query: "ALTER INDEX ix SET(fillfactor=9,fillfactor=80)", diagnostic: fixture.Diagnostic{Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`}},
		{query: "ALTER INDEX ix SET(fillfactor=80,fillfactor=9)", diagnostic: fixture.Diagnostic{Code: "22023", Message: `parameter "fillfactor" specified more than once`}},
		{query: "ALTER INDEX ix RESET(unknown)"},
		{query: "ALTER INDEX ix RESET(toast.fillfactor)"},
		{query: "REINDEX (verbose='bad') INDEX ix", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "17"}},
		{query: "REINDEX (concurrently=false) INDEX ix", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "22"}},
		{query: "REINDEX (verbose=false) INDEX ix", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "17"}},
		{query: "REINDEX (tablespace=pg_default) INDEX ix", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "20"}},
		{query: "REINDEX (parallel=0) INDEX ix", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "18"}},
		{query: "REINDEX (parallel=-1) INDEX ix", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "="`, Position: "18"}},
		{query: "REINDEX (unknown) INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "10"}},
		{query: "/* é */ REINDEX (unknown) INDEX ix", diagnostic: fixture.Diagnostic{Code: "42601", Message: `unrecognized REINDEX option "unknown"`, Position: "18"}},
		{query: "REINDEX INDEX v", diagnostic: fixture.Diagnostic{Code: "42809", Message: `"v" is not an index`}},
		{query: "REINDEX TABLE seq", diagnostic: fixture.Diagnostic{Code: "42809", Message: `"seq" is not a table or materialized view`}},
		{query: "REINDEX TABLE v", diagnostic: fixture.Diagnostic{Code: "42809", Message: `"v" is not a table or materialized view`}},
		{query: "ALTER INDEX ix RENAME TO fresh; SELECT FROM", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "44"}},
		{query: "DROP INDEX ix; SELECT FROM", parse: true, diagnostic: fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "27"}},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(c.query+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT CONSTRAINT nn_name NOT NULL,CONSTRAINT check_name CHECK(n>0),CONSTRAINT u UNIQUE(n))", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Exec("CREATE INDEX ix ON t(n)", 0), fixture.Exec("CREATE SEQUENCE seq", 0), fixture.Exec("CREATE VIEW v AS SELECT n FROM t", 0)} {
						run(s)
					}
					s := fixture.Exec(c.query, 0)
					if c.diagnostic.Code != "" {
						s = fixture.ImplicitError(c.query, c.diagnostic)
						s.ErrorStage = "execute"
					}
					if prepared {
						if c.parse {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
							run(s)
						} else {
							stmt, err := b.DB.PrepareContext(p.Ctx, c.query)
							if err != nil {
								t.Fatal("semantic utility failed Prepare", err)
							}
							defer stmt.Close()
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: c.query}, b.Recorder, s)
						}
					} else {
						run(s)
					}
					want := [][]any{{"ix", "i"}, {"t", "r"}, {"u", "i"}}
					switch c.changed {
					case "drop":
						want = [][]any{{"t", "r"}, {"u", "i"}}
					case "table":
						want = [][]any{{"ix", "i"}, {"table_alias", "r"}, {"u", "i"}}
					case "sequence":
						want = [][]any{{"ix", "i"}, {"sequence_alias", "S"}, {"t", "r"}, {"u", "i"}}
					case "view":
						want = [][]any{{"ix", "i"}, {"t", "r"}, {"u", "i"}, {"view_alias", "v"}}
					}
					for _, name := range []string{"ix", "fresh", "u", "check_name", "nn_name", "t", "table_alias", "sequence_alias", "view_alias"} {
						var rows [][]any
						for _, row := range want {
							if row[0] == name {
								rows = append(rows, row)
							}
						}
						witness := fixture.Select("SELECT relname,relkind FROM pg_class WHERE relname=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, rows)
						witness.Args = []any{name}
						run(witness)
					}
				})
			})
		}
	}
}

func TestReferenceIndexMaintenanceNotices(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var records []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &records)
		for _, q := range []string{"DROP INDEX IF EXISTS missing", "DROP INDEX IF EXISTS absent_schema.ix", "ALTER INDEX IF EXISTS missing RENAME TO fresh", "CREATE TABLE t(n INT)", "REINDEX TABLE t"} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
		}
		// Inferred complete NOTICE contract; allocation and C provenance excluded.
		wants := []fixture.Diagnostic{
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `index "missing" does not exist, skipping`},
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "absent_schema" does not exist, skipping`},
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`},
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "t" has no indexes to reindex`},
		}
		if len(records) != len(wants) {
			t.Fatalf("notices %#v want %#v", records, wants)
		}
		for i := range wants {
			if records[i] != wants[i] {
				t.Errorf("notice %d got %#v want %#v", i, records[i], wants[i])
			}
		}
	})
}
