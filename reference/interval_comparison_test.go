package reference_test

import (
	"testing"
)

func TestReferenceIntervalComparisonGroupingAndConditionals(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE iv(id INTEGER,v INTERVAL)", 0))
	p.step(t, referenceExec("INSERT INTO iv VALUES(1,'-infinity'),(2,'-1 day'),(3,'0'),(4,'1 day'),(5,'24 hours'),(6,'1 mon'),(7,'30 days'),(8,'infinity'),(9,NULL)", 9))
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
		s := referenceSelect("SELECT id FROM iv WHERE v"+tc.op+"INTERVAL '1 day' ORDER BY id", []string{"id"}, []string{"INT4"}, want)
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	// First independently assert the complete duplicate bag, then the ordering
	// of comparison classes in the ORIGINAL consumed middleware sequence.
	s := referenceSelect("SELECT v FROM iv ORDER BY v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"-infinity"}, {"-1 days"}, {"00:00:00"}, {"1 day"}, {"24:00:00"}, {"1 mon"}, {"30 days"}, {"infinity"}, {nil}})
	s.bag = true
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			s.prepared = prepared
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			backend.recorder.mu.Lock()
			var recorded *referenceOperation
			for _, op := range backend.recorder.operations {
				if op.SQL == s.query && op.Columns != nil {
					recorded = op
				}
			}
			backend.recorder.mu.Unlock()
			previous := -1
			for _, row := range recorded.Rows {
				value := referenceNormalize(row[0])
				rank, known := map[any]int{"-infinity": 0, "-1 days": 1, "00:00:00": 2, "1 day": 3, "24:00:00": 3, "1 mon": 4, "30 days": 4, "infinity": 5, nil: 6}[value]
				if !known || rank < previous {
					t.Fatal(value, rank, previous)
				}
				previous = rank
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		s := referenceSelect("SELECT COALESCE(NULL,v),GREATEST(v,INTERVAL '1 day'),LEAST(v,INTERVAL '1 day') FROM iv WHERE id=6", []string{"coalesce", "greatest", "least"}, []string{"INTERVAL", "INTERVAL", "INTERVAL"}, [][]any{{"1 mon", "1 mon", "1 day"}})
		s.prepared = prepared
		p.step(t, s)
		s = referenceSelect("SELECT COUNT(*) FROM iv GROUP BY v", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}})
		s.prepared, s.bag = prepared, true
		p.step(t, s)
	}
	p.step(t, referenceExec("CREATE TABLE large(a INTERVAL,b INTERVAL)", 0))
	p.step(t, referenceExec("INSERT INTO large VALUES('2147483647 months -2147483648 days','2147483646 months -2147483618 days')", 1))
	s = referenceSelect("SELECT a=b,a>b,a<b FROM large", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
}

func TestReferenceIntervalComponentsAndComparison(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE iv(id INTEGER,v INTERVAL)", 0))
	p.step(t, referenceExec("INSERT INTO iv VALUES(1,'1 mon'),(2,'30 days'),(3,'720 hours'),(4,'1 day'),(5,'24 hours'),(6,NULL),(7,'P2Y10M15DT10H30M20S')", 7))
	s := referenceSelect("SELECT id,v FROM iv ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "1 mon"}, {int64(2), "30 days"}, {int64(3), "720:00:00"}, {int64(4), "1 day"}, {int64(5), "24:00:00"}, {int64(6), nil}, {int64(7), "2 years 10 mons 15 days 10:30:20"}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
	for _, backend := range p.backends() {
		intervalReferenceMetadata(t, p, backend, "SELECT id,v FROM iv ORDER BY id", 1)
	}
	p.step(t, referenceSelect("SELECT id FROM iv WHERE v=INTERVAL '1 month' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	s = referenceSelect("SELECT COUNT(*) FROM iv GROUP BY v", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}})
	s.bag = true
	p.step(t, s)
}
