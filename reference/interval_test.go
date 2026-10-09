package reference_test

import (
	"context"
	"reflect"
	"testing"
)

func TestReferenceIntervalValuesDeclarationsAndComparison(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			query := func(q string, names, types []string, rows [][]any) {
				s := referenceSelect(q, names, types, rows)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE iv(id INTEGER,v INTERVAL)", 0))
			inputs := []struct{ s, out string }{{"2 years 15 months 100 weeks 99 hours 123456789 milliseconds", "3 years 3 mons 700 days 133:17:36.789"}, {"1.75 months", "1 mon 22 days 12:00:00"}, {"1.5 weeks", "10 days 12:00:00"}, {"1-2", "1 year 2 mons"}, {"3 4:05:06", "3 days 04:05:06"}, {"P2Y10M15DT10H30M20S", "2 years 10 mons 15 days 10:30:20"}, {"P00021015T103020", "2 years 10 mons 15 days 10:30:20"}, {"P0002-10-15T10:30:20", "2 years 10 mons 15 days 10:30:20"}, {"P1Y2Y", "3 years"}, {"@ 1 year 2 mons -3 days 4 hours 5 mins 6 secs ago", "-1 years -2 mons +3 days -04:05:06"}, {"2147483647 months", "178956970 years 7 mons"}, {"-9223372036854775808 microseconds", "-2562047788:00:54.775808"}, {"infinity", "infinity"}, {"-infinity", "-infinity"}}
			var want [][]any
			for i, tc := range inputs {
				s := referenceExec("INSERT INTO iv VALUES($1,$2)", 1, i+1, tc.s)
				if i%2 == 0 {
					s = referencePrepared(s)
				}
				run(s)
				want = append(want, []any{int64(i + 1), tc.out})
			}
			run(referencePrepared(referenceExec("INSERT INTO iv VALUES($1,$2)", 1, 15, nil)))
			want = append(want, []any{int64(15), nil})
			query("SELECT id,v FROM iv ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, want)
			rows, e := backend.db.QueryContext(p.ctx, "SELECT v FROM iv")
			if e != nil {
				t.Fatal(e)
			}
			meta, e := rows.ColumnTypes()
			if closeErr := rows.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if e != nil {
				t.Fatal(e)
			}
			scan := reflect.TypeFor[string]()
			if backend.pg {
				scan = reflect.TypeFor[any]()
			}
			if meta[0].DatabaseTypeName() != "INTERVAL" || meta[0].ScanType() != scan {
				t.Fatal(meta[0], scan)
			}
			if _, ok := meta[0].Length(); ok {
				t.Fatal("interval length metadata")
			}
			if _, _, ok := meta[0].DecimalSize(); ok {
				t.Fatal("interval numeric metadata")
			}
			run(referenceExec("CREATE TABLE ivtm(id INTEGER,y INTERVAL YEAR,m INTERVAL MINUTE TO SECOND(2))", 0))
			run(referenceExec("INSERT INTO ivtm VALUES(1,'1','1:02.345')", 1))
			statement, err := backend.db.PrepareContext(p.ctx, "INSERT INTO ivtm VALUES($1,$2,$3)")
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := referenceHeldStatement{backend.db, statement, "INSERT INTO ivtm VALUES($1,$2,$3)"}
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(held.query, 1, 2, "1", []byte("1:02.345")))
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(held.query, 1, 3, nil, nil))
			query("SELECT id,y,m FROM ivtm ORDER BY id", []string{"id", "y", "m"}, []string{"INT4", "INTERVAL", "INTERVAL"}, [][]any{{int64(1), "1 year", "00:01:02.35"}, {int64(2), "00:00:00", "00:01:02.35"}, {int64(3), nil, nil}})
			run(referenceExec("CREATE TABLE eq(id INTEGER,v INTERVAL)", 0))
			run(referenceExec("INSERT INTO eq VALUES(1,'1 mon'),(2,'30 days'),(3,'720 hours'),(4,'1 day'),(5,'24 hours'),(6,NULL)", 6))
			query("SELECT id FROM eq WHERE v=INTERVAL '1 mon' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
			s := referenceSelect("SELECT COUNT(*) FROM eq GROUP BY v", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}})
			s.bag = true
			run(s)
			run(referencePrepared(s))
			query("SELECT id FROM eq WHERE v IS NOT DISTINCT FROM INTERVAL '1 day' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}})
		})
	}
}

