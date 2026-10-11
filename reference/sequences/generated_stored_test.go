package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Stored-column scenarios use one body for every available backend.
func generatedStoredScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			body(t, p, backend, run)
		})
	}
}

func TestReferenceGeneratedStoredSmoke(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a integer,b integer GENERATED ALWAYS AS(a+1) STORED)`, 0))
		fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, false, `SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 0)
		insert, err := backend.DB.PrepareContext(p.Ctx, `INSERT INTO g(a) VALUES($1)`)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		for _, value := range []any{2, nil} {
			result, err := insert.ExecContext(p.Ctx, value)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		}
		run(fixture.Select(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Select(`SELECT * FROM g WHERE a IS NULL`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		run(fixture.Exec(`INSERT INTO g VALUES(4,DEFAULT)`, 1))
		run(fixture.Prepared(fixture.Exec(`UPDATE g SET a=8 WHERE a=2`, 1)))
		run(fixture.Exec(`UPDATE g SET b=DEFAULT WHERE a=8`, 1))
		run(fixture.Select(`SELECT * FROM g WHERE a=8`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
		run(fixture.ImplicitError(`INSERT INTO g VALUES(5,6)`, fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "b"`, Detail: `Column "b" is a generated column.`}))
		run(fixture.ImplicitError(`UPDATE g SET b=1`, fixture.Diagnostic{Code: "428C9", Message: `column "b" can only be updated to DEFAULT`, Detail: `Column "b" is a generated column.`}))
		run(fixture.ImplicitError(`INSERT INTO g(a) VALUES(7),(2147483647)`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}))
		run(fixture.Select(`SELECT * FROM g WHERE a=7`, []string{"a", "b"}, []string{"INT4", "INT4"}, nil))
		fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, true, `SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 3)
	})
}

func TestReferenceGeneratedStoredInsertSurfaces(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
			exec := func(q string, n int64) { s := fixture.Exec(q, n); s.Prepared = prepared; run(s) }
			exec(`CREATE TABLE g(b INT GENERATED ALWAYS AS(a+1) STORED,a INT DEFAULT 7,c BIGINT GENERATED ALWAYS AS(a*2) STORED)`, 0)
			exec(`INSERT INTO g DEFAULT VALUES`, 1)
			exec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2)
			exec(`INSERT INTO g(a,b,c) VALUES(4,DEFAULT,DEFAULT),(5,DEFAULT,DEFAULT)`, 2)
			exec(`INSERT INTO g(b,a,c) VALUES(DEFAULT,6,DEFAULT)`, 1)
			exec(`INSERT INTO g(a) SELECT 9`, 1)
			bag := fixture.Select(`SELECT * FROM g`, []string{"b", "a", "c"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(8), int64(7), int64(14)}, {int64(3), int64(2), int64(4)}, {nil, nil, nil}, {int64(5), int64(4), int64(8)}, {int64(6), int64(5), int64(10)}, {int64(7), int64(6), int64(12)}, {int64(10), int64(9), int64(18)}})
			bag.Bag = true
			run(bag)
			exec(`CREATE TABLE only_generated(b INT GENERATED ALWAYS AS(1) STORED)`, 0)
			exec(`INSERT INTO only_generated DEFAULT VALUES`, 1)
			run(fixture.Select(`SELECT * FROM only_generated`, []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}}))
			exec(`CREATE TABLE short(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0)
			exec(`INSERT INTO short VALUES(3)`, 1)
			run(fixture.Select(`SELECT * FROM short`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}))
		})
	}
}

func TestReferenceGeneratedStoredUpdate(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,c INT,b INT GENERATED ALWAYS AS(a+c) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a,c) VALUES(2,5),(3,6)`, 2))
		run(fixture.Exec(`UPDATE g SET a=c,c=a`, 2))
		run(fixture.Select(`SELECT * FROM g ORDER BY a`, []string{"a", "c", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(2), int64(7)}, {int64(6), int64(3), int64(9)}}))
		returned := fixture.Select(`UPDATE g SET a=a+1 RETURNING a,c,b`, []string{"a", "c", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(6), int64(2), int64(8)}, {int64(7), int64(3), int64(10)}})
		returned.Bag = true
		run(returned)
		run(fixture.Exec(`UPDATE g SET b=DEFAULT`, 2))
	})
}
