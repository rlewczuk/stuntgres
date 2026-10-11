package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeValuesParametersAndMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			query := func(q string, names, types []string, rows [][]any, args ...any) {
				s := fixture.Select(q, names, types, fixture.TimeRows(t, backend.PG, types, rows), args...)
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE TABLE baseline_time(v TIME(6))", 0))
			run(fixture.Exec("INSERT INTO baseline_time VALUES('24:00')", 1))
			query("SELECT v FROM baseline_time", []string{"v"}, []string{"TIME"}, [][]any{{"24:00:00"}})
			run(fixture.Exec("CREATE TABLE t(id INTEGER,v TIME(0),fine TIME(6))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'00:00','00:00'),(2,'24:00','24:00'),(3,'04:05 PM','04:05:06.120000'),(4,NULL,NULL)", 4))
			query("SELECT id,v,fine FROM t ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12"}, {int64(4), nil, nil}})
			query("SELECT TIME '24:00',TIME(3) '04:05:06.1235' FROM t WHERE id=1", []string{"time", "time"}, []string{"TIME", "TIME"}, [][]any{{"24:00:00", "04:05:06.124"}})
			rows, err := backend.DB.QueryContext(p.Ctx, "SELECT v,fine FROM t")
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
				if backend.PG {
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
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("CREATE TABLE t(id INTEGER,v TIME(0),fine TIME(6) WITHOUT TIME ZONE)", 0))
				s := fixture.Exec("INSERT INTO t VALUES(1,'00:00','00:00'),(2,'24:00','24:00'),(3,'04:05 PM','04:05:06.120000'),(4,NULL,NULL)", 4)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT id,v,fine FROM t ORDER BY id", []string{"id", "v", "fine"}, []string{"INT4", "TIME", "TIME"}, fixture.TimeRows(t, b.PG, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12"}, {int64(4), nil, nil}})))
				run(fixture.Select("SELECT TIME '24:00',TIME(3) '04:05:06.1235' FROM t WHERE id=1", []string{"time", "time"}, []string{"TIME", "TIME"}, fixture.TimeRows(t, b.PG, []string{"TIME", "TIME"}, [][]any{{"24:00:00", "04:05:06.124"}})))
				run(fixture.Exec(`CREATE TABLE quoted(v "time"(3) NOT NULL,w U&"t\0069me"(6))`, 0))
				rows, err := b.DB.QueryContext(p.Ctx, "SELECT v,w FROM quoted")
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
				fixture.TemporalMetadata(t, b.PG, meta)
				for i, typ := range meta {
					nullable, known := typ.Nullable()
					if b.PG {
						if known {
							t.Fatal("pq nullable must be unknown")
						}
					} else if !known || nullable != (i == 1) {
						t.Fatal("native nullability", typ)
					}
				}
				run(fixture.Select("SELECT v,w FROM quoted", []string{"v", "w"}, []string{"TIME", "TIME"}, nil))
				run(fixture.Exec("DROP TABLE quoted", 0))
				run(fixture.Exec("DROP TABLE t", 0))
			}
		})
	}
}
