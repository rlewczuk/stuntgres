package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowExcludeScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(p, b) })
	}
}

func queryWindowExcludeSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE ex_t(id INT,k INT,v INT)", 0))
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO ex_t VALUES(1,1,10),(2,1,20),(3,2,30)", 3))
}

func queryWindowExcludeResult(q string, counts []int64, sums []any) fixture.Step {
	rows := make([][]any, len(counts))
	for i, n := range counts {
		rows[i] = []any{int64(i + 1), n, sums[i]}
	}
	return fixture.Select(q, []string{"id", "n", "s"}, []string{"INT4", "INT8", "INT8"}, rows)
}

func queryWindowExcludeQuery(spec string) string {
	return "SELECT id,count(*) OVER(" + spec + ") AS n,sum(v) OVER(" + spec + ") AS s FROM ex_t ORDER BY id"
}

func queryWindowExcludeMetadata(pg bool, nullable ...bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != len(nullable) {
			t.Fatalf("column count %d want %d", len(cols), len(nullable))
		}
		for i, c := range cols {
			want := nullable[i] && !pg
			if n, known := c.Nullable(); n != want || known != !pg {
				t.Errorf("column %s Nullable=%v/%v want %v/%v", c.Name(), n, known, want, !pg)
			}
			switch c.DatabaseTypeName() {
			case "INT8", "NUMERIC", "FLOAT4", "FLOAT8":
				want := reflect.TypeOf(int64(0))
				switch c.DatabaseTypeName() {
				case "NUMERIC":
					want = reflect.TypeOf("")
					if pg {
						want = reflect.TypeOf((*any)(nil)).Elem()
					}
				case "FLOAT4":
					want = reflect.TypeOf(float64(0))
					if pg {
						want = reflect.TypeOf(float32(0))
					}
				case "FLOAT8":
					want = reflect.TypeOf(float64(0))
				}
				if c.ScanType() != want {
					t.Errorf("column %s ScanType=%v want %v", c.Name(), c.ScanType(), want)
				}
				precision, scale, known := c.DecimalSize()
				if pg && c.DatabaseTypeName() == "NUMERIC" {
					if precision != 65535 || scale != 65531 || !known {
						t.Errorf("NUMERIC DecimalSize=%d/%d/%v", precision, scale, known)
					}
				} else if precision != 0 || scale != 0 || known {
					t.Errorf("column %s DecimalSize=%d/%d/%v", c.Name(), precision, scale, known)
				}
				if length, known := c.Length(); length != 0 || known {
					t.Errorf("column %s Length=%d/%v", c.Name(), length, known)
				}
			}
		}
	}
}

func queryWindowExcludeFull(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, unit := range []string{"ROWS", "RANGE", "GROUPS"} {
		for _, tc := range []struct {
			mode   string
			counts []int64
			sums   []any
		}{
			{"", []int64{3, 3, 3}, []any{int64(60), int64(60), int64(60)}},
			{"NO OTHERS", []int64{3, 3, 3}, []any{int64(60), int64(60), int64(60)}},
			{"CURRENT ROW", []int64{2, 2, 2}, []any{int64(50), int64(40), int64(30)}},
			{"GROUP", []int64{1, 1, 2}, []any{int64(30), int64(30), int64(30)}},
			{"TIES", []int64{2, 2, 3}, []any{int64(40), int64(50), int64(60)}},
		} {
			spec := "ORDER BY k " + unit + " BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING"
			if tc.mode != "" {
				spec += " EXCLUDE " + tc.mode
			}
			s := queryWindowExcludeResult(queryWindowExcludeQuery(spec), tc.counts, tc.sums)
			s.CheckMetadata = queryWindowExcludeMetadata(b.PG, true, false, true)
			queryWindowRoutes(t, p, b, s)
		}
	}
}

func TestReferenceQueryWindowExcludeExecution(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		queryWindowExcludeFull(t, p, b)
	})
}

func TestReferenceQueryWindowExcludeMembership(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		for _, mode := range []string{"NO OTHERS", "CURRENT ROW", "GROUP", "TIES"} {
			for _, unit := range []string{"ROWS", "RANGE", "GROUPS"} {
				counts, sums := []int64{1, 1, 1}, []any{int64(10), int64(20), int64(30)}
				if unit != "ROWS" {
					counts, sums = []int64{2, 2, 1}, []any{int64(30), int64(30), int64(30)}
				}
				if mode == "GROUP" || (mode == "CURRENT ROW" && unit == "ROWS") {
					counts, sums = []int64{0, 0, 0}, []any{nil, nil, nil}
				}
				if mode == "CURRENT ROW" && unit != "ROWS" {
					counts, sums = []int64{1, 1, 0}, []any{int64(20), int64(10), nil}
				}
				if mode == "TIES" {
					counts, sums = []int64{1, 1, 1}, []any{int64(10), int64(20), int64(30)}
				}
				queryWindowRoutes(t, p, b, queryWindowExcludeResult(queryWindowExcludeQuery("ORDER BY k "+unit+" CURRENT ROW EXCLUDE "+mode), counts, sums))
			}
			queryWindowRoutes(t, p, b, queryWindowExcludeResult(queryWindowExcludeQuery("ORDER BY k RANGE BETWEEN 1 FOLLOWING AND 1 FOLLOWING EXCLUDE "+mode), []int64{1, 1, 0}, []any{int64(30), int64(30), nil}))
			queryWindowRoutes(t, p, b, queryWindowExcludeResult(queryWindowExcludeQuery("ORDER BY id ROWS BETWEEN 1 PRECEDING AND 2 PRECEDING EXCLUDE "+mode), []int64{0, 0, 0}, []any{nil, nil, nil}))
		}
		for _, tc := range []struct {
			mode string
			n    []int64
			s    []any
		}{
			{"GROUP", []int64{0, 0, 0}, []any{nil, nil, nil}},
			{"TIES", []int64{1, 1, 1}, []any{int64(10), int64(20), int64(30)}},
			{"CURRENT ROW", []int64{2, 2, 2}, []any{int64(50), int64(40), int64(30)}},
		} {
			queryWindowRoutes(t, p, b, queryWindowExcludeResult(queryWindowExcludeQuery("ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE "+tc.mode), tc.n, tc.s))
		}
	})
}

func TestReferenceQueryWindowExcludePartitions(t *testing.T) {
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE peers(id INT,p INT,a INT,b INT,v INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO peers VALUES(1,1,NULL,1,10),(2,1,NULL,1,20),(3,1,NULL,2,30),(4,2,1,1,40)", 4))
		q := "SELECT id,count(*) OVER w AS n,sum(v) OVER w AS s FROM peers WINDOW w AS(PARTITION BY p ORDER BY a DESC NULLS FIRST,b DESC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE GROUP) ORDER BY id"
		queryWindowRoutes(t, p, b, fixture.Select(q, []string{"id", "n", "s"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(30)}, {int64(2), int64(1), int64(30)}, {int64(3), int64(2), int64(30)}, {int64(4), int64(0), nil}}))
		// A ROWS prefix intersects a tied peer group only partially; the bag
		// avoids assigning a physical order to the two equal ORDER keys.
		queryWindowRoutes(t, p, b, fixture.Select("SELECT n,s FROM (SELECT id,count(*) OVER w AS n,sum(v) OVER w AS s FROM peers WHERE p=1 WINDOW w AS(ORDER BY a,b ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW EXCLUDE GROUP)) x WHERE id<3", []string{"n", "s"}, []string{"INT8", "INT8"}, [][]any{{int64(0), nil}, {int64(0), nil}}))
	})
}
