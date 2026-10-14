package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowValueWhole(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRoutes(t, p, b, fixture.Select("SELECT id,last_value(v) OVER w AS l,nth_value(v,3) OVER w AS n3,nth_value(v,4) OVER w AS n4,nth_value(v,n) OVER w AS current_n FROM wv WINDOW w AS(PARTITION BY p ORDER BY k,id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY id", []string{"id", "l", "n3", "n4", "current_n"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{
		{int64(1), int64(40), nil, int64(40), int64(10)}, {int64(2), int64(40), nil, int64(40), int64(10)}, {int64(3), int64(40), nil, int64(40), int64(10)}, {int64(4), int64(40), nil, int64(40), nil}, {int64(5), int64(40), nil, int64(40), nil}, {int64(6), int64(70), nil, nil, nil}, {int64(7), int64(70), nil, nil, nil},
	}))
}

func queryWindowValueExcluded(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRoutes(t, p, b, fixture.Select("SELECT id,first_value(id) OVER w AS f,last_value(id) OVER w AS l,nth_value(id,2) OVER w AS n FROM wv WHERE id<=4 WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) ORDER BY id", []string{"id", "f", "l", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(4), int64(3)}, {int64(2), int64(1), int64(4), int64(3)}, {int64(3), int64(1), int64(4), int64(2)}, {int64(4), int64(1), int64(3), int64(2)}}))
}

func TestReferenceQueryWindowValueFrames(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowValueSeed(t, p, b)
		t.Run("whole", func(t *testing.T) { queryWindowValueWhole(t, p, b) })
		t.Run("current-exclusion", func(t *testing.T) { queryWindowValueExcluded(t, p, b) })
		for _, frame := range []string{"ROWS CURRENT ROW", "ROWS BETWEEN 1 PRECEDING AND 2 PRECEDING", "ROWS CURRENT ROW EXCLUDE CURRENT ROW"} {
			t.Run(frame, func(t *testing.T) {
				rows := [][]any{{int64(1), nil, nil, nil}, {int64(2), nil, nil, nil}, {int64(3), nil, nil, nil}, {int64(4), nil, nil, nil}, {int64(5), nil, nil, nil}, {int64(6), nil, nil, nil}, {int64(7), nil, nil, nil}}
				if frame == "ROWS CURRENT ROW" {
					values := []any{int64(10), int64(10), nil, int64(40), int64(40), nil, int64(70)}
					for i, v := range values {
						rows[i][1], rows[i][2] = v, v
					}
				}
				queryWindowRoutes(t, p, b, fixture.Select("SELECT id,first_value(v) OVER w AS f,last_value(v) OVER w AS l,nth_value(v,2) OVER w AS n FROM wv WINDOW w AS(ORDER BY id "+frame+") ORDER BY id", []string{"id", "f", "l", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, rows))
			})
		}
		t.Run("descending", func(t *testing.T) {
			queryWindowRoutes(t, p, b, fixture.Select("SELECT id,first_value(id) OVER w AS f,last_value(id) OVER w AS l,nth_value(id,2) OVER w AS n FROM wv WHERE id<=4 WINDOW w AS(ORDER BY id DESC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY id", []string{"id", "f", "l", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(4), int64(1), int64(3)}, {int64(2), int64(4), int64(1), int64(3)}, {int64(3), int64(4), int64(1), int64(3)}, {int64(4), int64(4), int64(1), int64(3)}}))
		})
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE wv_peers(id INT,k INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wv_peers VALUES(1,1),(2,1),(3,2),(4,3)", 4))
		for _, tc := range []struct {
			frame string
			rows  [][]any
		}{
			{"ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE GROUP", [][]any{{int64(1), int64(2), int64(3), int64(3)}, {int64(2), int64(2), int64(3), int64(3)}, {int64(3), int64(1), int64(3), int64(1)}, {int64(4), int64(1), int64(2), int64(1)}}},
			{"ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES", [][]any{{int64(1), int64(1), int64(3), int64(2)}, {int64(2), int64(1), int64(3), int64(2)}, {int64(3), int64(1), int64(3), int64(1)}, {int64(4), int64(1), int64(3), int64(1)}}},
			{"GROUPS CURRENT ROW EXCLUDE GROUP", [][]any{{int64(1), nil, nil, nil}, {int64(2), nil, nil, nil}, {int64(3), nil, nil, nil}, {int64(4), nil, nil, nil}}},
			{"RANGE CURRENT ROW", [][]any{{int64(1), int64(1), int64(1), int64(1)}, {int64(2), int64(1), int64(1), int64(1)}, {int64(3), int64(2), int64(2), nil}, {int64(4), int64(3), int64(3), nil}}},
			{"GROUPS CURRENT ROW", [][]any{{int64(1), int64(1), int64(1), int64(1)}, {int64(2), int64(1), int64(1), int64(1)}, {int64(3), int64(2), int64(2), nil}, {int64(4), int64(3), int64(3), nil}}},
			{"ROWS CURRENT ROW", [][]any{{int64(1), int64(1), int64(1), nil}, {int64(2), int64(1), int64(1), nil}, {int64(3), int64(2), int64(2), nil}, {int64(4), int64(3), int64(3), nil}}},
			{"ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE NO OTHERS", [][]any{{int64(1), int64(1), int64(3), int64(1)}, {int64(2), int64(1), int64(3), int64(1)}, {int64(3), int64(1), int64(3), int64(1)}, {int64(4), int64(1), int64(3), int64(1)}}},
			{"GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING", [][]any{{int64(1), int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(2), int64(1)}, {int64(3), int64(1), int64(3), int64(1)}, {int64(4), int64(2), int64(3), int64(3)}}},
			{"RANGE BETWEEN 1 PRECEDING AND 1 FOLLOWING", [][]any{{int64(1), int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(2), int64(1)}, {int64(3), int64(1), int64(3), int64(1)}, {int64(4), int64(2), int64(3), int64(3)}}},
		} {
			t.Run(tc.frame, func(t *testing.T) {
				queryWindowRoutes(t, p, b, fixture.Select("SELECT id,first_value(k) OVER w AS f,last_value(k) OVER w AS l,nth_value(k,2) OVER w AS n FROM wv_peers WINDOW w AS(ORDER BY k "+tc.frame+") ORDER BY id", []string{"id", "f", "l", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, tc.rows))
			})
		}
	})
}
