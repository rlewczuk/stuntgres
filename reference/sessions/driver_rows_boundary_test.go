package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverRowsBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE metadata(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)", 0))
			for _, populated := range []bool{false, true} {
				if populated {
					run(fixture.Exec("INSERT INTO metadata VALUES(1,2,'x',FALSE)", 1))
				}
				var want [][]any
				if populated {
					want = [][]any{{int64(1), int64(2), "x", false}}
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT * FROM metadata", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, want)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.DriverBasicMetadata(t, rows, backend.PG, s.Types, []bool{false, true, true, true})
					}
					run(s)
				}
			}
		})
	}
}
