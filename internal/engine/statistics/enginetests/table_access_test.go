package statistics_test

import (
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"
)

func TestPGStatAllTablesProvider(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2, t3 := t1.Add(time.Second), t1.Add(3*time.Second)
	var p statistics.Provider
	var w statistics.Writer
	key, other := statsKeys()
	off := w.CaptureRelationAccess(&p, key, statistics.Policy{})
	off.Record(1, 3, 7)
	var zero statistics.RelationAccess
	zero.Record(1, 3, 7)
	if w.Pending() { t.Fatal("disabled/zero capability allocated work") }
	on := statistics.Policy{Counts: true}
	a := w.CaptureRelationAccess(&p, key, on)
	a.Record(1, 3, 0)
	w.TransactionStopped(t1)
	a.Record(1, 2, 7)
	w.TransactionStopped(t2)
	w.Tuple(&p, key, on, 2, 1, 1)
	w.IndexAccess(&p, other, on, 2, 4, 3)
	w.Flush(&p, t3, false)
	f := p.Read(key)
	if f.SeqScans != 2 || f.SeqTuples != 5 || f.TableIndexFetches != 7 || !f.LastSeqScan.Equal(t2) || f.Inserts != 2 || f.Updates != 1 || f.Deletes != 1 { t.Fatal("additive stop-owned publication", f) }
	p.ReportAnalyze(key,on,t2,1500*time.Microsecond,2,1,true)
	p.ReportVacuum(key,on,t2,2500*time.Microsecond,2,0)
	a = w.CaptureRelationAccess(&p, key, on)
	retained := f
	a.Record(0, 1, 2)
	w.TransactionStopped(t3)
	w.Flush(&p, t3.Add(time.Millisecond), true)
	f = p.Read(key)
	a = w.CaptureRelationAccess(&p, key, on)
	if f.SeqScans != 2 || f.SeqTuples != 6 || f.TableIndexFetches != 9 || !f.LastSeqScan.Equal(t2) { t.Fatal("tuple/bitmap batch invented scan time", f) }
	if f.AnalyzeCount!=1||f.VacuumCount!=1||f.AnalyzeTime!=1500*time.Microsecond||f.VacuumTime!=2500*time.Microsecond||!f.LastAnalyze.Equal(t2)||!f.LastVacuum.Equal(t2) {t.Fatal("read merge changed completed maintenance",f)}
	a.Record(1, 0, 0)
	if w.Flush(&p, t3.Add(2*time.Millisecond), false) { t.Fatal("ordinary throttle bypass") }
	w.TransactionStopped(t3.Add(time.Second))
	w.Flush(&p, t3.Add(2*time.Second), false)
	if f = p.Read(key); f.SeqScans != 3 || !f.LastSeqScan.Equal(t3.Add(time.Second)) { t.Fatal("latest completed stop not publication clock", f) }
	if retained.SeqScans != 2 || retained.SeqTuples != 5 || !retained.LastSeqScan.Equal(t2) { t.Fatal("retained fact mutated", retained) }
	if f := p.Read(other); f.IndexScans != 2 || f.IndexEntries != 4 || f.IndexFetches != 3 || !f.LastIndexScan.Equal(t2) { t.Fatal("index fields changed", f) }
	for _, operation := range []string{"reset", "replace", "retire"} {
		a = w.CaptureRelationAccess(&p, key, on)
		a.Record(1, 10, 2)
		switch operation {
		case "reset": p.Reset(t3)
		case "replace": p.Replace(t3, nil)
		case "retire": p.Retire(key)
		}
		// Existing stale capture is also rejected when the writer next captures a generation.
		_ = w.CaptureRelationAccess(&p, other, on)
		a.Record(1, 20, 3)
		w.Flush(&p, t3, true)
		if f := p.Read(key); f.SeqScans != 0 || f.SeqTuples != 0 || f.TableIndexFetches != 0 || !f.LastSeqScan.IsZero() { t.Fatal(operation, "resurrected stale work", f) }
	}
	var standalone statistics.Writer
	b := standalone.CaptureRelationAccess(&p, other, on)
	b.Record(1, 0, 0)
	standalone.Flush(&p, t3, true)
	if f := p.Read(other); !f.LastSeqScan.Equal(t3) { t.Fatal("lazy missing-stop fallback", f) }
}
