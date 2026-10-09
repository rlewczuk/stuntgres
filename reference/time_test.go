package reference_test

import (
	"reflect"
	"testing"
	"time"
)

func TestReferenceTimeValuesParametersAndMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			query := func(q string, names, types []string, rows [][]any, args ...any) {
				s := referenceSelect(q, names, types, referenceTimeRows(t, backend.pg, types, rows), args...)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE baseline_time(v TIME(6))", 0))
			run(referenceExec("INSERT INTO baseline_time VALUES('24:00')", 1))
			query("SELECT v FROM baseline_time", []string{"v"}, []string{"TIME"}, [][]any{{"24:00:00"}})
			run(referenceExec("CREATE TABLE t(id INTEGER,v TIME(0),fine TIME(6))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'00:00','00:00'),(2,'24:00','24:00'),(3,'04:05 PM','04:05:06.120000'),(4,NULL,NULL)", 4))
			query("SELECT id,v,fine FROM t ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12"}, {int64(4), nil, nil}})
			query("SELECT TIME '24:00',TIME(3) '04:05:06.1235' FROM t WHERE id=1", []string{"time", "time"}, []string{"TIME", "TIME"}, [][]any{{"24:00:00", "04:05:06.124"}})
			rows, err := backend.db.QueryContext(p.ctx, "SELECT v,fine FROM t")
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, m := range metadata {
				want := reflect.TypeFor[string]()
				if backend.pg {
					want = reflect.TypeFor[time.Time]()
				}
				if m.DatabaseTypeName() != "TIME" || m.ScanType() != want {
					t.Fatal(m, want)
				}
			}
		})
	}
}

func TestReferenceTimeDeclarationsAndRetainedMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, prepared := range []bool{false, true} {
				run(referenceExec("CREATE TABLE t(id INTEGER,v TIME(0),fine TIME(6) WITHOUT TIME ZONE)", 0))
				s := referenceExec("INSERT INTO t VALUES(1,'00:00','00:00'),(2,'24:00','24:00'),(3,'04:05 PM','04:05:06.120000'),(4,NULL,NULL)", 4)
				s.prepared = prepared
				run(s)
				run(referenceSelect("SELECT id,v,fine FROM t ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, referenceTimeRows(t, b.pg, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12"}, {int64(4), nil, nil}})))
				run(referenceSelect("SELECT TIME '24:00',TIME(3) '04:05:06.1235' FROM t WHERE id=1", []string{"time", "time"}, []string{"TIME", "TIME"}, referenceTimeRows(t, b.pg, []string{"TIME", "TIME"}, [][]any{{"24:00:00", "04:05:06.124"}})))
				run(referenceExec(`CREATE TABLE quoted(v "time"(3) NOT NULL,w U&"t\0069me"(6))`, 0))
				rows, err := b.db.QueryContext(p.ctx, "SELECT v,w FROM quoted")
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 2 {
					rows.Close()
					t.Fatal(meta, err)
				}
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("unexpected quoted row", rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				referenceTemporalMetadata(t, b.pg, meta)
				for i, typ := range meta {
					nullable, known := typ.Nullable()
					if b.pg {
						if known {
							t.Fatal("pq nullable must be unknown")
						}
					} else if !known || nullable != (i == 1) {
						t.Fatal("native nullability", typ)
					}
				}
				run(referenceSelect("SELECT v,w FROM quoted", []string{"v", "w"}, []string{"TIME", "TIME"}, nil))
				run(referenceExec("DROP TABLE quoted", 0))
				run(referenceExec("DROP TABLE t", 0))
			}
		})
	}
}
