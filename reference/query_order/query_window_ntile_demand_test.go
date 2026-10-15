package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryWindowNtileArgumentDemand(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowNtileSeed(t, p, b)
		for _, tc := range []struct {
			name, expr string
			labels     []any
		}{
			{"positive-first", "CASE WHEN id=1 THEN 2 WHEN id=2 THEN 0 WHEN id=3 THEN NULL WHEN id=4 THEN -1 ELSE 99 END", []any{int64(1), int64(1), int64(1), int64(2), int64(2)}},
			{"one-null", "b", []any{nil, int64(1), int64(1), int64(1), int64(2)}},
			{"two-nulls", "CASE WHEN id<=2 THEN NULL WHEN id=3 THEN 2 WHEN id=4 THEN 0 ELSE -1 END", []any{nil, nil, int64(1), int64(1), int64(1)}},
			{"all-null", "NULL", []any{nil, nil, nil, nil, nil}},
			{"later-error-not-evaluated", "CASE WHEN id=1 THEN 2 ELSE 1/(id-id) END", []any{int64(1), int64(1), int64(1), int64(2), int64(2)}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				queryWindowRoutes(t, p, b, queryWindowNtileLabels("SELECT id,ntile("+tc.expr+") OVER(ORDER BY id) AS n FROM nt WHERE id<=5 ORDER BY id", tc.labels...))
			})
		}
		t.Run("reached-zero-after-null", func(t *testing.T) {
			// Opposite outer ordering demands every window output before any
			// row can escape. The shared diagnostic helper checks Query's error
			// before draining a cursor, so an initial NULL must not stream first.
			queryWindowValueDiagnosticRoutes(t, p, b, "SELECT ntile(CASE WHEN id=1 THEN NULL ELSE 0 END) OVER(ORDER BY id) FROM nt WHERE id<=5 ORDER BY id DESC", fixture.Diagnostic{Code: "22014", Message: "argument of ntile must be greater than zero"}, "execute")
		})
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,ntile(2) OVER w AS a,ntile(3) OVER w AS b FROM nt WHERE id<=5 WINDOW w AS(ORDER BY id) ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(1), int64(1)}, {int64(3), int64(1), int64(2)}, {int64(4), int64(2), int64(2)}, {int64(5), int64(2), int64(3)}}))
		queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE nt_seq START WITH 1", 0))
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("partition-effects/prepared=%t", prepared), func(t *testing.T) {
				queryWindowRun(t, p, b, fixture.Exec("ALTER SEQUENCE nt_seq RESTART WITH 1", 0))
				// Use two three-row partitions, independent of the seed's p split.
				s := queryWindowNtileLabels("SELECT id,ntile(nextval('nt_seq')::int) OVER(PARTITION BY (id>3) ORDER BY id) AS n FROM nt WHERE id<=6 ORDER BY id", int64(1), int64(1), int64(1), int64(1), int64(1), int64(2))
				s.Prepared = prepared
				queryWindowRun(t, p, b, s)
				queryWindowRun(t, p, b, fixture.Select("SELECT nextval('nt_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			})
			for _, tc := range []struct {
				name, target, order string
				shared              bool
			}{
				{"whole-target-alias", "ntile(nextval('nt_seq')::int) OVER w AS a,ntile(nextval('nt_seq')::int) OVER w AS b", "a", true},
				{"independent-occurrences", "ntile(nextval('nt_seq')::int) OVER w AS a,ntile(nextval('nt_seq')::int) OVER w AS b", "id", false},
				{"different-arguments", "ntile(nextval('nt_seq')::int) OVER w AS a,ntile((nextval('nt_seq')+0)::int) OVER w AS b", "a,b", false},
			} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
					queryWindowRun(t, p, b, fixture.Exec("ALTER SEQUENCE nt_seq RESTART WITH 1", 0))
					q := "SELECT " + tc.target + " FROM nt WHERE id<=5 WINDOW w AS(ORDER BY id) ORDER BY " + tc.order
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := b.DB.PrepareContext(p.Ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = b.DB.QueryContext(p.Ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != 2 {
						t.Fatal(columns, err)
					}
					for i, c := range columns {
						if c.Name() != []string{"a", "b"}[i] || c.DatabaseTypeName() != "INT4" {
							t.Fatal("volatile metadata", c)
						}
					}
					// No call scheduling assumption: exactly one B1 and one B2
					// initialized call, unless the whole upper target owns both labels.
					var got [2][]int64
					for rows.Next() {
						var a, c int64
						if err := rows.Scan(&a, &c); err != nil {
							t.Fatal(err)
						}
						got[0] = append(got[0], a)
						got[1] = append(got[1], c)
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if len(got[0]) != 5 {
						t.Fatal("volatile row count", got)
					}
					allOne, split := 0, 0
					for _, col := range got {
						if fmt.Sprint(col) == "[1 1 1 1 1]" {
							allOne++
						} else if fmt.Sprint(col) == "[1 1 1 2 2]" {
							split++
						} else {
							t.Fatal("unexpected volatile labels", got)
						}
					}
					calls := int64(2)
					if tc.shared {
						calls = 1
						if allOne != 2 || split != 0 {
							t.Fatal("upper target not reused", got)
						}
					} else if allOne != 1 || split != 1 {
						t.Fatal("independent calls merged", got)
					}
					queryWindowRun(t, p, b, fixture.Select("SELECT last_value FROM nt_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{calls}}))
				})
			}
		}
	})
}

func TestReferenceQueryWindowNtileFrames(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowNtileSeed(t, p, b)
		for _, frame := range []string{"", "ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING", "ROWS CURRENT ROW", "RANGE CURRENT ROW", "GROUPS CURRENT ROW"} {
			for _, exclude := range []string{"", " EXCLUDE NO OTHERS", " EXCLUDE CURRENT ROW", " EXCLUDE GROUP", " EXCLUDE TIES"} {
				if frame == "" && exclude != "" {
					continue
				}
				t.Run(frame+exclude, func(t *testing.T) {
					queryWindowRoutes(t, p, b, queryWindowNtileLabels("SELECT id,ntile(4) OVER(ORDER BY id "+frame+exclude+") AS n FROM nt WHERE id<=6 ORDER BY id", int64(1), int64(1), int64(2), int64(2), int64(3), int64(4)))
				})
			}
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,ntile(4) OVER w AS n,count(*) OVER w AS c FROM nt WHERE id<=6 WINDOW w AS(ORDER BY id ROWS CURRENT ROW EXCLUDE CURRENT ROW) ORDER BY id", []string{"id", "n", "c"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(0)}, {int64(2), int64(1), int64(0)}, {int64(3), int64(2), int64(0)}, {int64(4), int64(2), int64(0)}, {int64(5), int64(3), int64(0)}, {int64(6), int64(4), int64(0)}}))
		for _, tc := range []struct {
			arg           any
			code, message string
		}{{nil, "22004", "frame starting offset must not be null"}, {-1, "22013", "frame starting offset must not be negative"}} {
			t.Run(tc.code, func(t *testing.T) {
				queryWindowRoutes(t, p, b, fixture.Select("SELECT ntile(2) OVER(ORDER BY id ROWS $1 PRECEDING) AS n FROM nt WHERE id<=3 ORDER BY id", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}, tc.arg))
				queryWindowValueDiagnosticRoutes(t, p, b, "SELECT ntile(2) OVER w,count(*) OVER w FROM nt WINDOW w AS(ORDER BY id ROWS $1 PRECEDING)", fixture.Diagnostic{Code: tc.code, Message: tc.message}, "execute", tc.arg)
			})
		}
	})
}
