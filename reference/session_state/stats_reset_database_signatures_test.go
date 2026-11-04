package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

var resetDatabaseFunctions = []struct {
	name   string
	oid    int64
	strict bool
}{
	{"pg_stat_reset", 2274, false},
	{"pg_stat_reset_single_table_counters", 3776, true},
	{"pg_stat_reset_single_function_counters", 3777, true},
}

func resetDatabaseExec(t *testing.T, p *fixture.Pair, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, query := range queries {
		activityExec(t, p, c, query)
	}
}
func resetDatabaseValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query, name string, value any, args ...any) {
	t.Helper()
	step := fixture.Select(query, []string{name}, []string{"VOID"}, [][]any{{value}}, args...)
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		resetSharedMetadata(t, b, rows)
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if _, known := columns[0].Length(); known {
			t.Fatal("VOID length")
		}
		if _, _, known := columns[0].DecimalSize(); known {
			t.Fatal("VOID precision")
		}
	}
	step.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		var raw any
		if err := rows.Scan(&raw); err != nil || fixture.Normalize(raw) != value {
			t.Fatal("VOID raw carrier", raw, err)
		}
		var text string
		err := rows.Scan(&text)
		if value == nil {
			if err == nil {
				t.Fatal("NULL VOID scanned into string")
			}
			var nullable sql.NullString
			if err := rows.Scan(&nullable); err != nil || nullable.Valid {
				t.Fatal("NULL VOID nullable scan", nullable, err)
			}
		} else if err != nil || text != "" {
			t.Fatal("VOID string scan", text, err)
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
}
func TestReferenceStatsResetDatabaseSignatures(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range resetDatabaseFunctions {
			arguments := []string{""}
			if f.strict {
				arguments = []string{"0::oid", "4294967295::oid", "2147483000::oid", "NULL", "NULL::oid", "'pg_class'::regclass::oid"}
			}
			for _, prepared := range []bool{false, true} {
				for _, argument := range arguments {
					var value any = ""
					if argument == "NULL" || argument == "NULL::oid" {
						value = nil
					}
					step := fixture.Select("SELECT "+f.name+"("+argument+")", []string{f.name}, []string{"VOID"}, [][]any{{value}})
					step.Prepared = prepared
					step.CheckMetadata = func(t *testing.T, r *sql.Rows) { resetSharedMetadata(t, b, r) }
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
			}
			argument, formal := "", ""
			if f.strict {
				argument = "0::oid"
				formal = "oid"
			}
			for _, q := range []string{"SELECT pg_catalog.\"" + f.name + "\"(" + argument + ") AS value", "SELECT * FROM pg_catalog." + f.name + "(" + argument + ") AS value"} {
				resetDatabaseValue(t, p, b, c, q, "value", "")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT "+f.name+"("+argument+")", 1))
			q := "SELECT pg_catalog." + f.name + "("
			if f.strict {
				q += "$1::oid"
			}
			q += ")"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			if f.strict {
				for _, value := range []any{int64(0), int64(4294967295), nil, int64(2147483000)} {
					var want any = ""
					if value == nil {
						want = nil
					}
					resetDatabaseValue(t, p, b, held, q, f.name, want, value)
				}
			} else {
				resetDatabaseValue(t, p, b, held, q, f.name, "")
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			resetDatabaseExec(t, p, c, "CREATE FUNCTION public."+f.name+"("+formal+") RETURNS text LANGUAGE SQL AS 'SELECT ''shadow''::text'", "SET search_path=public,pg_catalog")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT "+f.name+"("+argument+")", []string{f.name}, []string{"TEXT"}, [][]any{{"shadow"}}))
			resetDatabaseValue(t, p, b, c, "SELECT pg_catalog."+f.name+"("+argument+")", f.name, "")
			resetDatabaseExec(t, p, c, "SET search_path=pg_catalog,public", "DROP FUNCTION public."+f.name+"("+formal+")")
		}
		query := `SELECT oid::bigint,proname::text,pronargs::bigint,prorettype::bigint,proisstrict,provolatile::text,proparallel::text,proretset,proargtypes::text,proargnames::text,proargmodes::text,proallargtypes::text,pronargdefaults::bigint,provariadic::bigint,prosecdef,prolang::bigint,prosrc FROM pg_proc WHERE oid IN (2274,3776,3777) ORDER BY oid`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"oid", "proname", "pronargs", "prorettype", "proisstrict", "provolatile", "proparallel", "proretset", "proargtypes", "proargnames", "proargmodes", "proallargtypes", "pronargdefaults", "provariadic", "prosecdef", "prolang", "prosrc"}, []string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "INT8", "BOOL", "INT8", "TEXT"}, [][]any{
			{int64(2274), "pg_stat_reset", int64(0), int64(2278), false, "v", "s", false, "", nil, nil, nil, int64(0), int64(0), false, int64(12), "pg_stat_reset"},
			{int64(3776), "pg_stat_reset_single_table_counters", int64(1), int64(2278), true, "v", "s", false, "26", nil, nil, nil, int64(0), int64(0), false, int64(12), "pg_stat_reset_single_table_counters"},
			{int64(3777), "pg_stat_reset_single_function_counters", int64(1), int64(2278), true, "v", "s", false, "26", nil, nil, nil, int64(0), int64(0), false, int64(12), "pg_stat_reset_single_function_counters"},
		}))
	})
}
