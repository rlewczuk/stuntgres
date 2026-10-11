package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// intervalAvgScenario shares every SQL assertion between quick and real modes.
func intervalAvgScenario(t *testing.T, exercise func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			exercise(t, p, conn, backend.Recorder, backend.PG)
		})
	}
}

func intervalAvgBoth(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
}

func TestReferenceIntervalAvgArithmetic(t *testing.T) {
	intervalAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("SET IntervalStyle = 'postgres'", 0))
		run(fixture.Exec("CREATE TABLE t (g INTEGER, v INTERVAL, b BOOLEAN)", 0))
		vectors := []struct{ name, values, want string }{
			{"month_fraction", "('1 mon'),('0')", "15 days"},
			{"day_fraction", "('1 day'),('0')", "12:00:00"},
			{"time_fraction", "('24 hours'),('0')", "12:00:00"},
			{"not_justified_month", "('1 mon'),('30 days')", "30 days"},
			{"not_justified_day", "('1 day'),('24 hours')", "24:00:00"},
			{"negative_month", "('-1 mon'),('0')", "-15 days"},
			{"negative_day", "('-1 day'),('0')", "-12:00:00"},
			{"mixed_sign", "('1 mon -1 day'),('0')", "15 days -12:00:00"},
			{"month_tsround", "('1 mon'),('0'),('0'),('0'),('0'),('0'),('0')", "4 days 06:51:25.6896"},
			{"tie_one", "('1 microsecond'),('0')", "00:00:00"},
			{"tie_three", "('3 microseconds'),('0')", "00:00:00.000002"},
			{"tie_five", "('5 microseconds'),('0')", "00:00:00.000002"},
			{"tie_negative_one", "('-1 microsecond'),('0')", "00:00:00"},
			{"tie_negative_three", "('-3 microseconds'),('0')", "-00:00:00.000002"},
			{"tie_negative_five", "('-5 microseconds'),('0')", "-00:00:00.000002"},
			{"day_carry", "('1 mon 1 day'),('0')", "15 days 12:00:00"},
			{"tsround_day_carry", "('1 mon 1 day')" + strings.Repeat(",('0')", 30), "1 day 00:00:00.005574"},
			{"tsround_negative_day_carry", "('-1 mon -1 day')" + strings.Repeat(",('0')", 30), "-1 days -00:00:00.005574"},
			{"zero", "('0'),('0')", "00:00:00"},
		}
		var previous int64
		for _, v := range vectors {
			t.Run(v.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				run(fixture.Exec("DELETE FROM t", previous))
				// Only interval values are inserted; nullable sibling columns stay NULL.
				run(fixture.Exec("INSERT INTO t(v) VALUES "+v.values, int64(countIntervalAvgValues(v.values))))
				previous = int64(countIntervalAvgValues(v.values))
				intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{v.want}}))
			})
		}
		run(fixture.Exec("DELETE FROM t", 2))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{nil}}))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT g,avg(v) FROM t GROUP BY g", []string{"g", "avg"}, []string{"INT4", "INTERVAL"}, [][]any{}))
		run(fixture.Exec("INSERT INTO t VALUES (1,'1 day',TRUE),(1,'3 days',FALSE),(1,NULL,NULL),(2,NULL,TRUE),(2,NULL,NULL),(3,'0',TRUE)", 6))
		for _, query := range []string{"SELECT avg(v) FROM t WHERE g=1", "SELECT pg_catalog.avg(v) FROM t WHERE g=1", "SELECT \"avg\"(v) FROM t WHERE g=1", "SELECT \"pg_catalog\".\"avg\"(v) FROM t WHERE g=1"} {
			intervalAvgBoth(t, p, conn, recorder, fixture.Select(query, []string{"avg"}, []string{"INTERVAL"}, [][]any{{"2 days"}}))
		}
		run(fixture.Exec("SET search_path = missing_avg_schema", 0))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM public.t WHERE g=1", []string{"avg"}, []string{"INTERVAL"}, [][]any{{"2 days"}}))
		run(fixture.Exec("SET search_path = public", 0))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM t WHERE FALSE", []string{"avg"}, []string{"INTERVAL"}, [][]any{{nil}}))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM t WHERE g=2", []string{"avg"}, []string{"INTERVAL"}, [][]any{{nil}}))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT g,avg(v),avg(v) AS repeated,count(v),bool_and(b),bool_or(b) FROM t GROUP BY g ORDER BY g", []string{"g", "avg", "repeated", "count", "bool_and", "bool_or"}, []string{"INT4", "INTERVAL", "INTERVAL", "INT8", "BOOL", "BOOL"}, [][]any{{int64(1), "2 days", "2 days", int64(2), false, true}, {int64(2), nil, nil, int64(0), true, true}, {int64(3), "00:00:00", "00:00:00", int64(1), true, true}}))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT g FROM t GROUP BY g HAVING avg(v)=INTERVAL '2 days' ORDER BY avg(v)", []string{"g"}, []string{"INT4"}, [][]any{{int64(1)}}))
		for _, arg := range []any{"1 day", []byte("1 day"), nil} {
			var want any = "1 day"
			if arg == nil {
				want = nil
			}
			intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg($1::interval) FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{want}}, arg))
		}
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(NULL::interval) FROM t", []string{"avg"}, []string{"INTERVAL"}, [][]any{{nil}}))
		run(fixture.Exec("CREATE TABLE precise (v INTERVAL SECOND(0) NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO precise VALUES ('1 second'),('0')", 2))
		intervalAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM precise", []string{"avg"}, []string{"INTERVAL"}, [][]any{{"00:00:00.5"}}))
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct_metadata", true: "prepared_metadata"}[prepared], func(t *testing.T) {
				var executor fixture.Executor = conn
				if prepared {
					stmt, err := conn.PrepareContext(p.Ctx, "SELECT avg(v) FROM precise")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: "SELECT avg(v) FROM precise"}
				}
				rows, err := executor.QueryContext(p.Ctx, "SELECT avg(v) FROM precise")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(columns) != 1 {
					t.Fatalf("columns: %v", columns)
				}
				c := columns[0]
				if c.Name() != "avg" || c.DatabaseTypeName() != "INTERVAL" {
					t.Errorf("metadata: %s/%s", c.Name(), c.DatabaseTypeName())
				}
				wantScan := reflect.TypeFor[string]()
				if pg {
					wantScan = reflect.TypeFor[any]()
				}
				if c.ScanType() != wantScan {
					t.Errorf("ScanType: %v want %v", c.ScanType(), wantScan)
				}
				if n, ok := c.Length(); ok {
					t.Errorf("unexpected Length %d", n)
				}
				if a, b, ok := c.DecimalSize(); ok {
					t.Errorf("unexpected DecimalSize %d,%d", a, b)
				}
				if !pg {
					if nullable, known := c.Nullable(); !known || !nullable {
						t.Errorf("AVG nullability: %v,%v", nullable, known)
					}
				}
			})
		}
	})
}

func countIntervalAvgValues(values string) int {
	n := 0
	for _, c := range values {
		if c == '(' {
			n++
		}
	}
	return n
}
