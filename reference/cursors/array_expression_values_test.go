package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations originate in PostgreSQL REL_18_0 transformArrayExpr,
// ExecEvalArrayExpr and array_get_slice and were checked against the pinned
// PostgreSQL 18.0 oracle during Stage A (root tests-1.md).
func arrayExpressionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE public.one (v INT)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.one VALUES (1)", 1))
			body(t, p, b, c)
		})
	}
}

func arrayExpressionFixture(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE public.ax (id INT,a INT[],m INT[])", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.ax VALUES (1,'{10,NULL,30}','{{1,2},{3,4}}'),(2,'[0:2]={40,50,60}','[-1:0][3:4]={{5,6},{7,8}}'),(3,'{}','{}'),(4,NULL,NULL)", 4))
}

func arrayExpressionBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func TestReferenceArrayExpressionConstructors(t *testing.T) {
	cases := []struct{ name, expr, typ, value string }{
		{"integer_null", "ARRAY[1,NULL,2]", "_INT4", "{1,NULL,2}"},
		{"text_unknown", "ARRAY['x','NULL',NULL]", "_TEXT", `{x,"NULL",NULL}`},
		{"all_unknown", "ARRAY[NULL,NULL]", "_TEXT", "{NULL,NULL}"},
		{"wide_integer", "ARRAY[1,2147483648]", "_INT8", "{1,2147483648}"},
		{"numeric", "ARRAY[1,2.5]", "_NUMERIC", "{1,2.5}"},
		{"empty", "ARRAY[]::integer[]", "_INT4", "{}"},
		{"nested_empty", "ARRAY[[]]::integer[]", "_INT4", "{}"},
		{"shorthand", "ARRAY[[1,2],[3,4]]", "_INT4", "{{1,2},{3,4}}"},
		{"explicit_nested", "ARRAY[ARRAY[1,2],ARRAY[3,4]]", "_INT4", "{{1,2},{3,4}}"},
		{"nested_common", "ARRAY[ARRAY[1],ARRAY[2::bigint]]", "_INT8", "{{1},{2}}"},
		{"explicit_rounding", "ARRAY[1,2,22.7]::integer[]", "_INT4", "{1,2,23}"},
		{"empty_children", "ARRAY[ARRAY[]::int[],ARRAY[]::int[]]", "_INT4", "{}"},
		{"null_children", "ARRAY[NULL::int[],NULL::int[]]", "_INT4", "{}"},
		{"null_cells", "ARRAY[ARRAY[NULL::int],ARRAY[1]]", "_INT4", "{{NULL},{1}}"},
		{"inner_bounds", "ARRAY['[0:1]={1,2}'::int[],'[0:1]={3,4}'::int[]]", "_INT4", "[1:2][0:1]={{1,2},{3,4}}"},
		{"boolean", "ARRAY[TRUE,FALSE,NULL]", "_BOOL", "{t,f,NULL}"},
		{"uuid", "ARRAY['00112233-4455-6677-8899-aabbccddeeff'::uuid,NULL]", "_UUID", "{00112233-4455-6677-8899-aabbccddeeff,NULL}"},
		{"date", "ARRAY['2024-02-29'::date,NULL]", "_DATE", "{2024-02-29,NULL}"},
		{"regclass", "ARRAY['public.one'::regclass,NULL]", "_REGCLASS", "{one,NULL}"},
		{"varchar", "ARRAY['a'::varchar(2),'b'::varchar(2)]", "_VARCHAR", "{a,b}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				s := fixture.ArrayReferenceSelect("SELECT "+tc.expr+" AS a FROM public.one", []string{"a"}, []string{tc.typ}, []string{tc.typ}, [][]any{{tc.value}}, b.PG)
				arrayExpressionBoth(t, p, b, c, s)
			})
		})
	}
	t.Run("domains", func(t *testing.T) {
		arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE DOMAIN public.ax_di AS int", "CREATE DOMAIN public.ax_whole AS int[]"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			typ := "_AX_DI"
			whole := "_AX_WHOLE"
			if b.PG {
				typ = ""
				whole = ""
			}
			arrayExpressionBoth(t, p, b, c, fixture.ArrayReferenceSelect("SELECT ARRAY[1::public.ax_di,2::public.ax_di] AS d,ARRAY[1::public.ax_di,2] AS i,ARRAY['{1,2}'::public.ax_whole,'{3,4}'::public.ax_whole] AS w FROM public.one", []string{"d", "i", "w"}, []string{typ, "_INT4", whole}, []string{"_INT4", "_INT4", "_INT4"}, [][]any{{"{1,2}", "{1,2}", `{"{1,2}","{3,4}"}`}}, b.PG))
		})
	})
}

