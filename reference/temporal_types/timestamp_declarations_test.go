package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimestampQuotedUnicodeTypesAndKeywordIdentifiers(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Prepared(fixture.Exec(`CREATE TABLE timestamp(without "timestamp"(3),time U&"time\0073tamp"(3),zone TIMESTAMP,timestamp TIMESTAMP)`, 0)))
			run(fixture.Prepared(fixture.Exec("INSERT INTO timestamp VALUES($1,$1,'epoch','epoch')", 1, "2000-01-01 00:00:00.1236")))
			want := [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}
			query := fixture.Select("SELECT without,time,zone,timestamp FROM timestamp", []string{"without", "time", "zone", "timestamp"}, []string{"TIMESTAMP", "TIMESTAMP", "TIMESTAMP", "TIMESTAMP"}, want)
			run(query)
			run(fixture.Prepared(query))
		})
	}
	t.Run("unicode-first-layout", func(t *testing.T) {
		p := newTimestampReferenceScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				db := backend.DB
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				referenceTimestampExec(t, ctx, db, `CREATE TABLE timestamp(without U&"time\0073tamp"(3),time TIMESTAMP,zone TIMESTAMP,timestamp TIMESTAMP)`)
				referenceTimestampExec(t, ctx, db, `INSERT INTO timestamp VALUES('2000-01-01 00:00:00.1236','epoch','epoch','epoch')`)
				referenceTimestampQuery(t, ctx, db, backend.Recorder, "SELECT without,time,zone,timestamp FROM timestamp", []string{"without", "time", "zone", "timestamp"}, []string{"TIMESTAMP", "TIMESTAMP", "TIMESTAMP", "TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
				q := "SELECT TIMESTAMP 'epoch'::timestamp FROM timestamp"
				want := [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}
				referenceTimestampQuery(t, ctx, db, backend.Recorder, q, []string{"timestamp"}, []string{"TIMESTAMP"}, want)
				stmt, err := db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.QueryContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				referenceTimestampCheckRows(t, rows, backend.PG, []string{"timestamp"}, []string{"TIMESTAMP"}, want)
			})
		}
	})
}

func TestReferenceTimestampOrdinalBoundaryOrdering(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INTEGER,v TIMESTAMP)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'2000-01-01'),(2,TIMESTAMP '1999.366'),(3,'1999.365'),(4,'2000 366')", 4))
			run(fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{
				{time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)},
				{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)},
			}))
			equal := fixture.Select("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, "1999.366")
			run(equal)
			run(fixture.Prepared(equal))
		})
	}
}
