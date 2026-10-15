package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayOrderMetadata(t *testing.T, b fixture.Backend, cols []*sql.ColumnType, names []string) {
	t.Helper()
	types := make([]string, len(names))
	for i := range types {
		types[i] = "BOOL"
	}
	fixture.ScalarAssertDescription(t, cols, names, types)
	for i, col := range cols {
		fixture.ScalarAssertMetadata(t, b.PG, cols, i)
		if col.ScanType() != reflect.TypeFor[bool]() {
			t.Fatalf("BOOL ScanType=%v", col.ScanType())
		}
		if n, ok := col.Length(); n != 0 || ok {
			t.Fatalf("BOOL Length=%d/%t", n, ok)
		}
		if p, s, ok := col.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("BOOL DecimalSize=%d/%d/%t", p, s, ok)
		}
	}
}

func arrayOrderHeldFrames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT $1::int[]=$2::int[] AS eq,$1::int[]<$2::int[] AS lt"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		left, right any
		want        []any
	}{
		{"{1}", "{2}", []any{false, true}}, {"{NULL}", "{NULL}", []any{true, false}},
		{nil, "{2}", []any{nil, nil}}, {"{}", "{}", []any{true, false}},
	} {
		s := fixture.Select(q, []string{"eq", "lt"}, []string{"BOOL", "BOOL"}, [][]any{tc.want}, tc.left, tc.right)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			cols, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			arrayOrderMetadata(t, b, cols, []string{"eq", "lt"})
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `malformed array literal: "oops"`, Detail: `Array value must start with "{" or dimension information.`, Context: "unnamed portal parameter $1 = '...'"}, "oops", "{2}")
	bad.SelectRows, bad.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"eq", "lt"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, "{1}", "{2}"))
}

func TestReferenceArrayOrderMetadata(t *testing.T) {
	arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE ao_meta(id int,a int[],b int[],x int)", 0),
			fixture.Exec("INSERT INTO ao_meta VALUES(1,'{1}','{2}',1),(2,NULL,NULL,NULL)", 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, tc := range []struct {
			suffix string
			want   [][]any
		}{
			{" ORDER BY id", [][]any{{false, true, true}, {nil, nil, nil}}},
			{" WHERE FALSE", nil}, {" WHERE id=2", [][]any{{nil, nil, nil}}},
		} {
			for _, prepared := range []bool{false, true} {
				var retained []*sql.ColumnType
				s := fixture.Select("SELECT a=b AS eq,a<b AS lt,x=ANY(a) AS quantified FROM ao_meta"+tc.suffix, []string{"eq", "lt", "quantified"}, []string{"BOOL", "BOOL", "BOOL"}, tc.want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					var err error
					retained, err = r.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					arrayOrderMetadata(t, b, retained, []string{"eq", "lt", "quantified"})
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				arrayOrderMetadata(t, b, retained, []string{"eq", "lt", "quantified"})
			}
		}
		arrayOrderHeldFrames(t, p, b, c)
	})
}

func TestReferenceArrayOrderFeatureSmoke(t *testing.T) {
	arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT '{1,9}'::int[]<'{2,0}'::int[],'{1,2}'::int[]='[0:1]={1,2}'::int[],'{NULL}'::int[]>'{1}'::int[],'{1,2,3,4}'::int[]<'{{1,2},{3,4}}'::int[]", []any{true, false, true, true}))
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT NULL::int=ANY('{}'::int[]),NULL::int=ALL('{}'::int[]),3=ANY('{1,NULL}'::int[])", []any{false, true, nil}))
		arrayOrderHeldFrames(t, p, b, c)
		arrayOrderErrorBoth(t, p, b, c, "SELECT ARRAY[1] = ARRAY[TRUE]", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer[] = boolean[]", Position: "17", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, true)
	})
}
