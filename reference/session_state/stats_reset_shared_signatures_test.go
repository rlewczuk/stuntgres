package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var resetSharedFunctions = []struct {
	name, argument string
	oid            int64
	input          string
	strict         bool
	defaults       int64
}{
	{"pg_stat_reset_slru", "text", 2307, "25", false, 1},
	{"pg_stat_reset_shared", "text", 3775, "25", false, 1},
	{"pg_stat_reset_backend_stats", "integer", 6387, "23", true, 0},
}
var resetSharedTargets = []string{"archiver", "bgwriter", "checkpointer", "io", "recovery_prefetch", "slru", "wal"}
var resetSLRUTargets = []string{"commit_timestamp", "multixact_member", "multixact_offset", "notify", "serializable", "subtransaction", "transaction", "other", "", "TRANSACTION", "transaction ", "unknown"}

func resetSharedNull(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, args ...any) {
	t.Helper()
	step := fixture.Select(query, []string{"pg_stat_reset_backend_stats"}, []string{"VOID"}, [][]any{{nil}}, args...)
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { resetSharedMetadata(t, b, rows) }
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
}
func resetSharedMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 {
		t.Fatal(columns, err)
	}
	scan := reflect.TypeFor[string]()
	if b.PG {
		scan = reflect.TypeFor[any]()
	}
	if columns[0].ScanType() != scan {
		t.Fatal("VOID ScanType", columns[0].ScanType(), scan)
	}
	if _, known := columns[0].Nullable(); known {
		t.Fatal("computed VOID has known nullability")
	}
}
func TestReferenceStatsResetSharedSignatures(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var pid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid <= 0 {
			t.Fatal(pid, err)
		}
		for _, f := range resetSharedFunctions {
			arguments := []string{"NULL", "NULL::" + f.argument}
			if !f.strict {
				arguments = append(arguments, "", "target => NULL", "target := NULL")
			} else {
				arguments = append(arguments, "0", "-1", "2147483647", "pg_backend_pid()")
			}
			if f.name == "pg_stat_reset_shared" {
				for _, target := range resetSharedTargets {
					arguments = append(arguments, "'"+target+"'", "target => '"+target+"'")
				}
			}
			if f.name == "pg_stat_reset_slru" {
				for _, target := range resetSLRUTargets {
					arguments = append(arguments, "'"+target+"'", "target := '"+target+"'")
				}
			}
			for _, prepared := range []bool{false, true} {
				for _, argument := range arguments {
					query := "SELECT " + f.name + "(" + argument + ")"
					var value any = ""
					if f.strict && (argument == "NULL" || argument == "NULL::integer") {
						value = nil
					}
					step := fixture.Select(query, []string{f.name}, []string{"VOID"}, [][]any{{value}})
					step.Prepared = prepared
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { resetSharedMetadata(t, b, rows) }
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
			}
			arg := "NULL"
			if f.strict {
				arg = "0"
			}
			resetReplicationVoid(t, p, b, c, "SELECT pg_catalog.\""+f.name+"\"("+arg+"::"+f.argument+") AS reset_value", "reset_value")
			resetReplicationVoid(t, p, b, c, "SELECT * FROM pg_catalog."+f.name+"("+arg+"::"+f.argument+")", f.name)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT "+f.name+"("+arg+"::"+f.argument+")", 1))
			query := "SELECT pg_catalog." + f.name + "($1::" + f.argument + ")"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			values := []any{"wal", nil, "io", nil}
			if f.name == "pg_stat_reset_slru" {
				values = []any{"transaction", "unknown", nil, ""}
			}
			if f.strict {
				values = []any{pid, int64(0), nil, int64(-1)}
			}
			for _, value := range values {
				if f.strict && value == nil {
					resetSharedNull(t, p, b, held, query, value)
				} else {
					resetReplicationVoid(t, p, b, held, query, f.name, value)
				}
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			activityExec(t, p, c, "CREATE FUNCTION public."+f.name+"("+f.argument+") RETURNS text LANGUAGE SQL AS 'SELECT ''shadow''::text'")
			activityExec(t, p, c, "SET search_path=public,pg_catalog")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT "+f.name+"(NULL::"+f.argument+")", []string{f.name}, []string{"TEXT"}, [][]any{{"shadow"}}))
			resetReplicationVoid(t, p, b, c, "SELECT pg_catalog."+f.name+"("+arg+"::"+f.argument+")", f.name)
			activityExec(t, p, c, "SET search_path=pg_catalog,public")
			activityExec(t, p, c, "DROP FUNCTION public."+f.name+"("+f.argument+")")
		}
		query := `SELECT oid::bigint AS oid,proname::text AS name,pronargs::bigint AS nargs,prorettype::bigint AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,proretset AS is_set,proargtypes::text AS inputs,proargnames::text AS names,proargmodes::text AS modes,proallargtypes::text AS alltypes,pronargdefaults::bigint AS defaults,provariadic::bigint AS variadic,prosecdef AS definer,prolang::bigint AS language,prosrc AS source FROM pg_proc WHERE oid IN (2307,3775,6387) ORDER BY oid`
		var rows [][]any
		for _, f := range resetSharedFunctions {
			var names any = "{target}"
			if f.strict {
				names = nil
			}
			rows = append(rows, []any{f.oid, f.name, int64(1), int64(2278), f.strict, "v", "s", false, f.input, names, nil, nil, f.defaults, int64(0), false, int64(12), f.name})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"oid", "name", "nargs", "result", "strict", "volatility", "parallel", "is_set", "inputs", "names", "modes", "alltypes", "defaults", "variadic", "definer", "language", "source"}, []string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "INT8", "BOOL", "INT8", "TEXT"}, rows))
	})
}
