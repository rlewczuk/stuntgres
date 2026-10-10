package reference_test

import "testing"

func referenceTiesScenario(t *testing.T, body func(*referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		body(p, backend)
	}
}

func referenceTiesSetup(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE ties_t(id INT,k INT)", 0),
		referenceExec("INSERT INTO ties_t VALUES(1,1),(2,2),(3,2),(4,3)", 4),
	} {
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	}
}

func referenceTiesBoth(t *testing.T, p *referencePair, b referenceBackend, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				referenceTiesSetup(t, p, b)
				referenceTiesBoth(t, p, b, referenceSelect("SELECT k FROM ties_t ORDER BY k "+tc.suffix, []string{"k"}, []string{"INT4"}, referenceLimitIDs(tc.keys...)))
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				referenceTiesSetup(t, p, b)
				referenceTiesBoth(t, p, b, referenceSelect("SELECT k FROM "+tc.source+" ORDER BY k "+tc.suffix, []string{"k"}, []string{"INT4"}, referenceLimitIDs(tc.keys...)))
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				columns, types := []string{"k"}, []string{tc.typ}
				if tc.typ == "pair" {
					columns = []string{"a", "b"}
					types = []string{"INT4", "INT4"}
				}
				referenceTiesBoth(t, p, b, referenceSelect(tc.query, columns, types, tc.rows))
			})
		})
	}
}

func TestReferenceQueryTiesPrepared(t *testing.T) {
	referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
		referenceTiesSetup(t, p, b)
		stmt, err := b.db.PrepareContext(p.ctx, "SELECT k FROM ties_t ORDER BY k FETCH FIRST $1 ROWS WITH TIES OFFSET $2")
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
			rows, err := stmt.QueryContext(p.ctx, tc.count, tc.offset)
			referenceViewValues(t, relationOutcome{rows: rows, err: err}, "k", "INT4", tc.keys...)
		}
		rows, err := stmt.QueryContext(p.ctx, int64(-1), int64(0))
		if rows != nil {
			rows.Close()
			t.Fatal("failed execution published cursor")
		}
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201W", Message: "LIMIT must not be negative"}
		referenceAssertDiagnostic(t, err, &want)
	})
}
