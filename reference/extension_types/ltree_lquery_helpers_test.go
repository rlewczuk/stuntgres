package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ltreeLqueryScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func ltreeLqueryRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	defer func() {
		if t.Failed() { t.Logf("SQL (backend=%s, prepared=%t): %s", b.Name, s.Prepared, s.Query) }
	}()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func ltreeLqueryPair(b fixture.Backend, q string, want any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"forward", "reverse"}, []string{"BOOL", "BOOL"}, [][]any{{want, want}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ltreeArrayMetadata(t, b, rows, []string{"BOOL", "BOOL"}, nil) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value != nil { if _, ok := value.(bool); !ok { t.Fatalf("Boolean raw value %T", value) } }
		return true
	}
	return s
}

const ltreeLqueryHeldSQL = "SELECT $1::text::ltree ~ $2::text::lquery AS forward,$2::text::lquery ~ $1::text::ltree AS reverse"

func ltreeLqueryHeldChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, tc := range []struct{ path, query, want any }{
		{"Top.A.Child", "Top.*{1,2}.Child", true},
		{"Top.Child", "Top.*{1,2}.Child", false},
		{nil, "Top.*{1,2}.Child", nil},
		{"FOO1_BAR2_baz", "foo_bar%@*", true},
		{"a.a", "a{0,2}.a", true},
		{"a.b", "a{2}.b", false},
		{"", "*", true},
	} { ltreeLqueryRun(t, p, b, e, ltreeLqueryPair(b, ltreeLqueryHeldSQL, tc.want, tc.path, tc.query)) }
}

func ltreeLqueryUpdateAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	run := func(s fixture.Step) { ltreeLqueryRun(t, p, b, c, s) }
	run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v boolean)", 0))
	run(fixture.Exec("INSERT INTO "+table+" VALUES (0,false),(1,true)", 2))
	// Both formals are TEXT. Only the row-dependent CASE result is converted,
	// so malformed lquery input reaches execution rather than parameter import.
	q := "UPDATE "+table+" SET v='a'::ltree ~ (CASE WHEN id=0 THEN $1::text ELSE $2::text END)::lquery"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	original := [][]any{{int64(0), false}, {int64(1), true}}
	check := func(want [][]any) {
		run(fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "BOOL"}, want))
	}
	// No UPDATE traversal order is promised. Swap the malformed branch: if
	// id=0 is visited first, the first case has a successful changing candidate;
	// if id=1 is visited first, the second case does. Each valid branch flips
	// its original value, so a leaked prefix cannot hide behind an equal value.
	// Both cases must preserve the entire original relation independently.
	for _, args := range [][]any{{"a", "a..b"}, {"a..b", "b"}} {
		want := fixture.ControlsReferenceDiagnostic("42601", "lquery syntax error at character 3")
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, want, args...)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			check(original)
		}
		// Runtime casts have empty Context/Position, unlike typed portal input.
		s := fixture.ErrorDiagnostic(q, want, args...)
		s.ErrorStage = "execute"
		ltreeLqueryRun(t, p, b, held, s)
		check(original)
	}
	// Retry the very same UPDATE handle after both runtime-cast failures.
	ltreeLqueryRun(t, p, b, held, fixture.Exec(q, 2, "a", "b"))
	check([][]any{{int64(0), true}, {int64(1), false}})
}