func TestReferenceArrayExpressionAccess(t *testing.T) {
	cases := []struct {
		name, query  string
		names, types []string
		rows         [][]any
	}{
		{"elements", "SELECT id,a[0] AS z,a[1] AS x,a[3] AS y,a[NULL] AS n,a[1][1] AS r,m[2][1] AS q,m[1] AS s FROM public.ax ORDER BY id", []string{"id", "z", "x", "y", "n", "r", "q", "s"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, int64(10), int64(30), nil, nil, int64(3), nil}, {int64(2), int64(40), int64(50), nil, nil, nil, nil, nil}, {int64(3), nil, nil, nil, nil, nil, nil, nil}, {int64(4), nil, nil, nil, nil, nil, nil, nil}}},
		{"slices", "SELECT id,a[0:2] AS x,a[2:9] AS y,a[9:12] AS z,a[3:1] AS r,a[:] AS s,a[NULL:2] AS n FROM public.ax ORDER BY id", []string{"id", "x", "y", "z", "r", "s", "n"}, []string{"INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_INT4"}, [][]any{{int64(1), "{10,NULL}", "{NULL,30}", "{}", "{}", "{10,NULL,30}", nil}, {int64(2), "{40,50,60}", "{60}", "{}", "{}", "{40,50,60}", nil}, {int64(3), "{}", "{}", "{}", "{}", "{}", nil}, {int64(4), nil, nil, nil, nil, nil, nil}}},
		{"global_slice", "SELECT m[1:2][2] AS a,m[2][1:1] AS b,m[:][2:] AS c,m[1:1] AS d,m[1:2][1:2][1:1] AS e FROM public.ax WHERE id=1", []string{"a", "b", "c", "d", "e"}, []string{"_INT4", "_INT4", "_INT4", "_INT4", "_INT4"}, [][]any{{"{{1,2},{3,4}}", "{{1},{3}}", "{{2},{4}}", "{{1,2}}", "{}"}}},
		{"nonone_slice", "SELECT m[-1:0][3] AS a,m[-1:0][:3] AS b,m[:] AS c FROM public.ax WHERE id=2", []string{"a", "b", "c"}, []string{"_INT4", "_INT4", "_INT4"}, [][]any{{"{{5},{7}}", "{{5},{7}}", "{{5,6},{7,8}}"}}},
		{"labels_grouping", "SELECT (ARRAY[7,8])[1],(ARRAY[7,8])[1:1],(a[1:2])[2] AS again,a[id] AS indexed FROM public.ax WHERE id=1", []string{"array", "array", "again", "indexed"}, []string{"INT4", "_INT4", "INT4", "INT4"}, [][]any{{int64(7), "{7}", nil, int64(10)}}},
		{"assignment_bounds", "SELECT a[1.6] AS a,a[1.4] AS b,a['1'] AS c FROM public.ax WHERE id=1", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{nil, int64(10), int64(10)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				arrayExpressionFixture(t, p, b, c)
				arrayExpressionBoth(t, p, b, c, fixture.ArrayReferenceSelect(tc.query, tc.names, tc.types, tc.types, tc.rows, b.PG))
			})
		})
	}
}
