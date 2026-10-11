package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualFeatureSmoke(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a integer,b integer GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, `SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 0)
		stmt, err := b.DB.PrepareContext(p.Ctx, `INSERT INTO g(a) VALUES($1)`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, v := range []any{2, nil} {
			r, err := stmt.ExecContext(p.Ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		}
		run(fixture.Select(`SELECT * FROM g WHERE a=2`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Select(`SELECT * FROM g WHERE a IS NULL`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		run(fixture.Prepared(fixture.Select(`UPDATE g SET a=8 WHERE a=2 RETURNING a,b`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}})))
		run(fixture.Exec(`UPDATE g SET b=DEFAULT`, 2))
		run(fixture.ImplicitError(`INSERT INTO g VALUES(5,6)`, fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "b"`, Detail: `Column "b" is a generated column.`}))
		run(fixture.Exec(`CREATE TABLE omitted(a INT,b INT GENERATED ALWAYS AS(1/a))`, 0))
		run(fixture.Exec(`INSERT INTO omitted(a) VALUES(0)`, 1))
		run(fixture.Select(`SELECT a FROM omitted`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
		s := fixture.ImplicitError(`SELECT b FROM omitted`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.SelectRows = true
		run(s)
	})
}

func TestReferenceGeneratedVirtualInsertSurfaces(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			exec := func(q string, n int64) { s := fixture.Exec(q, n); s.Prepared = prepared; run(s) }
			exec(`CREATE TABLE g(b INT GENERATED ALWAYS AS(a+1) VIRTUAL,a INT DEFAULT 7,c BIGINT GENERATED ALWAYS AS(a*2) VIRTUAL)`, 0)
			exec(`INSERT INTO g DEFAULT VALUES`, 1)
			exec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2)
			exec(`INSERT INTO g(a,b,c) VALUES(4,DEFAULT,DEFAULT),(5,DEFAULT,DEFAULT)`, 2)
			exec(`INSERT INTO g(b,a,c) VALUES(DEFAULT,6,DEFAULT)`, 1)
			exec(`INSERT INTO g(a) SELECT 9`, 1)
			s := fixture.Select(`SELECT * FROM g`, []string{"b", "a", "c"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(8), int64(7), int64(14)}, {int64(3), int64(2), int64(4)}, {nil, nil, nil}, {int64(5), int64(4), int64(8)}, {int64(6), int64(5), int64(10)}, {int64(7), int64(6), int64(12)}, {int64(10), int64(9), int64(18)}})
			s.Bag = true
			run(s)
			exec(`CREATE TABLE only_generated(b INT GENERATED ALWAYS AS(1) VIRTUAL)`, 0)
			exec(`INSERT INTO only_generated DEFAULT VALUES`, 1)
			run(fixture.Select(`SELECT * FROM only_generated`, []string{"b"}, []string{"INT4"}, [][]any{{int64(1)}}))
			exec(`CREATE TABLE short(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0)
			exec(`INSERT INTO short VALUES(3)`, 1)
			run(fixture.Select(`SELECT * FROM short`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}))
		})
	}
}

func TestReferenceGeneratedVirtualTypedValues(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b BIGINT GENERATED ALWAYS AS(abs(a)*2) VIRTUAL,c TEXT GENERATED ALWAYS AS(a+1) VIRTUAL,d INT GENERATED ALWAYS AS(CASE WHEN a IS NULL THEN 7 ELSE COALESCE(a,9) END) VIRTUAL,e INT[] GENERATED ALWAYS AS(ARRAY[a,a+1]) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(-2),(NULL)`, 2))
		run(fixture.Select(`SELECT a,b,c,d,e FROM g WHERE a=-2`, []string{"a", "b", "c", "d", "e"}, []string{"INT4", "INT8", "TEXT", "INT4", "_INT4"}, [][]any{{int64(-2), int64(4), "-1", int64(-2), "{-2,-1}"}}))
		run(fixture.Select(`SELECT a,b,c,d,e FROM g WHERE a IS NULL`, []string{"a", "b", "c", "d", "e"}, []string{"INT4", "INT8", "TEXT", "INT4", "_INT4"}, [][]any{{nil, nil, nil, int64(7), "{NULL,NULL}"}}))
		run(fixture.Exec(`CREATE TABLE oidg(a INT,b oid GENERATED ALWAYS AS(tableoid) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO oidg(a) VALUES(2)`, 1))
		var owner, value int64
		if err := b.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&owner); err != nil || owner <= 0 {
			t.Fatal(owner, err)
		}
		if err := b.DB.QueryRowContext(p.Ctx, `SELECT b FROM oidg`).Scan(&value); err != nil || value != owner {
			t.Fatal(value, owner, err)
		}
	})
}
