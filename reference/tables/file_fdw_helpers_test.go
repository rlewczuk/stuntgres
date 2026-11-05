package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Each backend/route owns its global extension, wrapper, schemas and roles.
// No provider registration or external file/program operation is involved.
func fileFDWScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					cleanup := func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						queries := []string{"ROLLBACK", "RESET ROLE", "RESET search_path", "DROP EXTENSION IF EXISTS file_fdw CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS ff_wrapper CASCADE", "DROP SERVER IF EXISTS ff_server CASCADE", "DROP SERVER IF EXISTS ff_renamed_server CASCADE"}
						queries = append(queries, "DROP FUNCTION IF EXISTS public.file_fdw_validator(text[],oid)", "DROP FUNCTION IF EXISTS public.file_fdw_handler()")
						for _, schema := range []string{"ff_helpers", "ff_moved", "ff_destination", "ff_owner_namespace"} {
							queries = append(queries, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
						}
						for _, table := range []string{"ff_table", "ff_rejected", "ff_program", "ff_binary", "ff_empty", "ff_text", "ff_local", "ff_renamed_table", "ff_staged", "ff_pair_staged", "ff_options_staged", "ff_new_parent", "ff_new_child", "ff_parent", "ff_child", "ff_partition_parent", "ff_compact", "ff_attached"} {
							queries = append(queries, "DROP TABLE IF EXISTS "+table+" CASCADE")
						}
						for _, q := range queries {
							if _, err := c.ExecContext(ctx, q); err != nil {
								t.Error("file_fdw cleanup:", q, err)
							}
						}
						queries = nil
						for _, role := range []string{"ff_actor", "ff_middle", "ff_noinherit", "ff_file", "ff_program_role", "ff_owner"} {
							// DROP OWNED cannot name an absent role. Presence is checked
							// explicitly instead of suppressing cleanup failures.
							var exists bool
							if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
								t.Error("file_fdw cleanup role lookup:", err)
								continue
							}
							if exists {
								queries = append(queries, "DROP OWNED BY "+role+" CASCADE", "DROP ROLE "+role)
							}
						}
						for _, q := range queries {
							if _, err := c.ExecContext(ctx, q); err != nil {
								t.Error("file_fdw cleanup:", q, err)
							}
						}
					}
					cleanup()
					defer cleanup()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					body(t, p, b, c, prepared, run)
				})
			}
		})
	}
}

func fileFDWInstall(run func(fixture.Step)) {
	run(fixture.Exec("CREATE EXTENSION file_fdw", 0))
	run(fixture.Exec("CREATE SERVER ff_server FOREIGN DATA WRAPPER file_fdw", 0))
}

func fileFDWError(query, code, primary string) fixture.Step {
	s := fixture.ErrorDiagnostic(query, fdwDiagnostic(code, primary))
	s.ErrorStage = "execute"
	return s
}

func fileFDWOptions(table string, entries ...string) fixture.Step {
	var value any
	if len(entries) != 0 {
		value = relationOptionsArray(entries)
	}
	return fixture.Select("SELECT ftoptions FROM pg_foreign_table WHERE ftrelid='"+table+"'::regclass", []string{"ftoptions"}, []string{"_TEXT"}, [][]any{{value}})
}

func fileFDWLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func fileFDWCall(entries []string, context int) string {
	values := make([]string, len(entries))
	for i, entry := range entries {
		values[i] = fileFDWLiteral(entry)
	}
	return "SELECT file_fdw_validator(ARRAY[" + strings.Join(values, ",") + "]::text[]," + fileFDWContext(context) + "::oid) AS validated"
}

func fileFDWContext(context int) string {
	switch context {
	case 3118:
		return "3118"
	case 1249:
		return "1249"
	case 2328:
		return "2328"
	case 1417:
		return "1417"
	case 1418:
		return "1418"
	default:
		return "0"
	}
}

func fileFDWVoid(query string, args ...any) fixture.Step {
	return fixture.Select(query, []string{"validated"}, []string{"VOID"}, [][]any{{""}}, args...)
}

func fileFDWAbsent(run func(fixture.Step)) {
	run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='ff_rejected'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(fixture.Select("SELECT count(*) FROM pg_type WHERE typname IN('ff_rejected','_ff_rejected')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func fileFDWGraph(run func(fixture.Step), wrapper, handler, validator string) {
	run(fixture.Select("SELECT e.extname,e.extversion,e.extrelocatable,obj_description(e.oid,'pg_extension') AS description FROM pg_extension e WHERE e.extname='file_fdw'", []string{"extname", "extversion", "extrelocatable", "description"}, []string{"NAME", "TEXT", "BOOL", "TEXT"}, [][]any{{"file_fdw", "1.0", true, "foreign-data wrapper for flat file access"}}))
	run(fixture.Select("SELECT w.fdwname,w.fdwhandler=h.oid AS handler_link,w.fdwvalidator=v.oid AS validator_link,w.fdwoptions IS NULL AS no_options,w.fdwowner=e.extowner AS same_owner FROM pg_foreign_data_wrapper w JOIN pg_proc h ON h.oid="+fileFDWLiteral(handler)+"::regprocedure JOIN pg_proc v ON v.oid="+fileFDWLiteral(validator)+"::regprocedure JOIN pg_extension e ON e.extname='file_fdw' WHERE w.fdwname="+fileFDWLiteral(wrapper), []string{"fdwname", "handler_link", "validator_link", "no_options", "same_owner"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{wrapper, true, true, true, true}}))
	run(fixture.Select("SELECT d.classid=1255::oid AS routine,d.classid=2328::oid AS wrapper,count(*) AS members FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid=3079::oid AND e.extname='file_fdw' AND d.deptype='e' GROUP BY d.classid ORDER BY d.classid", []string{"routine", "wrapper", "members"}, []string{"BOOL", "BOOL", "INT8"}, [][]any{{true, false, int64(2)}, {false, true, int64(1)}}))
}
