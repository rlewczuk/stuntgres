package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// The typed Scan and complete metadata snapshot are retained in addition to
// independently bounded clock capture. Recording observes this same execution.
func sequencePreparedTemporal(t *testing.T, w *sequencePreparedSession, s *sql.Stmt, want int64) (time.Time, []*sql.ColumnType) {
	t.Helper()
	ctx := context.WithValue(w.p.ctx, referenceRecorderKey{}, w.backend.recorder)
	rows, err := s.QueryContext(ctx, int64(70))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"nextval", "timestamp", "setval"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	for i, name := range []string{"INT8", "TIMESTAMP", "INT8"} {
		if types[i].DatabaseTypeName() != name {
			t.Fatal(types[i], name)
		}
	}
	if !rows.Next() {
		t.Fatal("missing temporal row", rows.Err())
	}
	var value, set int64
	var stamp time.Time
	if err := rows.Scan(&value, &stamp, &set); err != nil || value != want || set != 70 {
		t.Fatal(value, set, stamp, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra temporal row", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	r := w.backend.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	var recorded *referenceOperation
	for _, op := range r.operations {
		if op.SQL == w.queries[s] && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil || !recorded.Closed || recorded.CloseErr != nil || !reflect.DeepEqual(recorded.Columns, columns) || len(recorded.Rows) != 1 || len(recorded.Rows[0]) != 3 {
		t.Fatal("temporal consumed recording", recorded)
	}
	for i, name := range []string{"INT8", "TIMESTAMP", "INT8"} {
		referenceAssertRawValue(t, w.backend.pg, name, recorded.Rows[0][i])
	}
	if recorded.Rows[0][0] != want || recorded.Rows[0][2] != int64(70) {
		t.Fatal("recorded temporal effects", recorded.Rows)
	}
	if got, ok := recorded.Rows[0][1].(time.Time); !ok || !got.Equal(stamp) {
		t.Fatal("recorded temporal capture", recorded.Rows)
	}
	return stamp, types
}

func TestReferenceSequencePreparedDynamicTemporalMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
				w.exec(q)
			}
			beforePrepare := time.Now().UTC()
			s := w.hold("SELECT nextval('s'),TIMESTAMP 'now',setval('t',$1,false)")
			afterPrepare := time.Now().UTC()
			first, types := sequencePreparedTemporal(t, w, s, 10)
			if first.Before(beforePrepare.Add(-time.Microsecond)) || first.After(afterPrepare.Add(time.Microsecond)) {
				t.Fatal("Prepare capture bounds", first, beforePrepare, afterPrepare)
			}
			for _, q := range []string{"CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE a.t START 200", "ALTER SEQUENCE b.s CACHE 1"} {
				w.exec(q)
			}
			beforeExecute := time.Now().UTC()
			second, freshTypes := sequencePreparedTemporal(t, w, s, 100)
			afterExecute := time.Now().UTC()
			if !backend.pg {
				if !second.Equal(first) {
					t.Fatal("native Prepare capture changed", first, second)
				}
			} else if second.Before(beforeExecute.Add(-time.Microsecond)) || second.After(afterExecute.Add(time.Microsecond)) {
				t.Fatal("PG reanalysis transaction-start bounds", second, beforeExecute, afterExecute)
			}
			for i := range types {
				if !reflect.DeepEqual(types[i], freshTypes[i]) {
					t.Fatal("full result metadata changed", types[i], freshTypes[i])
				}
			}
			sequenceProbeValue(t, p.ctx, w.c, "SELECT nextval('a.t')", 70)
			sequenceProbeValue(t, p.ctx, w.c, "SELECT nextval('b.t')", 70)
		})
	}
}

func TestReferenceSequencePreparedSameUnlogged(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE UNLOGGED SEQUENCE b.s START 10", "SET search_path TO a,b"} {
				w.exec(q)
			}
			s := w.hold("SELECT nextval('s')")
			w.exec("CREATE SEQUENCE a.s START 100")
			w.exec("ALTER SEQUENCE b.s SET UNLOGGED")
			w.value(s, 10)
			w.exec("ALTER SEQUENCE b.s SET LOGGED")
			w.value(s, 100)
		})
	}
}
