package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func regexpHeldSets(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	const q = `SELECT m,o FROM regexp_matches($1,$2,$3) WITH ORDINALITY r(m,o) ORDER BY o`
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
	var typedNil *string
	for _, c := range []struct {
		args   []any
		rows   [][]any
		reason string
	}{
		{[]any{"aba", "(a)", "g"}, [][]any{{"{a}", int64(1)}, {"{a}", int64(2)}}, ""},
		{[]any{"aba", "(a)", ""}, [][]any{{"{a}", int64(1)}}, ""},
		{[]any{"x", "a", "g"}, nil, ""},
		{[]any{typedNil, "[", "z"}, nil, ""},
		{[]any{"a", "[", "g"}, nil, "brackets [] not balanced"},
		{[]any{"é🙂", "(.)", "g"}, [][]any{{"{é}", int64(1)}, {"{🙂}", int64(2)}}, ""},
		{[]any{"a", "a", typedNil}, nil, ""},
	} {
		s := regexpRows(b, q, []string{"m", "o"}, []string{"_TEXT", "INT8"}, c.rows, c.args...)
		if c.reason != "" {
			s = similarReferenceRegexError(q, c.reason, "", c.args...)
		}
		t.Run(q, func(t *testing.T) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) })
	}
	const splitSQL = `SELECT s,o FROM regexp_split_to_table($1,$2,$3) WITH ORDINALITY r(s,o) ORDER BY o`
	split, err := b.DB.PrepareContext(p.Ctx, splitSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer split.Close()
	heldSplit := fixture.HeldStatement{Executor: b.DB, Statement: split, SQL: splitSQL}
	for _, c := range []struct {
		args []any
		rows [][]any
	}{
		{[]any{"a,,b,", ",", ""}, [][]any{{"a", int64(1)}, {"", int64(2)}, {"b", int64(3)}, {"", int64(4)}}},
		{[]any{"é🙂", "", ""}, [][]any{{"é", int64(1)}, {"🙂", int64(2)}}},
		{[]any{"", "", ""}, [][]any{{"", int64(1)}}},
		{[]any{"a", "a", nil}, nil},
	} {
		fixture.RunStep(t, p.Ctx, heldSplit, b.Recorder, regexpRows(b, splitSQL, []string{"s", "o"}, []string{"TEXT", "INT8"}, c.rows, c.args...))
	}
	fixture.RunStep(t, p.Ctx, heldSplit, b.Recorder, regexpRuntimeError(splitSQL, "22023", `regexp_split_to_table() does not support the "global" option`, "", "a", "a", "g"))
	fixture.RunStep(t, p.Ctx, heldSplit, b.Recorder, regexpRows(b, splitSQL, []string{"s", "o"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}}, "a", "z", ""))
	if !b.PG {
		// Native eager cursors freeze owned arrays before the shared owner is reused.
		old, err := stmt.QueryContext(p.Ctx, "aba", "(a)", "g")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpRows(b, q, []string{"m", "o"}, []string{"_TEXT", "INT8"}, [][]any{{"{x}", int64(1)}}, "x", "(x)", ""))
		fixture.StringCoreRetainedRows(t, old, nil, []string{"m", "o"}, []string{"_TEXT", "INT8"}, [][]any{{"{a}", int64(1)}, {"{a}", int64(2)}}, false)
	}
}
