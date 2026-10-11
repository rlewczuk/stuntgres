package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalComparisonGroupingAndConditionals(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE iv(id INTEGER,v INTERVAL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO iv VALUES(1,'-infinity'),(2,'-1 day'),(3,'0'),(4,'1 day'),(5,'24 hours'),(6,'1 mon'),(7,'30 days'),(8,'infinity'),(9,NULL)", 9))
	for _, tc := range []struct {
		op  string
		ids []int64
	}{
		{"=", []int64{4, 5}}, {"<>", []int64{1, 2, 3, 6, 7, 8}}, {"<", []int64{1, 2, 3}}, {"<=", []int64{1, 2, 3, 4, 5}}, {">", []int64{6, 7, 8}}, {">=", []int64{4, 5, 6, 7, 8}},
	} {
		want := make([][]any, len(tc.ids))
		for i, id := range tc.ids {
			want[i] = []any{id}
		}
		s := fixture.Select("SELECT id FROM iv WHERE v"+tc.op+"INTERVAL '1 day' ORDER BY id", []string{"id"}, []string{"INT4"}, want)
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	// First independently assert the complete duplicate bag, then the ordering
	// of comparison classes in the ORIGINAL consumed middleware sequence.
	s := fixture.Select("SELECT v FROM iv ORDER BY v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"-infinity"}, {"-1 days"}, {"00:00:00"}, {"1 day"}, {"24:00:00"}, {"1 mon"}, {"30 days"}, {"infinity"}, {nil}})
	s.Bag = true
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			backend.Recorder.Mu.Lock()
			var recorded *fixture.Operation
			for _, op := range backend.Recorder.Operations {
				if op.SQL == s.Query && op.Columns != nil {
					recorded = op
				}
			}
			backend.Recorder.Mu.Unlock()
			previous := -1
			for _, row := range recorded.Rows {
				value := fixture.Normalize(row[0])
				rank, known := map[any]int{"-infinity": 0, "-1 days": 1, "00:00:00": 2, "1 day": 3, "24:00:00": 3, "1 mon": 4, "30 days": 4, "infinity": 5, nil: 6}[value]
				if !known || rank < previous {
					t.Fatal(value, rank, previous)
				}
				previous = rank
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT COALESCE(NULL,v),GREATEST(v,INTERVAL '1 day'),LEAST(v,INTERVAL '1 day') FROM iv WHERE id=6", []string{"coalesce", "greatest", "least"}, []string{"INTERVAL", "INTERVAL", "INTERVAL"}, [][]any{{"1 mon", "1 mon", "1 day"}})
		s.Prepared = prepared
		p.Step(t, s)
		s = fixture.Select("SELECT COUNT(*) FROM iv GROUP BY v", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}})
		s.Prepared, s.Bag = prepared, true
		p.Step(t, s)
	}
	p.Step(t, fixture.Exec("CREATE TABLE large(a INTERVAL,b INTERVAL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO large VALUES('2147483647 months -2147483648 days','2147483646 months -2147483618 days')", 1))
	s = fixture.Select("SELECT a=b,a>b,a<b FROM large", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}})
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}

func TestReferenceIntervalComponentsAndComparison(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE iv(id INTEGER,v INTERVAL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO iv VALUES(1,'1 mon'),(2,'30 days'),(3,'720 hours'),(4,'1 day'),(5,'24 hours'),(6,NULL),(7,'P2Y10M15DT10H30M20S')", 7))
	s := fixture.Select("SELECT id,v FROM iv ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "1 mon"}, {int64(2), "30 days"}, {int64(3), "720:00:00"}, {int64(4), "1 day"}, {int64(5), "24:00:00"}, {int64(6), nil}, {int64(7), "2 years 10 mons 15 days 10:30:20"}})
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
	for _, backend := range p.Backends() {
		fixture.IntervalReferenceMetadata(t, p, backend, "SELECT id,v FROM iv ORDER BY id", 1)
	}
	p.Step(t, fixture.Select("SELECT id FROM iv WHERE v=INTERVAL '1 month' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	s = fixture.Select("SELECT COUNT(*) FROM iv GROUP BY v", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}})
	s.Bag = true
	p.Step(t, s)
}
