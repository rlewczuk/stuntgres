package reference_test

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestReferenceTimeAcceptedInputsPrecisionAndOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE anchor(id INTEGER)", 0))
			run(referenceExec("INSERT INTO anchor VALUES(1)", 1))
			run(referenceExec("CREATE TABLE inputs(id INTEGER,v TIME)", 0))
			insertQuery := "INSERT INTO inputs VALUES($1,$2)"
			insert, err := backend.db.PrepareContext(p.ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := insert.Close(); err != nil {
					t.Error(err)
				}
			}()
			heldInsert := referenceHeldStatement{referenceExecutor: backend.db, statement: insert, query: insertQuery}
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
					want := referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{tc.want}})
					s := referenceSelect("SELECT TIME '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, want)
					run(s)
					run(referencePrepared(s))
					s = referenceSelect("SELECT TIME WITHOUT TIME ZONE '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, want)
					run(s)
					run(referencePrepared(s))
					run(referenceExec(insertQuery, 1, i*2, tc.input))
					referenceTemporalRunStep(t, p.ctx, heldInsert, backend.recorder, referenceExec(insertQuery, 1, i*2+1, tc.input))
					run(referenceSelect("SELECT v FROM inputs WHERE id=$1", []string{"v"}, []string{"TIME"}, want, i*2+1))
					run(referenceSelect("SELECT v FROM inputs WHERE id=$1", []string{"v"}, []string{"TIME"}, want, i*2))
				})
			}
			for _, tc := range []struct{ decl, input, want string }{
				{"TIME(0)", "04:05:06.499999", "04:05:06"}, {"TIME(0)", "04:05:06.5", "04:05:07"}, {"TIME(3)", "04:05:06.1235", "04:05:06.124"}, {"TIME(3)", "04:05:06.1234999", "04:05:06.124"}, {"TIME(6)", "04:05:06.0000005", "04:05:06"}, {"TIME(6)", "04:05:06.0000015", "04:05:06.000002"}, {"TIME(0)", "23:59:59.5", "24:00:00"},
			} {
				s := referenceSelect("SELECT "+tc.decl+" '"+tc.input+"' FROM anchor", []string{"time"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{tc.want}}))
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE rounding(v TIME(0))", 0))
			run(referenceExec("INSERT INTO rounding VALUES(TIME(3) '04:05:06.4995'),('04:05:06.4995'),('23:59:59.5')", 3))
			run(referenceSelect("SELECT v FROM rounding ORDER BY v", []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"04:05:06"}, {"04:05:07"}, {"24:00:00"}})))
			run(referenceExec("CREATE TABLE ordering(v TIME)", 0))
			run(referenceExec("INSERT INTO ordering VALUES('00:00'),('04:05:06.1'),('04:05:06.2'),('24:00'),(NULL)", 5))
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{{"SELECT v FROM ordering ORDER BY v", [][]any{{"00:00:00"}, {"04:05:06.1"}, {"04:05:06.2"}, {"24:00:00"}, {nil}}}, {"SELECT v FROM ordering ORDER BY v DESC", [][]any{{nil}, {"24:00:00"}, {"04:05:06.2"}, {"04:05:06.1"}, {"00:00:00"}}}, {"SELECT v FROM ordering WHERE v='24:00'", [][]any{{"24:00:00"}}}, {"SELECT v FROM ordering WHERE v=NULL", nil}} {
				s := referenceSelect(tc.q, []string{"v"}, []string{"TIME"}, referenceTimeRows(t, backend.pg, []string{"TIME"}, tc.rows))
				run(s)
				run(referencePrepared(s))
			}
		})
	}
}

func TestReferenceTimeHeldParametersAndOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			query := func(q string, names, types []string, rows [][]any, args ...any) {
				s := referenceSelect(q, names, types, referenceTimeRows(t, backend.pg, types, rows), args...)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE params(id INTEGER,v TIME(0),fine TIME(6))", 0))
			ins, err := backend.db.PrepareContext(p.ctx, "INSERT INTO params VALUES($1,$2,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer ins.Close()
			for _, tc := range []struct {
				id    int
				value any
			}{{1, "00:00"}, {2, "24:00"}, {3, nil}, {4, []byte("04:05:06.5")}, {5, time.Date(2003, 7, 7, 15, 36, 39, 123456000, time.FixedZone("offset", 7*3600))}} {
				x := referenceHeldStatement{referenceExecutor: backend.db, statement: ins, query: "INSERT INTO params VALUES($1,$2,$2)"}
				referenceTemporalRunStep(t, p.ctx, x, backend.recorder, referenceExec("INSERT INTO params VALUES($1,$2,$2)", 1, tc.id, tc.value))
			}
			query("SELECT id,v,fine FROM params ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), nil, nil}, {int64(4), "04:05:07", "04:05:06.5"}, {int64(5), "15:36:39", "15:36:39.123456"}})
			held, err := backend.db.PrepareContext(p.ctx, "SELECT id FROM params WHERE v=$1 ORDER BY id")
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
				x := referenceHeldStatement{referenceExecutor: backend.db, statement: held, query: "SELECT id FROM params WHERE v=$1 ORDER BY id"}
				referenceTemporalRunStep(t, p.ctx, x, backend.recorder, referenceSelect("SELECT id FROM params WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, expected, tc.arg))
			}
			run(referenceExec("CREATE TABLE ordering(v TIME)", 0))
			run(referenceExec("INSERT INTO ordering VALUES('00:00'),('04:05:06.1'),('04:05:06.2'),('24:00'),(NULL)", 5))
			bytes := []byte("04:05:06.2")
			q := "SELECT v FROM ordering WHERE v=$1"
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			rows, err := backend.db.QueryContext(ctx, q, bytes)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			copy(bytes, "00:00:00.0")
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			referenceTemporalMetadata(t, backend.pg, meta)
			if !rows.Next() {
				t.Fatal("missing retained row", rows.Err())
			}
			var got any
			if err := rows.Scan(&got); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, backend.pg, "TIME", got)
			want := referenceTimeRows(t, backend.pg, []string{"TIME"}, [][]any{{"04:05:06.2"}})[0][0]
			if !reflect.DeepEqual(referenceNormalize(got), want) {
				t.Fatal(got, want)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra retained row", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			referenceTemporalMetadata(t, backend.pg, meta)
			referenceTemporalRecorded(t, backend.recorder, referenceSelect(q, []string{"v"}, []string{"TIME"}, [][]any{{want}}))
		})
	}
}
