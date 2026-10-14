package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowValueVolatile(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE wv_seq START WITH 1", 0))
	const tail = " FROM wv WHERE id<=4 WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)"
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, target, order string
			columns             []string
			rows                [][]any
		}{
			{"single", "first_value(nextval('wv_seq')) OVER w AS a", "a", []string{"a"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
			{"single-last", "last_value(nextval('wv_seq')) OVER w AS a", "a", []string{"a"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
			{"single-nth", "nth_value(nextval('wv_seq'),2) OVER w AS a", "a", []string{"a"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
			// REL_18_0 make_sort_input_target postpones volatile nonsort b,
			// then add_new_columns_to_pathtarget matches its whole WindowFunc
			// to sort target a. The upper projection reads a for both labels;
			// this is not a value-provider cache or blanket volatile-call merge.
			{"identical-upper-target", "first_value(nextval('wv_seq')) OVER w AS a,first_value(nextval('wv_seq')) OVER w AS b", "a", []string{"a", "b"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}},
			{"identical-later-required-target", "first_value(nextval('wv_seq')) OVER w AS a,first_value(nextval('wv_seq')) OVER w AS b", "b", []string{"a", "b"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}},
			{"hidden-whole-owner", "first_value(nextval('wv_seq')) OVER w+1 AS a", "first_value(nextval('wv_seq')) OVER w", []string{"a"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}},
		} {
			t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
				queryWindowRun(t, p, b, fixture.Exec("ALTER SEQUENCE wv_seq RESTART WITH 1", 0))
				types := []string{"INT8"}
				if len(tc.columns) == 2 {
					types = append(types, "INT8")
				}
				s := fixture.Select("SELECT "+tc.target+tail+" ORDER BY "+tc.order, tc.columns, types, tc.rows)
				s.Prepared = prepared
				queryWindowRun(t, p, b, s)
				queryWindowRun(t, p, b, fixture.Select("SELECT last_value FROM wv_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(4)}}))
			})
		}
		for _, tc := range []struct {
			name, second, order string
			firstAdjustment     int64
		}{
			{"different-arguments", "nextval('wv_seq')+0", "a", 0},
			{"identical-two-sort-targets", "nextval('wv_seq')", "a,b", 0},
			{"different-arguments-order-id", "nextval('wv_seq')+0", "id", 0},
			{"identical-postponed-targets", "nextval('wv_seq')", "id", 0},
			{"identical-no-outer-order", "nextval('wv_seq')", "", 0},
			{"retained-wrapper", "nextval('wv_seq')", "a", 1},
		} {
			t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
				queryWindowRun(t, p, b, fixture.Exec("ALTER SEQUENCE wv_seq RESTART WITH 1", 0))
				first := "first_value(nextval('wv_seq')) OVER w"
				if tc.firstAdjustment != 0 {
					first += fmt.Sprintf("+%d", tc.firstAdjustment)
				}
				q := "SELECT " + first + " AS a,first_value(" + tc.second + ") OVER w AS b" + tail
				if tc.order != "" {
					q += " ORDER BY " + tc.order
				}
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, prepareErr := b.DB.PrepareContext(p.Ctx, q)
					if prepareErr != nil {
						t.Fatal(prepareErr)
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
				for i, column := range columns {
					if column.Name() != []string{"a", "b"}[i] || column.DatabaseTypeName() != "INT8" {
						t.Fatal("volatile metadata", i, column)
					}
				}
				// Assert the entire independently expected value set and effect,
				// without inventing which executing call consumes each value.
				var seen [9]bool
				count := 0
				for rows.Next() {
					var a, second int64
					if err := rows.Scan(&a, &second); err != nil {
						t.Fatal(err)
					}
					a -= tc.firstAdjustment
					for _, value := range []int64{a, second} {
						if value < 1 || value > 8 || seen[value] {
							t.Fatalf("independent targets reused/unexpected volatile result: a=%d b=%d", a, second)
						}
						seen[value] = true
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if count != 4 {
					t.Fatal("volatile output count", count)
				}
				for value := 1; value <= 8; value++ {
					if !seen[value] {
						t.Fatal("missing volatile value", value)
					}
				}
				queryWindowRun(t, p, b, fixture.Select("SELECT last_value FROM wv_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(8)}}))
			})
		}
	}
}
