package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Pinned discovery established frame elimination for row_number and LIMIT 0,
// but an active count WindowAgg inside EXISTS still demands its frame.
func TestReferenceQueryWindowRangeDemand(t *testing.T) {
	for _, input := range []string{"finite", "empty", "nulls"} {
		for _, endpoint := range []string{"start", "end"} {
			for _, offset := range []string{"NULL", "-1", "'NaN'::DOUBLE PRECISION"} {
				t.Run(input+"_"+endpoint+"_"+offset, func(t *testing.T) {
					queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
						queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE demand(id INT,k DOUBLE PRECISION)", 0))
						values := [][]any(nil)
						if input == "finite" {
							queryWindowRun(t, p, b, fixture.Exec("INSERT INTO demand VALUES(1,1),(2,2)", 2))
							values = [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}
						}
						if input == "nulls" {
							queryWindowRun(t, p, b, fixture.Exec("INSERT INTO demand VALUES(1,NULL),(2,NULL)", 2))
							values = [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}}
						}
						frame := "BETWEEN " + offset + " PRECEDING AND CURRENT ROW"
						if endpoint == "end" {
							frame = "BETWEEN UNBOUNDED PRECEDING AND " + offset + " FOLLOWING"
						}
						q := "SELECT id,count(*) OVER(ORDER BY k RANGE " + frame + ") AS n FROM demand ORDER BY id"
						if offset == "NULL" {
							word := "starting"
							if endpoint == "end" {
								word = "ending"
							}
							queryWindowRangeRuntime(t, p, b, q, "22004", "frame "+word+" offset must not be null")
						} else if input == "finite" {
							queryWindowRangeRuntime(t, p, b, q, "22013", "invalid preceding or following size in window function")
						} else {
							queryWindowRoutes(t, p, b, fixture.Select(q, []string{"id", "n"}, []string{"INT4", "INT8"}, values))
						}
						// LIMIT 0 suppresses runtime demand, not analysis.
						queryWindowRoutes(t, p, b, fixture.Select(q+" LIMIT 0", []string{"id", "n"}, []string{"INT4", "INT8"}, nil))
						rn := "SELECT row_number() OVER(ORDER BY k RANGE " + frame + ") AS n FROM demand ORDER BY n"
						rows := [][]any(nil)
						if input != "empty" {
							rows = [][]any{{int64(1)}, {int64(2)}}
						}
						queryWindowRoutes(t, p, b, fixture.Select(rn, []string{"n"}, []string{"INT8"}, rows))
						queryWindowRoutes(t, p, b, fixture.Select("SELECT id FROM demand WINDOW unused AS(ORDER BY k RANGE "+frame+") ORDER BY id", []string{"id"}, []string{"INT4"}, rangeDemandIDs(input)))
						exists := input != "empty"
						existsQuery := "SELECT EXISTS(SELECT count(*) OVER(ORDER BY k RANGE " + frame + ") FROM demand) AS found"
						if offset == "NULL" {
							word := "starting"
							if endpoint == "end" {
								word = "ending"
							}
							queryWindowRangeRuntime(t, p, b, existsQuery, "22004", "frame "+word+" offset must not be null")
						} else if input == "finite" {
							queryWindowRangeRuntime(t, p, b, existsQuery, "22013", "invalid preceding or following size in window function")
						} else {
							queryWindowRoutes(t, p, b, fixture.Select(existsQuery, []string{"found"}, []string{"BOOL"}, [][]any{{exists}}))
						}
					})
				})
			}
		}
	}
	queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRangeSeed(t, p, b)
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k RANGE BETWEEN NULL PRECEDING AND NULL FOLLOWING) FROM range_t", "22004", "frame starting offset must not be null")
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k RANGE BETWEEN -1 PRECEDING AND NULL FOLLOWING) FROM range_t", "22004", "frame ending offset must not be null")
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k RANGE (SELECT 1/0) PRECEDING) FROM range_t", "22012", "division by zero")
		// PostgreSQL constant folding reports the input expression error before
		// WindowAgg can evaluate its NULL frame offset.
		queryWindowRangeRuntime(t, p, b, "SELECT count(*) OVER(ORDER BY k RANGE NULL PRECEDING) FROM (SELECT 1/0 AS k) AS s", "22012", "division by zero")
	})
}

func rangeDemandIDs(input string) [][]any {
	if input == "empty" {
		return nil
	}
	return [][]any{{int64(1)}, {int64(2)}}
}

func TestReferenceQueryWindowRangeLifecycle(t *testing.T) {
	queryWindowRangeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRangeSeed(t, p, b)
		q := "SELECT id,count(*) OVER(ORDER BY k RANGE $1 PRECEDING) AS n FROM range_t ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		for _, tc := range []struct {
			arg           any
			values        []int64
			code, message string
		}{
			{1, []int64{1, 3, 3, 1, 2, 2, 2}, "", ""}, {3, []int64{1, 3, 3, 4, 4, 2, 2}, "", ""},
			{nil, nil, "22004", "frame starting offset must not be null"}, {-1, nil, "22013", "invalid preceding or following size in window function"},
			{0, []int64{1, 2, 2, 1, 1, 2, 2}, "", ""}, {1, []int64{1, 3, 3, 1, 2, 2, 2}, "", ""},
		} {
			s := queryWindowRangeResult(q, tc.values...)
			s.Args = []any{tc.arg}
			if tc.code != "" {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.arg)
				s.SelectRows = true
				s.ErrorStage = "execute"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		q = "SELECT id,count(*) OVER(ORDER BY k RANGE BETWEEN $1 PRECEDING AND $2 FOLLOWING) AS n FROM range_t ORDER BY id"
		s := queryWindowRangeResult(q, 3, 3, 3, 2, 2, 2, 2)
		s.Args = []any{1, 1}
		queryWindowRoutes(t, p, b, s)
	})
}
