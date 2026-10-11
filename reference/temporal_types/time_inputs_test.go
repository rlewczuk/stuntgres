package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeAcceptedInputsPrecisionAndOrdering(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE anchor(id INTEGER)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES(1)", 1))
			run(fixture.Exec("CREATE TABLE inputs(id INTEGER,v TIME)", 0))
			insertQuery := "INSERT INTO inputs VALUES($1,$2)"
			insert, err := backend.DB.PrepareContext(p.Ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := insert.Close(); err != nil {
					t.Error(err)
				}
			}()
			heldInsert := fixture.HeldStatement{Executor: backend.DB, Statement: insert, SQL: insertQuery}
			for i, tc := range []struct{ input, want string }{
				{"04:05:06.789", "04:05:06.789"}, {"04:05:06", "04:05:06"}, {"04:05", "04:05:00"}, {"040506", "04:05:06"}, {"0405", "04:05:00"}, {"04:05.5", "00:04:05.5"}, {"4:5:6", "04:05:06"},
				{"04:05 AM", "04:05:00"}, {"04:05 pm", "16:05:00"}, {"12:00 AM", "00:00:00"}, {"12:00 PM", "12:00:00"}, {"00:00 AM", "00:00:00"}, {"00:00 PM", "12:00:00"},
				{"04:05:06.789-8", "04:05:06.789"}, {"04:05:06-08:00", "04:05:06"}, {"04:05:06+0730", "04:05:06"}, {"04:05:06+07:30:00", "04:05:06"}, {"02:03 PST", "02:03:00"}, {"11:59 EDT", "11:59:00"},
				{"2003-03-07 15:36:39 America/New_York", "15:36:39"}, {"2003-07-07 15:36:39 America/New_York", "15:36:39"}, {"2003-07-07 15:36:39", "15:36:39"}, {"2003-07-07 15:36:39 Asia/Kolkata", "15:36:39"},
				{"04:05 Etc/GMT+5", "04:05:00"}, {"04:05 eTc/UtC", "04:05:00"}, {"04:05 Universal", "04:05:00"}, {"04:05 X0", "04:05:00"}, {"04:05 X0Y0", "04:05:00"}, {"2003-07-07 04:05 X0Y", "04:05:00"},
				{"04:05 MET DST", "04:05:00"}, {"04:05 MSK", "04:05:00"}, {"t040506", "04:05:06"}, {"t04:05:06", "04:05:06"}, {"j2451545.5", "12:00:00"}, {"j2451545 04:05", "04:05:00"}, {"04:05 on", "04:05:00"}, {"04:05 AD", "04:05:00"}, {"allballs", "00:00:00"},
				{"23:59:60", "24:00:00"}, {"23:59:59.9999999", "24:00:00"}, {"04:05:60", "04:06:00"}, {"04:05:06.0000005", "04:05:06"}, {"04:05:06.0000015", "04:05:06.000002"},
			} {
				t.Run(tc.input, func(t *testing.T) {
					want := fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{tc.want}})
					s := fixture.Select("SELECT TIME '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, want)
					run(s)
					run(fixture.Prepared(s))
					s = fixture.Select("SELECT TIME WITHOUT TIME ZONE '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, want)
					run(s)
					run(fixture.Prepared(s))
					run(fixture.Exec(insertQuery, 1, i*2, tc.input))
					fixture.TemporalRunStep(t, p.Ctx, heldInsert, backend.Recorder, fixture.Exec(insertQuery, 1, i*2+1, tc.input))
					run(fixture.Select("SELECT v FROM inputs WHERE id=$1", []string{"v"}, []string{"TIME"}, want, i*2+1))
					run(fixture.Select("SELECT v FROM inputs WHERE id=$1", []string{"v"}, []string{"TIME"}, want, i*2))
				})
			}
			for _, tc := range []struct{ decl, input, want string }{
				{"TIME(0)", "04:05:06.499999", "04:05:06"}, {"TIME(0)", "04:05:06.5", "04:05:07"}, {"TIME(3)", "04:05:06.1235", "04:05:06.124"}, {"TIME(3)", "04:05:06.1234999", "04:05:06.124"}, {"TIME(6)", "04:05:06.0000005", "04:05:06"}, {"TIME(6)", "04:05:06.0000015", "04:05:06.000002"}, {"TIME(0)", "23:59:59.5", "24:00:00"},
			} {
				s := fixture.Select("SELECT "+tc.decl+" '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{tc.want}}))
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE TABLE rounding(v TIME(0))", 0))
			run(fixture.Exec("INSERT INTO rounding VALUES(TIME(3) '04:05:06.4995'),('04:05:06.4995'),('23:59:59.5')", 3))
			run(fixture.Select("SELECT v FROM rounding ORDER BY v", []string{"v"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{"04:05:06"}, {"04:05:07"}, {"24:00:00"}})))
			run(fixture.Exec("CREATE TABLE ordering(v TIME)", 0))
			run(fixture.Exec("INSERT INTO ordering VALUES('00:00'),('04:05:06.1'),('04:05:06.2'),('24:00'),(NULL)", 5))
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{{"SELECT v FROM ordering ORDER BY v", [][]any{{"00:00:00"}, {"04:05:06.1"}, {"04:05:06.2"}, {"24:00:00"}, {nil}}}, {"SELECT v FROM ordering ORDER BY v DESC", [][]any{{nil}, {"24:00:00"}, {"04:05:06.2"}, {"04:05:06.1"}, {"00:00:00"}}}, {"SELECT v FROM ordering WHERE v='24:00'", [][]any{{"24:00:00"}}}, {"SELECT v FROM ordering WHERE v=NULL", nil}} {
				s := fixture.Select(tc.q, []string{"v"}, []string{"TIME"}, fixture.TimeRows(t, backend.PG, []string{"TIME"}, tc.rows))
				run(s)
				run(fixture.Prepared(s))
			}
		})
	}
}

func TestReferenceTimeHeldParametersAndOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			query := func(q string, names, types []string, rows [][]any, args ...any) {
				s := fixture.Select(q, names, types, fixture.TimeRows(t, backend.PG, types, rows), args...)
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE TABLE params(id INTEGER,v TIME(0),fine TIME(6))", 0))
			ins, err := backend.DB.PrepareContext(p.Ctx, "INSERT INTO params VALUES($1,$2,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer ins.Close()
			for _, tc := range []struct {
				id    int
				value any
			}{{1, "00:00"}, {2, "24:00"}, {3, nil}, {4, []byte("04:05:06.5")}, {5, time.Date(2003, 7, 7, 15, 36, 39, 123456000, time.FixedZone("offset", 7*3600))}} {
				x := fixture.HeldStatement{Executor: backend.DB, Statement: ins, SQL: "INSERT INTO params VALUES($1,$2,$2)"}
				fixture.TemporalRunStep(t, p.Ctx, x, backend.Recorder, fixture.Exec("INSERT INTO params VALUES($1,$2,$2)", 1, tc.id, tc.value))
			}
			query("SELECT id,v,fine FROM params ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), nil, nil}, {int64(4), "04:05:07", "04:05:06.5"}, {int64(5), "15:36:39", "15:36:39.123456"}})
			held, err := backend.DB.PrepareContext(p.Ctx, "SELECT id FROM params WHERE v=$1 ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, tc := range []struct {
				arg any
				id  int64
			}{{"00:00", 1}, {"24:00", 2}, {nil, 0}, {"00:00", 1}} {
				var expected [][]any
				if tc.id != 0 {
					expected = [][]any{{tc.id}}
				}
				x := fixture.HeldStatement{Executor: backend.DB, Statement: held, SQL: "SELECT id FROM params WHERE v=$1 ORDER BY id"}
				fixture.TemporalRunStep(t, p.Ctx, x, backend.Recorder, fixture.Select("SELECT id FROM params WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, expected, tc.arg))
			}
			run(fixture.Exec("CREATE TABLE ordering(v TIME)", 0))
			run(fixture.Exec("INSERT INTO ordering VALUES('00:00'),('04:05:06.1'),('04:05:06.2'),('24:00'),(NULL)", 5))
			bytes := []byte("04:05:06.2")
			q := "SELECT v FROM ordering WHERE v=$1"
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			rows, err := backend.DB.QueryContext(ctx, q, bytes)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			copy(bytes, "00:00:00.0")
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TemporalMetadata(t, backend.PG, meta)
			if !rows.Next() {
				t.Fatal("missing retained row", rows.Err())
			}
			var got any
			if err := rows.Scan(&got); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, backend.PG, "TIME", got)
			want := fixture.TimeRows(t, backend.PG, []string{"TIME"}, [][]any{{"04:05:06.2"}})[0][0]
			if !reflect.DeepEqual(fixture.Normalize(got), want) {
				t.Fatal(got, want)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra retained row", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.TemporalMetadata(t, backend.PG, meta)
			fixture.TemporalRecorded(t, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"TIME"}, [][]any{{want}}))
		})
	}
}
