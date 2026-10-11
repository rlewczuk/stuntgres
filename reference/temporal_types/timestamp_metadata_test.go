package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimestampPrecisionAndCivilParameters(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE initial(id INTEGER,v TIMESTAMP,p TIMESTAMP(3) WITHOUT TIME ZONE)", 0))
			run(fixture.Exec("INSERT INTO initial VALUES(1,'2004-10-19 10:23:54+02','2004-10-19 10:23:54.1236'),(2,NULL,NULL)", 2))
			run(fixture.Select("SELECT * FROM initial ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "TIMESTAMP", "TIMESTAMP"}, [][]any{{int64(1), time.Date(2004, 10, 19, 10, 23, 54, 0, time.UTC), time.Date(2004, 10, 19, 10, 23, 54, 124000000, time.UTC)}, {int64(2), nil, nil}}))
			run(fixture.Exec("CREATE TABLE anchor(n INTEGER)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES(1)", 1))
			for prec, ns := range []int{0, 100000000, 120000000, 123000000, 123500000, 123460000, 123456000} {
				table := fmt.Sprintf("p%d", prec)
				run(fixture.Prepared(fixture.Exec(fmt.Sprintf("CREATE TABLE %s(v TIMESTAMP(%d) WITHOUT TIME ZONE)", table, prec), 0)))
				run(fixture.Prepared(fixture.Exec("INSERT INTO "+table+" VALUES($1)", 1, "2000-01-01 00:00:00.123456")))
				want := time.Date(2000, 1, 1, 0, 0, 0, ns, time.UTC)
				run(fixture.Select("SELECT v FROM "+table, []string{"v"}, []string{"TIMESTAMP"}, [][]any{{want}}))
				run(fixture.Prepared(fixture.Select(fmt.Sprintf("SELECT TIMESTAMP(%d) '2000-01-01 00:00:00.123456' FROM anchor", prec), []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{want}})))
				run(fixture.Select(fmt.Sprintf("SELECT TIMESTAMP(%d) '2000-01-01 00:00:00.123456' FROM initial WHERE id=1", prec), []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{want}}))
			}
			run(fixture.Exec("CREATE TABLE sized(v TIMESTAMP(2))", 0))
			run(fixture.Exec("INSERT INTO sized VALUES('2000-01-01 00:00:00.1245'),(TIMESTAMP(3) '2000-01-01 00:00:00.1245')", 2))
			run(fixture.Select("SELECT v FROM sized ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 120000000, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 130000000, time.UTC)}}))
			for _, tc := range []struct {
				q    string
				want time.Time
			}{
				{"TIMESTAMP(0) '2000-01-01 00:00:00.5'", time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC)},
				{"TIMESTAMP(0) '1999-12-31 23:59:59.5'", time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC)},
				{"TIMESTAMP(3) '1999-12-31 23:59:59.9996'", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{"TIMESTAMP '2000-01-01 00:00:00.0000005'", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{"TIMESTAMP '2000-01-01 00:00:00.0000015'", time.Date(2000, 1, 1, 0, 0, 0, 2000, time.UTC)},
				{"TIMESTAMP(0) '294276-12-31 23:59:59.999999'", time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)},
			} {
				run(fixture.Prepared(fixture.Select("SELECT "+tc.q+" FROM anchor", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{tc.want}})))
			}
			run(fixture.Exec("CREATE TABLE params(id INTEGER,v TIMESTAMP(6))", 0))
			for i, year := range []int{0, -98, 2004, 12000} {
				raw := time.Date(year, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 7200))
				run(fixture.Prepared(fixture.Exec("INSERT INTO params VALUES($1,$2)", 1, i+1, raw)))
				run(fixture.Select("SELECT v FROM params WHERE id=$1", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(year, 10, 19, 10, 23, 54, 123457000, time.UTC)}}, i+1))
			}
			for i, raw := range []any{[]byte("infinity"), "-infinity", nil} {
				run(fixture.Prepared(fixture.Exec("INSERT INTO params VALUES($1,$2)", 1, i+5, raw)))
			}
			run(fixture.Select("SELECT v FROM params WHERE id=5", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{"infinity"}}))
			run(fixture.Select("SELECT v FROM params WHERE id=6", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{"-infinity"}}))
			run(fixture.Select("SELECT v FROM params WHERE id=7", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{nil}}))
			// Assert zero offset and Scan(*time.Time) independently, before parity normalization.
			{
				db := b.DB
				rows, e := db.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), "SELECT v FROM params WHERE id=3")
				if e != nil {
					t.Fatal(e)
				}
				if !rows.Next() {
					t.Fatal("missing finite")
				}
				var got time.Time
				if e = rows.Scan(&got); e != nil {
					t.Fatal(e)
				}
				rows.Close()
				_, off := got.Zone()
				if off != 0 || got.Hour() != 10 || got.Nanosecond() != 123457000 {
					t.Fatal(got)
				}
			}
		})
	}
}

func TestReferenceTimestampCivilParameterOwnership(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			referenceTimestampExec(t, ctx, db, "CREATE TABLE params(v TIMESTAMP(6))")
			ins, e := db.PrepareContext(ctx, "INSERT INTO params VALUES($1)")
			if e != nil {
				t.Fatal(e)
			}
			defer ins.Close()
			for _, year := range []int{0, -98, 2004, 12000} {
				raw := time.Date(year, 10, 19, 10, 23, 54, 123456600, time.FixedZone("east", 7200))
				if _, e = ins.ExecContext(ctx, raw); e != nil {
					t.Fatal(e)
				}
			}
			for _, raw := range []any{[]byte("infinity"), "-infinity", nil} {
				if _, e = ins.ExecContext(ctx, raw); e != nil {
					t.Fatal(e)
				}
			}
			referenceTimestampQuery(t, ctx, db, backend.Recorder, "SELECT v FROM params ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{[]byte("-infinity")}, {time.Date(-98, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {time.Date(0, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {time.Date(12000, 10, 19, 10, 23, 54, 123457000, time.UTC)}, {[]byte("infinity")}, {nil}})
			rows, e := db.QueryContext(ctx, "SELECT v FROM params WHERE v='infinity'")
			if e != nil {
				t.Fatal(e)
			}
			if !rows.Next() {
				t.Fatal("missing infinity")
			}
			var owned []byte
			if e = rows.Scan(&owned); e != nil {
				t.Fatal(e)
			}
			rows.Close()
			owned[0] = 'X'
			referenceTimestampQuery(t, ctx, db, backend.Recorder, "SELECT v FROM params WHERE v='infinity'", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{[]byte("infinity")}})
			rows, e = db.QueryContext(ctx, "SELECT v FROM params WHERE v='2004-10-19 10:23:54.123457'")
			if e != nil {
				t.Fatal(e)
			}
			if !rows.Next() {
				t.Fatal("missing finite")
			}
			var finite time.Time
			if e = rows.Scan(&finite); e != nil {
				t.Fatal(e)
			}
			rows.Close()
			if finite.Hour() != 10 {
				t.Fatal(finite)
			}
		})
	}
}
