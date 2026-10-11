package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referenceTiesScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		body(p, backend)
	}
}

func referenceTiesSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ties_t(id INT,k INT)", 0),
		fixture.Exec("INSERT INTO ties_t VALUES(1,1),(2,2),(3,2),(4,3)", 4),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func referenceTiesBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func TestReferenceQueryTiesBasics(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		keys   []int64
	}{
		{"FETCH FIRST 2 ROWS WITH TIES", []int64{1, 2, 2}},
		{"FETCH NEXT (1+1) ROW WITH TIES", []int64{1, 2, 2}},
		{"FETCH FIRST 2 ROWS ONLY", []int64{1, 2}},
		{"FETCH FIRST ROW WITH TIES", []int64{1}},
		{"FETCH NEXT ROWS WITH TIES", []int64{1}},
		{"OFFSET 1 ROW FETCH FIRST 1 ROW WITH TIES", []int64{2, 2}},
		{"FETCH FIRST 1 ROW WITH TIES OFFSET 1 ROWS", []int64{2, 2}},
		{"OFFSET 2 FETCH FIRST 1 ROW WITH TIES", []int64{2}},
		{"OFFSET 3 FETCH FIRST 1 ROW WITH TIES", []int64{3}},
		{"OFFSET 4 FETCH FIRST 1 ROW WITH TIES", nil},
		{"FETCH FIRST 0 ROWS WITH TIES", nil},
		{"OFFSET 9223372036854775807 FETCH FIRST 9223372036854775807 ROWS WITH TIES", nil},
		{"OFFSET 1 FETCH FIRST 9223372036854775807 ROWS WITH TIES", []int64{2, 2, 3}},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				referenceTiesBoth(t, p, b, fixture.Select("SELECT k FROM ties_t ORDER BY k "+tc.suffix, []string{"k"}, []string{"INT4"}, referenceLimitIDs(tc.keys...)))
			})
		})
	}
	for _, tc := range []struct {
		source, suffix string
		keys           []int64
	}{
		{"(VALUES(2),(2),(2)) AS a(k)", "FETCH FIRST ROW WITH TIES", []int64{2, 2, 2}},
		{"(VALUES(2),(2),(2)) AS a(k)", "OFFSET 1 FETCH NEXT ROW WITH TIES", []int64{2, 2}},
		{"(VALUES(1),(1),(2)) AS a(k)", "FETCH NEXT ROW WITH TIES", []int64{1, 1}},
		{"ties_t WHERE FALSE", "FETCH FIRST 2 ROWS WITH TIES", nil},
	} {
		t.Run(tc.source+tc.suffix, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				referenceTiesBoth(t, p, b, fixture.Select("SELECT k FROM "+tc.source+" ORDER BY k "+tc.suffix, []string{"k"}, []string{"INT4"}, referenceLimitIDs(tc.keys...)))
			})
		})
	}
}

func TestReferenceQueryTiesPeers(t *testing.T) {
	for _, tc := range []struct {
		query string
		rows  [][]any
		typ   string
	}{
		{"SELECT a,b FROM (VALUES(1,1),(1,2),(1,2),(2,1)) AS t(a,b) ORDER BY a,b FETCH FIRST 2 ROWS WITH TIES", [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(2)}}, "pair"},
		{"SELECT k FROM (VALUES(1),(NULL),(NULL)) AS t(k) ORDER BY k FETCH FIRST 2 ROWS WITH TIES", [][]any{{int64(1)}, {nil}, {nil}}, "INT4"},
		{"SELECT k FROM (VALUES(1),(NULL),(NULL)) AS t(k) ORDER BY k DESC FETCH FIRST ROW WITH TIES", [][]any{{nil}, {nil}}, "INT4"},
		{"SELECT k FROM (VALUES(1),(NULL),(NULL)) AS t(k) ORDER BY k ASC NULLS FIRST FETCH FIRST ROW WITH TIES", [][]any{{nil}, {nil}}, "INT4"},
		{"SELECT k FROM (VALUES(NULL::int),(NULL::int)) AS t(k) ORDER BY k FETCH FIRST ROW WITH TIES", [][]any{{nil}, {nil}}, "INT4"},
		{"SELECT k FROM (VALUES('a'::char(2)),('a '::char(2)),('b'::char(2))) AS t(k) ORDER BY k FETCH FIRST ROW WITH TIES", [][]any{{"a "}, {"a "}}, "BPCHAR"},
		{"SELECT k FROM (VALUES('a'),('a'),('b')) AS t(k) ORDER BY k COLLATE \"C\" FETCH FIRST ROW WITH TIES", [][]any{{"a"}, {"a"}}, "TEXT"},
		{"SELECT k FROM (VALUES('a'),('a'),('b')) AS t(k) ORDER BY k COLLATE \"POSIX\" FETCH FIRST ROW WITH TIES", [][]any{{"a"}, {"a"}}, "TEXT"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				columns, types := []string{"k"}, []string{tc.typ}
				if tc.typ == "pair" {
					columns = []string{"a", "b"}
					types = []string{"INT4", "INT4"}
				}
				referenceTiesBoth(t, p, b, fixture.Select(tc.query, columns, types, tc.rows))
			})
		})
	}
}

func TestReferenceQueryTiesPrepared(t *testing.T) {
	referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		referenceTiesSetup(t, p, b)
		stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT k FROM ties_t ORDER BY k FETCH FIRST $1 ROWS WITH TIES OFFSET $2")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			count, offset any
			keys          []int64
		}{
			{int64(2), int64(0), []int64{1, 2, 2}}, {int64(0), nil, nil}, {nil, int64(1), []int64{2, 2, 3}}, {int64(1), int64(2), []int64{2}},
		} {
			rows, err := stmt.QueryContext(p.Ctx, tc.count, tc.offset)
			fixture.ViewValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, "k", "INT4", tc.keys...)
		}
		rows, err := stmt.QueryContext(p.Ctx, int64(-1), int64(0))
		if rows != nil {
			rows.Close()
			t.Fatal("failed execution published cursor")
		}
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201W", Message: "LIMIT must not be negative"}
		fixture.AssertDiagnostic(t, err, &want)
	})
}
