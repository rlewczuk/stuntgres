package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWSupportFunctions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				run(fixture.Exec("CREATE FUNCTION bad_handler() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", 0))
				run(fixture.Exec(`CREATE FUNCTION "Bad Handler"() RETURNS integer LANGUAGE SQL AS 'SELECT 1'`, 0))
				run(fixture.Exec("CREATE FUNCTION int_validator(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT 7'", 0))
				run(fixture.Exec("CREATE FUNCTION wrong_validator(text,oid) RETURNS integer LANGUAGE SQL AS 'SELECT 7'", 0))
				for _, specified := range []string{"bad_handler", "public.bad_handler", `public."Bad Handler"`} {
					for _, action := range []string{"CREATE", "ALTER"} {
						if action == "ALTER" {
							run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta OPTIONS(keep 'old')", 0))
						}
						displayName := specified
						if specified == `public."Bad Handler"` {
							displayName = "public.Bad Handler"
						}
						s := fixture.ErrorDiagnostic(action+" FOREIGN DATA WRAPPER fdw_meta HANDLER "+specified, fdwDiagnostic("42809", "function "+displayName+" must return type fdw_handler"))
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						rows := [][]any{}
						if action == "ALTER" {
							rows = [][]any{{"fdw_meta", true, true, "{keep=old}"}}
						}
						metadata := fixture.Select("SELECT fdwname,fdwhandler=0 AS no_handler,fdwvalidator=0 AS no_validator,fdwoptions::text AS options FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta'", []string{"fdwname", "no_handler", "no_validator", "options"}, []string{"NAME", "BOOL", "BOOL", "TEXT"}, rows)
						metadata.Prepared = prepared
						run(metadata)
						if action == "ALTER" {
							run(fixture.Exec("DROP FOREIGN DATA WRAPPER fdw_meta", 0))
						}
					}
				}
				s := fixture.ErrorDiagnostic("CREATE FOREIGN DATA WRAPPER fdw_meta VALIDATOR wrong_validator", fdwDiagnostic("42883", "function wrong_validator(text[], oid) does not exist"))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				create := fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta VALIDATOR int_validator OPTIONS(a 'one')", 0)
				create.Prepared = prepared
				run(create)
				metadata := fixture.Select("SELECT w.fdwname,w.fdwhandler=0 AS no_handler,w.fdwvalidator=p.oid AS same_validator,w.fdwoptions::text AS options FROM pg_foreign_data_wrapper w JOIN pg_proc p ON p.proname='int_validator' WHERE w.fdwname='fdw_meta'", []string{"fdwname", "no_handler", "same_validator", "options"}, []string{"NAME", "BOOL", "BOOL", "TEXT"}, [][]any{{"fdw_meta", true, true, "{a=one}"}})
				metadata.Prepared = prepared
				run(metadata)
			})
		})
	}
}

func TestReferenceFDWSupportOrder(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, `DROP FOREIGN DATA WRAPPER IF EXISTS "é" CASCADE`); err != nil {
				t.Error("support-order cleanup:", err)
			}
		}()
		run(fixture.Exec("CREATE FUNCTION order_validator(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT 7'", 0))
		handler := "fdw_sql_handler"
		if b.PG {
			run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS postgres_fdw", 0))
			handler = "postgres_fdw_handler"
		}
		for _, kind := range []string{"HANDLER", "VALIDATOR"} {
			function := handler
			other := "VALIDATOR"
			if kind == "VALIDATOR" {
				other = "HANDLER"
			}
			if kind == "VALIDATOR" {
				function = "order_validator"
			}
			for _, clauses := range []struct{ first, second string }{
				{kind + " " + function, "NO " + kind},
				{"NO " + kind, kind + " " + function},
				{kind + " " + function, kind + " " + function},
				{"NO " + kind, "NO " + kind},
				{"NO " + kind + " NO " + other, "NO " + kind},
				{"NO " + kind, kind + " missing_second"},
			} {
				for _, action := range []string{"CREATE", "ALTER"} {
					for _, prepared := range []bool{false, true} {
						if action == "ALTER" {
							run(fixture.Exec(`CREATE FOREIGN DATA WRAPPER "é" OPTIONS(keep 'old')`, 0))
						}
						prefix := action + ` FOREIGN DATA WRAPPER "é" ` + clauses.first + " "
						q := prefix + clauses.second
						d := fdwDiagnostic("42601", "conflicting or redundant options")
						d.Position = fixture.Position(len([]rune(prefix)) + 1)
						s := fixture.ErrorDiagnostic(q, d)
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						rows := [][]any{}
						if action == "ALTER" {
							rows = [][]any{{true, true, "{keep=old}"}}
						}
						run(fixture.Select(`SELECT fdwhandler=0 AS no_handler,fdwvalidator=0 AS no_validator,fdwoptions::text AS options FROM pg_foreign_data_wrapper WHERE fdwname='é'`, []string{"no_handler", "no_validator", "options"}, []string{"BOOL", "BOOL", "TEXT"}, rows))
						if action == "ALTER" {
							run(fixture.Exec(`DROP FOREIGN DATA WRAPPER "é"`, 0))
						}
					}
				}
			}
		}
		for _, action := range []string{"CREATE", "ALTER"} {
			if action == "ALTER" {
				run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta", 0))
			}
			for _, tc := range []struct{ clauses, missing string }{
				{"HANDLER missing_handler NO HANDLER", "missing_handler()"},
				{"VALIDATOR missing_validator NO VALIDATOR", "missing_validator(text[], oid)"},
				{"VALIDATOR missing_validator HANDLER missing_handler", "missing_validator(text[], oid)"},
				{"HANDLER missing_handler VALIDATOR missing_validator", "missing_handler()"},
				{"NO HANDLER VALIDATOR missing_validator NO HANDLER", "missing_validator(text[], oid)"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(action+" FOREIGN DATA WRAPPER fdw_meta "+tc.clauses, fdwDiagnostic("42883", "function "+tc.missing+" does not exist"))
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					rows := [][]any{}
					if action == "ALTER" {
						rows = [][]any{{true, true, true}}
					}
					run(fixture.Select("SELECT fdwhandler=0 AS no_handler,fdwvalidator=0 AS no_validator,fdwoptions IS NULL AS no_options FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta'", []string{"no_handler", "no_validator", "no_options"}, []string{"BOOL", "BOOL", "BOOL"}, rows))
				}
			}
		}
		q := "CREATE FOREIGN DATA WRAPPER malformed NO HANDLER trailing"
		d := fdwDiagnostic("42601", `syntax error at or near "trailing"`)
		d.Position = fixture.Position(strings.Index(q, "trailing") + 1)
		s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
		s.ErrorStage = "prepare"
		run(s)
	})
}
