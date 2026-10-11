package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSmokeSequenceMergedProducerSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE SEQUENCE s", 0),
				fixture.Exec("CREATE TABLE src(id INT,day DATE,payload BYTEA)", 0),
				fixture.Exec("INSERT INTO src VALUES(1,DATE '2024-02-29','\\x01'),(2,DATE '2024-03-01','\\x02')", 2),
				fixture.Exec("CREATE TABLE dst(id BIGINT,n BIGINT,day DATE,payload BYTEA)", 0),
			} {
				run(step)
			}
			q := "INSERT INTO dst SELECT id::bigint,nextval('s'::regclass),day,payload FROM src ORDER BY id"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, backend.Recorder, fixture.Exec(q, 2))
			stmt.Close()
			days := []any{"2024-02-29", "2024-03-01"}
			if backend.PG {
				days = []any{time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)}
			}
			step := fixture.Select("SELECT * FROM dst ORDER BY id", []string{"id", "n", "day", "payload"}, []string{"INT8", "INT8", "DATE", "BYTEA"}, [][]any{{int64(1), int64(1), days[0], "\x01"}, {int64(2), int64(2), days[1], "\x02"}})
			var retained [2][]byte
			step.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
				var id, n int64
				var day any
				var payload []byte
				if err := rows.Scan(&id, &n, &day, &payload); err != nil {
					t.Fatal(err)
				}
				if id != int64(i+1) || n != id || !smokeSequenceProducerDateEqual(day, days[i]) || !bytes.Equal(payload, []byte{byte(i + 1)}) {
					t.Fatal(id, n, day, payload)
				}
				retained[i] = payload
			}
			run(step)
			if !bytes.Equal(retained[0], []byte{1}) || !bytes.Equal(retained[1], []byte{2}) {
				t.Fatal("BYTEA scans changed after cursor advance/close", retained)
			}
			retained[0][0] = 9
			if retained[1][0] != 2 {
				t.Fatal("BYTEA scans share storage")
			}
			run(fixture.Exec("INSERT INTO dst(id,n) SELECT id::bigint,nextval('s') FROM src WHERE id=99", 0))
			bad := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			failure := fixture.ErrorDiagnostic("SELECT setval('s',$1::text::bigint) FROM src WHERE id=99", bad, "bad")
			failure.SelectRows, failure.ErrorStage = true, "execute"
			run(failure)
			run(fixture.SequenceInt64Step(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}})))
			run(fixture.Exec("INSERT INTO dst(n) SELECT last_value FROM s", 1))
		})
	}
}

func smokeSequenceProducerDateEqual(got, want any) bool {
	if expected, ok := want.(time.Time); ok {
		actual, ok := got.(time.Time)
		if !ok {
			return false
		}
		_, offset := actual.Zone()
		y, m, d := actual.Date()
		wy, wm, wd := expected.Date()
		return actual.Equal(expected) && offset == 0 && y == wy && m == wm && d == wd &&
			actual.Hour() == 0 && actual.Minute() == 0 && actual.Second() == 0 && actual.Nanosecond() == 0
	}
	actual, ok := got.(string)
	expected, expectedOK := want.(string)
	return ok && expectedOK && actual == expected
}
