package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseRelationsGroupedExecution(t *testing.T) {
	deparseRelationsUnaryGroupScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO t VALUES(NULL,NULL,true)", 1))
		index := "CREATE INDEX ix ON public.t USING btree (id, txt DESC NULLS LAST, id NULLS FIRST)"
		view := " SELECT id AS k,\n    txt AS label\n   FROM t\n  WHERE visible;"
		aliases := []string{"index_full", "index_column", "view_text", "view_oid", "view_text_pretty", "view_oid_pretty", "view_wrap"}
		types := []string{"INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
		constantCalls := []string{
			"pg_get_indexdef('ix'::regclass)", "pg_get_indexdef('ix'::regclass,2,true)",
			"pg_get_viewdef('v'::text)", "pg_get_viewdef('v'::regclass)",
			"pg_get_viewdef('v'::text,true)", "pg_get_viewdef('v'::regclass,true)", "pg_get_viewdef('v'::regclass,20)",
		}
		nullCalls := []string{
			"pg_get_indexdef(NULL::oid)", "pg_get_indexdef('ix'::regclass,NULL::integer,false)",
			"pg_get_viewdef(NULL::text)", "pg_get_viewdef(NULL::oid)",
			"pg_get_viewdef('v'::text,NULL::boolean)", "pg_get_viewdef('v'::regclass,NULL::boolean)", "pg_get_viewdef('v'::regclass,NULL::integer)",
		}
		projection := func(calls []string) string {
			parts := make([]string, len(calls))
			for i, call := range calls {
				parts[i] = call + " AS " + aliases[i]
			}
			return strings.Join(parts, ",")
		}
		for _, tc := range []struct {
			name   string
			calls  []string
			values []any
		}{
			{"constant", constantCalls, []any{index, "txt", view, view, view, view, view}},
			{"strict_null", nullCalls, []any{nil, nil, nil, nil, nil, nil, nil}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rows := [][]any{}
				for _, key := range []struct {
					id any
					n  int64
				}{{int64(1), 1}, {int64(2), 1}, {int64(3), 2}, {nil, 1}} {
					row := []any{key.id, key.n}
					row = append(row, tc.values...)
					rows = append(rows, row)
				}
				columns := append([]string{"id", "n"}, aliases...)
				q := "SELECT id,count(*) AS n," + projection(tc.calls) + " FROM t GROUP BY id ORDER BY id NULLS LAST"
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(q, columns, types, rows)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		run(fixture.Exec("CREATE TABLE grouped_args(idx OID,view_id OID,name TEXT,col INT,pretty BOOL,width INT)", 0))
		var indexID, viewID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ix'").Scan(&indexID); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&viewID); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("INSERT INTO grouped_args VALUES($1::oid,$2::oid,'v',2,true,20),($1::oid,$2::oid,'v',2,true,20),(NULL,NULL,NULL,NULL,NULL,NULL)", 3, indexID, viewID))
		groupedCalls := []string{"pg_get_indexdef(idx)", "pg_get_indexdef(idx,col,pretty)", "pg_get_viewdef(name)", "pg_get_viewdef(view_id)", "pg_get_viewdef(name,pretty)", "pg_get_viewdef(view_id,pretty)", "pg_get_viewdef(view_id,width)"}
		columns := append([]string{"name", "n"}, aliases...)
		groupedTypes := append([]string{"TEXT", "INT8"}, types[2:]...)
		groupedRows := [][]any{{"v", int64(2), index, "txt", view, view, view, view, view}, {nil, int64(1), nil, nil, nil, nil, nil, nil, nil}}
		q := "SELECT name,count(*) AS n," + projection(groupedCalls) + " FROM grouped_args GROUP BY idx,view_id,name,col,pretty,width ORDER BY name NULLS LAST"
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(q, columns, groupedTypes, groupedRows)
			s.Prepared = prepared
			run(s)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT name,count(*) AS n FROM grouped_args GROUP BY name HAVING name='v' ORDER BY pg_get_viewdef(name)", []string{"name", "n"}, []string{"TEXT", "INT8"}, [][]any{{"v", int64(2)}})
			s.Prepared = prepared
			run(s)
			s = fixture.Select("SELECT id,count(*) AS n FROM t GROUP BY id HAVING count(*)>0 ORDER BY pg_get_viewdef('v'::text),id NULLS LAST", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(2)}, {nil, int64(1)}})
			s.Prepared = prepared
			run(s)
			empty := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_viewdef('v'::text) AS definition FROM t WHERE false GROUP BY id", nil)
			empty.Rows = nil
			empty.Prepared = prepared
			run(empty)
		}
	})
}
