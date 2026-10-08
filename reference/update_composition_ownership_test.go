package reference_test

import (
	"reflect"
	"testing"
	"time"
)

func TestReferenceUpdateCoreCompositionOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,d DATE,b BYTEA)", 0))
			run(referenceExec(`INSERT INTO t VALUES(1,'2000-01-01','\x01'),(2,NULL,'\x')`, 2))
			old, err := backend.db.QueryContext(p.ctx, "SELECT id,d,b FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(metadata) != 3 || metadata[1].DatabaseTypeName() != "DATE" || metadata[2].DatabaseTypeName() != "BYTEA" || metadata[2].ScanType() != reflect.TypeOf([]byte{}) {
				t.Fatal(metadata)
			}
			if !old.Next() {
				t.Fatal("missing original row", old.Err())
			}
			var id any
			var date any
			var retainedValue any
			if err := old.Scan(&id, &date, &retainedValue); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, backend.pg, "INT4", id)
			referenceAssertRawValue(t, backend.pg, "BYTEA", retainedValue)
			retained, retainedBytes := retainedValue.([]byte)
			var original, leap, second any = "2000-01-01", "2024-02-29", "2030-01-02"
			if backend.pg {
				original = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				leap = time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
				second = time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
			}
			dateMatches := reflect.DeepEqual(date, original)
			if backend.pg {
				got, ok := date.(time.Time)
				dateMatches = ok && got.Equal(original.(time.Time))
			}
			if id != int64(1) || !dateMatches || !retainedBytes || !reflect.DeepEqual(retained, []byte{1}) {
				t.Fatal(id, date, retained)
			}
			retained[0] = 77
			run(referenceSelect("SELECT d,b FROM t ORDER BY id", []string{"d", "b"}, []string{"DATE", "BYTEA"}, [][]any{{original, "\x01"}, {nil, ""}}))
			payload := []byte{0, 92, 255}
			run(referenceExec("UPDATE t SET d=$1,b=$2 WHERE id=1", 1, "2024-02-29", payload))
			payload[0] = 99
			held, err := backend.db.PrepareContext(p.ctx, "UPDATE t SET d=$1,b=$2 WHERE id=2")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { old.Close(); held.Close() }()
			result, err := held.ExecContext(p.ctx, "2030-01-02", []byte{})
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			current := referenceSelect("SELECT id,d,b FROM t ORDER BY id", []string{"id", "d", "b"}, []string{"INT4", "DATE", "BYTEA"}, [][]any{{int64(1), leap, "\x00\\\xff"}, {int64(2), second, ""}})
			run(current)
			updated, err := backend.db.QueryContext(p.ctx, "SELECT b FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer updated.Close()
			if !updated.Next() {
				t.Fatal(updated.Err())
			}
			var detachedValue any
			if err := updated.Scan(&detachedValue); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, backend.pg, "BYTEA", detachedValue)
			detached, detachedBytes := detachedValue.([]byte)
			if !detachedBytes || !reflect.DeepEqual(detached, []byte{0, 92, 255}) {
				t.Fatal(detached)
			}
			detached[0] = 88
			run(current)
			if !old.Next() {
				t.Fatal(old.Err())
			}
			var emptyValue any
			if err := old.Scan(&id, &date, &emptyValue); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, backend.pg, "INT4", id)
			referenceAssertRawValue(t, backend.pg, "BYTEA", emptyValue)
			empty, emptyBytes := emptyValue.([]byte)
			if id != int64(2) || date != nil || !emptyBytes || empty == nil || len(empty) != 0 {
				t.Fatal("NULL versus owned empty", id, date, empty)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal(old.Err())
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			if !updated.Next() {
				t.Fatal(updated.Err())
			}
			if err := updated.Scan(&emptyValue); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, backend.pg, "BYTEA", emptyValue)
			empty, emptyBytes = emptyValue.([]byte)
			if !emptyBytes || empty == nil || len(empty) != 0 || retained[0] != 77 || detached[0] != 88 {
				t.Fatal(empty, retained, detached)
			}
			if updated.Next() || updated.Err() != nil {
				t.Fatal(updated.Err())
			}
			if err := updated.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