func TestReferenceIntervalStylesPreparedCaptureAndSessionIsolation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			b, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
			run(referenceExec("CREATE TABLE ivstyle(id INTEGER,v INTERVAL)", 0))
			literal, e := a.PrepareContext(p.ctx, "INSERT INTO ivstyle VALUES(1,INTERVAL '-1 2:03:04')")
			if e != nil {
				t.Fatal(e)
			}
			defer literal.Close()
			unknown, e := a.PrepareContext(p.ctx, "INSERT INTO ivstyle VALUES(2,'-1 2:03:04')")
			if e != nil {
				t.Fatal(e)
			}
			defer unknown.Close()
			param, e := a.PrepareContext(p.ctx, "INSERT INTO ivstyle VALUES(3,$1)")
			if e != nil {
				t.Fatal(e)
			}
			defer param.Close()
			run(referenceExec("SET IntervalStyle=sql_standard", 0))
			referenceRunStep(t, p.ctx, referenceHeldStatement{a, literal, "INSERT INTO ivstyle VALUES(1,INTERVAL '-1 2:03:04')"}, backend.recorder, referenceExec("INSERT INTO ivstyle VALUES(1,INTERVAL '-1 2:03:04')", 1))
			referenceRunStep(t, p.ctx, referenceHeldStatement{a, unknown, "INSERT INTO ivstyle VALUES(2,'-1 2:03:04')"}, backend.recorder, referenceExec("INSERT INTO ivstyle VALUES(2,'-1 2:03:04')", 1))
			referenceRunStep(t, p.ctx, referenceHeldStatement{a, param, "INSERT INTO ivstyle VALUES(3,$1)"}, backend.recorder, referenceExec("INSERT INTO ivstyle VALUES(3,$1)", 1, "-1 2:03:04"))
			run(referenceSelect("SELECT id,v FROM ivstyle ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "+0-0 -1 +2:03:04"}, {int64(2), "+0-0 -1 +2:03:04"}, {int64(3), "-1 2:03:04"}}))
			referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT id,v FROM ivstyle ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "-1 days +02:03:04"}, {int64(2), "-1 days +02:03:04"}, {int64(3), "-1 days -02:03:04"}}))
			referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"postgres"}}))
			outputs := []struct{ style, out, zero string }{{"postgres", "-1 years -2 mons +3 days -04:05:06", "00:00:00"}, {"postgres_verbose", "@ 1 year 2 mons -3 days 4 hours 5 mins 6 secs ago", "@ 0"}, {"sql_standard", "-1-2 +3 -4:05:06", "0"}, {"iso_8601", "P-1Y-2M3DT-4H-5M-6S", "PT0S"}}
			prepared, e := a.PrepareContext(p.ctx, "SELECT v FROM ivstyle WHERE id=4")
			if e != nil {
				t.Fatal(e)
			}
			defer prepared.Close()
			run(referenceExec("INSERT INTO ivstyle VALUES(4,'-14 months +3 days -4:05:06'),(5,'0')", 2))
			for _, tc := range outputs {
				run(referenceExec("SET IntervalStyle="+tc.style, 0))
				run(referenceSelect("SELECT v FROM ivstyle WHERE id=4", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
				referenceRunStep(t, p.ctx, referenceHeldStatement{a, prepared, "SELECT v FROM ivstyle WHERE id=4"}, backend.recorder, referenceSelect("SELECT v FROM ivstyle WHERE id=4", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
				run(referenceSelect("SELECT v FROM ivstyle WHERE id=5", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.zero}}))
			}
			run(referenceExec("RESET IntervalStyle", 0))
			run(referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"postgres"}}))
		})
	}
}

func TestReferenceIntervalGroupComparisonClasses(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		referenceRunStep(t, p.ctx, db, backend.recorder, referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
		referenceRunStep(t, p.ctx, db, backend.recorder, referenceExec("INSERT INTO iv VALUES('1 mon'),('30 days'),('720 hours'),('1 day'),('24 hours'),(NULL)", 6))
		rows, err := db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), "SELECT v FROM iv GROUP BY v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"v"}) {
			t.Fatal(names, err)
		}
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "INTERVAL" {
			t.Fatal(meta, err)
		}
		var consumed [][]any
		classes := make(map[int]bool)
		for rows.Next() {
			var value any
			if err = rows.Scan(&value); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			referenceAssertRawValue(t, backend.pg, "INTERVAL", value)
			if text, ok := value.([]byte); ok {
				value = string(text)
			}
			rank := -1
			switch value {
			case "1 mon", "30 days", "720:00:00":
				rank = 0
			case "1 day", "24:00:00":
				rank = 1
			case nil:
				rank = 2
			}
			if rank < 0 || classes[rank] {
				rows.Close()
				t.Fatal(value, classes)
			}
			classes[rank] = true
			consumed = append(consumed, []any{value})
		}
		err = rows.Err()
		if closeErr := rows.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil || len(classes) != 3 {
			t.Fatal(classes, err)
		}
		backend.recorder.mu.Lock()
		var op *referenceOperation
		for _, candidate := range backend.recorder.operations {
			if candidate.SQL == "SELECT v FROM iv GROUP BY v" && candidate.Columns != nil {
				op = candidate
			}
		}
		backend.recorder.mu.Unlock()
		if op == nil || !op.Closed || op.CloseErr != nil {
			t.Fatal("group cursor not recorded/closed", op)
		}
		captured := make([][]any, len(op.Rows))
		for i, row := range op.Rows {
			captured[i] = []any{referenceNormalize(row[0])}
		}
		if !reflect.DeepEqual(captured, consumed) || !reflect.DeepEqual(op.Columns, names) {
			t.Fatal(captured, consumed, op.Columns)
		}
	}
}
