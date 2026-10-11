package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualConstraints(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE vn(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL NOT NULL)`, 0))
		run(fixture.Exec(`INSERT INTO vn(a) VALUES(2)`, 1))
		nn := fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "vn" violates not-null constraint`, Detail: "Failing row contains (null, virtual).", Schema: "public", Table: "vn", Column: "b"}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`INSERT INTO vn(a) VALUES(4),(NULL)`, `UPDATE vn SET a=NULL`} {
				s := fixture.ImplicitError(q, nn)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
		run(fixture.Select(`SELECT * FROM vn`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Exec(`CREATE TABLE vc(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL CHECK(b>0))`, 0))
		run(fixture.Exec(`INSERT INTO vc(a) VALUES(2)`, 1))
		ck := fixture.Diagnostic{Code: "23514", Message: `new row for relation "vc" violates check constraint "vc_b_check"`, Detail: "Failing row contains (-2, virtual).", Schema: "public", Table: "vc", Constraint: "vc_b_check"}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`INSERT INTO vc(a) VALUES(4),(-2)`, `UPDATE vc SET a=-2`} {
				s := fixture.ImplicitError(q, ck)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
		run(fixture.Select(`SELECT * FROM vc`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Exec(`INSERT INTO vc(a) VALUES(NULL)`, 1))
		run(fixture.Exec(`CREATE TABLE demanded(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL CHECK(b>0))`, 0))
		run(fixture.ImplicitError(`INSERT INTO demanded(a) VALUES(0)`, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.Exec(`CREATE TABLE nndemand(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL NOT NULL)`, 0))
		run(fixture.ImplicitError(`INSERT INTO nndemand(a) VALUES(0)`, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.Exec(`CREATE TABLE alterg(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`ALTER TABLE alterg ALTER COLUMN b SET NOT NULL`, 0))
		run(fixture.Exec(`INSERT INTO alterg(a) VALUES(3)`, 1))
		run(fixture.Exec(`ALTER TABLE alterg ALTER COLUMN b DROP NOT NULL`, 0))
		run(fixture.Exec(`ALTER TABLE alterg ALTER COLUMN b SET NOT NULL`, 0))
		run(fixture.Select(`SELECT * FROM alterg`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}))
	})
}

func TestReferenceGeneratedVirtualIndexBoundaries(t *testing.T) {
	for _, constraint := range []string{"UNIQUE", "PRIMARY KEY"} {
		generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			message := "unique constraints on virtual generated columns are not supported"
			if constraint == "PRIMARY KEY" {
				message = "primary keys on virtual generated columns are not supported"
			}
			run(fixture.ImplicitError(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL `+constraint+`)`, fixture.Diagnostic{Code: "0A000", Message: message}))
			run(fixture.ImplicitError(`SELECT * FROM g`, fixture.Diagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
			run(fixture.Exec(`CREATE TABLE g(a INT)`, 0))
		})
	}
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		for _, q := range []string{`CREATE INDEX vi ON g(b)`, `CREATE INDEX vi ON g((b+1))`, `CREATE INDEX vi ON g(a) WHERE b>0`, `CREATE INDEX vi ON g(a) INCLUDE(b)`, `CREATE INDEX vi ON g((CASE WHEN FALSE THEN b ELSE a END))`} {
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "indexes on virtual generated columns are not supported"}))
			run(fixture.Select(`SELECT indexname FROM pg_indexes WHERE indexname='vi'`, []string{"indexname"}, []string{"NAME"}, nil))
		}
		run(fixture.Exec(`CREATE INDEX vi ON g(a)`, 0))
		q := `CREATE TABLE part(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL) PARTITION BY RANGE(b)`
		run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: "cannot use generated column in partition key", Detail: `Column "b" is a generated column.`, Position: fixture.GeneratedStoredPosition(q, "b)")}))
	})
}
