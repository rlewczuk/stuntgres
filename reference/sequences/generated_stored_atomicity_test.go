package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedStoredTypes(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE DOMAIN positive AS INT`, 0))
		run(fixture.Exec(`CREATE TABLE g(a BIGINT,b INT GENERATED ALWAYS AS(a+1) STORED,c BIGINT GENERATED ALWAYS AS(a+1) STORED,d positive GENERATED ALWAYS AS(a) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.Select(`SELECT a,b,c,d FROM g`, []string{"a", "b", "c", "d"}, []string{"INT8", "INT4", "INT8", "INT4"}, [][]any{{int64(2), int64(3), int64(3), int64(2)}}))
		run(fixture.ImplicitError(`INSERT INTO g(a) VALUES(2147483647)`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(-1)`, 1))
		run(fixture.Select(`SELECT a,d FROM g ORDER BY a`, []string{"a", "d"}, []string{"INT8", "INT4"}, [][]any{{int64(-1), int64(-1)}, {int64(2), int64(2)}}))
	})
}

func TestReferenceGeneratedStoredAtomicity(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(1),(2)`, 2))
		run(fixture.ImplicitError(`UPDATE g SET a=CASE WHEN a=1 THEN 3 ELSE 2147483647 END`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}))
		run(fixture.Select(`SELECT * FROM g ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}}))
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		txn := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		txn(fixture.Exec(`BEGIN`, 0))
		txn(fixture.Exec(`SAVEPOINT s`, 0))
		txn(fixture.ImplicitError(`INSERT INTO g(a) VALUES(4),(2147483647)`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"}))
		txn(fixture.ImplicitError(`INSERT INTO g(a) VALUES(5)`, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		txn(fixture.Exec(`ROLLBACK TO s`, 0))
		txn(fixture.Exec(`INSERT INTO g(a) VALUES(5)`, 1))
		txn(fixture.Exec(`COMMIT`, 0))
		run(fixture.Select(`SELECT * FROM g ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(5), int64(6)}}))
	})
}

func TestReferenceGeneratedStoredConstraints(t *testing.T) {
	for _, tc := range []struct {
		definition, q string
		want          fixture.Diagnostic
	}{
		{`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED NOT NULL)`, `INSERT INTO g(a) VALUES(2),(NULL)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "g" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "g", Column: "b"}},
		{`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED CHECK(b<5))`, `INSERT INTO g(a) VALUES(2),(5)`, fixture.Diagnostic{Code: "23514", Message: `new row for relation "g" violates check constraint "g_b_check"`, Detail: "Failing row contains (5, 6).", Schema: "public", Table: "g", Constraint: "g_b_check"}},
		{`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED UNIQUE)`, `INSERT INTO g(a) VALUES(2),(2)`, fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "g_b_key"`, Detail: "Key (b)=(3) already exists.", Schema: "public", Table: "g", Constraint: "g_b_key"}},
	} {
		t.Run(tc.definition, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(tc.definition, 0))
				run(fixture.ImplicitError(tc.q, tc.want))
				run(fixture.Select(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, nil))
			})
		})
	}
}
