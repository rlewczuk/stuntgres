package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowExcludeRemedyScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

func queryWindowExcludeRemedySequence(t *testing.T, p *fixture.Pair, b fixture.Backend, value int64) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Select("SELECT last_value FROM ex_remedy_sequence", []string{"last_value"}, []string{"INT8"}, [][]any{{value}}))
}

// F1: omitted and explicit NO OTHERS must retain transition state, not
// re-evaluate overlapping members. Held executions must own fresh state.
func TestReferenceQueryWindowExcludeNoOthersTransition(t *testing.T) {
	queryWindowExcludeRemedyScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		for _, exclusion := range []string{"", " EXCLUDE NO OTHERS"} {
			for _, tc := range []struct {
				end           string
				first, second [][]any
			}{
				{"UNBOUNDED FOLLOWING", [][]any{{int64(1), "6"}, {int64(2), "6"}, {int64(3), "6"}}, [][]any{{int64(1), "15"}, {int64(2), "15"}, {int64(3), "15"}}},
				{"CURRENT ROW", [][]any{{int64(1), "1"}, {int64(2), "3"}, {int64(3), "6"}}, [][]any{{int64(1), "4"}, {int64(2), "9"}, {int64(3), "15"}}},
			} {
				q := "SELECT id,sum(nextval('ex_remedy_sequence')) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND " + tc.end + exclusion + ") AS s FROM ex_t ORDER BY id"
				for _, prepared := range []bool{false, true} {
					queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE ex_remedy_sequence START WITH 1", 0))
					s := fixture.Select(q, []string{"id", "s"}, []string{"INT4", "NUMERIC"}, tc.first)
					s.Prepared = prepared
					queryWindowRun(t, p, b, s)
					queryWindowExcludeRemedySequence(t, p, b, 3)
					queryWindowRun(t, p, b, fixture.Exec("DROP SEQUENCE ex_remedy_sequence", 0))
				}
				queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE ex_remedy_sequence START WITH 1", 0))
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				for i, rows := range [][][]any{tc.first, tc.second} {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, fixture.Select(q, []string{"id", "s"}, []string{"INT4", "NUMERIC"}, rows))
					queryWindowExcludeRemedySequence(t, p, b, int64(3*(i+1)))
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				queryWindowRun(t, p, b, fixture.Exec("DROP SEQUENCE ex_remedy_sequence", 0))
			}
		}
	})
}

// F2: SQL target equality is independent of executor occurrence sharing.
func TestReferenceQueryWindowExcludeSumTargetResolution(t *testing.T) {
	queryWindowExcludeRemedyScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		w := " WINDOW w AS(ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES)"
		for _, prefix := range []string{"SELECT", "SELECT DISTINCT"} {
			q := prefix + " sum(v) OVER w AS s FROM ex_t" + w + " ORDER BY sum(v) OVER w"
			queryWindowRoutes(t, p, b, fixture.Select(q, []string{"s"}, []string{"INT8"}, [][]any{{int64(40)}, {int64(50)}, {int64(60)}}))
		}
		q := "SELECT sum(v) OVER w AS s,sum(v) OVER w AS s FROM ex_t WINDOW w AS(ROWS CURRENT ROW EXCLUDE TIES) ORDER BY s"
		queryWindowRoutes(t, p, b, fixture.Select(q, []string{"s", "s"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(10)}, {int64(20), int64(20)}, {int64(30), int64(30)}}))
		// An independently analyzed ORDER expression must resolve to the target,
		// not become an extra executing volatile aggregate occurrence.
		for _, prepared := range []bool{false, true} {
			queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE ex_remedy_sequence START WITH 1", 0))
			q := "SELECT sum(nextval('ex_remedy_sequence')) OVER w AS s FROM ex_t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) ORDER BY sum(nextval('ex_remedy_sequence')) OVER w"
			s := fixture.Select(q, []string{"s"}, []string{"NUMERIC"}, [][]any{{"6"}, {"15"}, {"24"}})
			s.Prepared = prepared
			queryWindowRun(t, p, b, s)
			queryWindowExcludeRemedySequence(t, p, b, 9)
			queryWindowRun(t, p, b, fixture.Exec("DROP SEQUENCE ex_remedy_sequence", 0))
		}
	})
}

// F3: normalize each output pair in SQL so no target evaluation order is
// prescribed. Exact member-major pairs distinguish call-major replay even
// though both schedules have the same combined totals and sequence count.
func TestReferenceQueryWindowExcludeVolatileReplaySchedule(t *testing.T) {
	queryWindowExcludeRemedyScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		q := "SELECT id,CASE WHEN a<b THEN a ELSE b END AS lo,CASE WHEN a<b THEN b ELSE a END AS hi FROM (SELECT id,sum(nextval('ex_remedy_sequence')) OVER w AS a,sum(nextval('ex_remedy_sequence')) OVER w AS b FROM ex_t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES)) x ORDER BY id"
		first := [][]any{{int64(1), "9", "12"}, {int64(2), "27", "30"}, {int64(3), "45", "48"}}
		second := [][]any{{int64(1), "63", "66"}, {int64(2), "81", "84"}, {int64(3), "99", "102"}}
		for _, prepared := range []bool{false, true} {
			queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE ex_remedy_sequence START WITH 1", 0))
			s := fixture.Select(q, []string{"id", "lo", "hi"}, []string{"INT4", "NUMERIC", "NUMERIC"}, first)
			s.Prepared = prepared
			queryWindowRun(t, p, b, s)
			queryWindowExcludeRemedySequence(t, p, b, 18)
			queryWindowRun(t, p, b, fixture.Exec("DROP SEQUENCE ex_remedy_sequence", 0))
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE ex_remedy_sequence START WITH 1", 0))
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for i, rows := range [][][]any{first, second} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, fixture.Select(q, []string{"id", "lo", "hi"}, []string{"INT4", "NUMERIC", "NUMERIC"}, rows))
			queryWindowExcludeRemedySequence(t, p, b, int64(18*(i+1)))
		}
	})
}
