package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayCompareScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

func arrayCompareBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		})
	}
}

func arrayCompareBool(query string, want []any, args ...any) fixture.Step {
	names, types := make([]string, len(want)), make([]string, len(want))
	for i := range want {
		names[i], types[i] = "?column?", "BOOL"
	}
	return fixture.Select(query, names, types, [][]any{want}, args...)
}

func arrayCompareHeldFrames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT $1::INT=ANY($2),$1::INT<>ALL($2)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, frame := range []struct {
		left, array any
		want        []any
	}{
		{1, "{1,NULL}", []any{true, false}},
		{3, "{1,NULL}", []any{nil, nil}},
		{nil, "{}", []any{false, true}},
		{1, nil, []any{nil, nil}},
		{1, "{1,NULL}", []any{true, false}},
	} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, arrayCompareBool(q, frame.want, frame.left, frame.array))
	}
}

func TestReferenceArrayCompareFeatureSmoke(t *testing.T) {
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ac(id INT,x INT,a INT[])", 0))
		run(fixture.Exec("INSERT INTO ac VALUES(1,1,'{1,2,NULL}'),(2,3,'{1,2,NULL}'),(3,NULL,'{}')", 3))
		run(fixture.Exec("INSERT INTO ac VALUES($1,$2,$3)", 1, 4, nil, "{1}"))
		run(fixture.Prepared(fixture.Exec("INSERT INTO ac VALUES($1,$2,$3)", 1, 5, 1, nil)))
		run(fixture.Prepared(fixture.Exec("INSERT INTO ac VALUES($1,$2,$3)", 1, 6, 1, "{NULL}")))
		run(fixture.Exec("INSERT INTO ac VALUES(7,1,'{1,1}')", 1))
		const projection = "SELECT id,x IN(1,2),x NOT IN(1,2),x=ANY(a),x=SOME(a),x=ALL(a),x<>ALL(a) FROM ac"
		names := []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}
		types := []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}
		want := [][]any{
			{int64(1), true, false, true, true, false, false},
			{int64(2), false, true, nil, nil, false, nil},
			{int64(3), nil, nil, false, false, true, true},
			{int64(4), nil, nil, nil, nil, nil, nil},
			{int64(5), true, false, nil, nil, nil, nil},
			{int64(6), true, false, nil, nil, nil, nil},
			{int64(7), true, false, true, true, true, false},
		}
		arrayCompareBoth(t, p, b, c, fixture.Select(projection+" ORDER BY id", names, types, want))
		arrayCompareBoth(t, p, b, c, fixture.Select(projection+" WHERE FALSE", names, types, nil))
		arrayCompareBoth(t, p, b, c, fixture.Select("SELECT id FROM ac WHERE x=ANY(a) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(7)}}))
		arrayCompareBoth(t, p, b, c, fixture.Select("SELECT id FROM ac WHERE x NOT IN(1,NULL)", []string{"id"}, []string{"INT4"}, nil))
		arrayCompareHeldFrames(t, p, b, c)
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT '2147483648' IN(1,2147483648),2=ANY('[-1:0][3:4]={{1,NULL},{2,3}}'::INT[])", []any{true, true}))
		for _, tc := range []struct {
			q string
			d fixture.Diagnostic
		}{
			{"SELECT 1 = ANY(1)", fixture.Diagnostic{Code: "42809", Message: "op ANY/ALL (array) requires array on right side", Position: "10"}},
			{"SELECT 1 IN (1,TRUE)", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		} {
			s := fixture.ImplicitError(tc.q, tc.d)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
		}
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT 1 IN(1),1=ANY(ARRAY[1])", []any{true, true}))
	})
}
