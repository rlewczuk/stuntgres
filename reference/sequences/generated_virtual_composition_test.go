package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualComposition(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`CREATE VIEW v AS SELECT a,b FROM g`, 0))
		run(fixture.Exec(`INSERT INTO v(a) VALUES(2)`, 1))
		run(fixture.Exec(`INSERT INTO v VALUES(4,DEFAULT)`, 1))
		run(fixture.ImplicitError(`UPDATE v SET b=DEFAULT`, fixture.Diagnostic{Code: "428C9", Message: `column "b" can only be updated to DEFAULT`, Detail: `Column "b" is a generated column.`}))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`INSERT INTO v VALUES(6,7)`, `UPDATE v SET b=9 WHERE FALSE`} {
				message := `cannot insert a non-DEFAULT value into column "b"`
				if q == `UPDATE v SET b=9 WHERE FALSE` {
					message = `column "b" can only be updated to DEFAULT`
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "428C9", Message: message, Detail: `Column "b" is a generated column.`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		run(fixture.Select(`SELECT * FROM g ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}, {int64(4), int64(5)}}))
		run(fixture.Exec(`CREATE TABLE eager(a INT,b INT GENERATED ALWAYS AS(1/a) STORED,c INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.ImplicitError(`INSERT INTO eager(a) VALUES(0)`, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.Select(`SELECT * FROM eager`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, nil))
		q := `CREATE TABLE failed(a INT GENERATED ALWAYS AS IDENTITY,b INT GENERATED ALWAYS AS(b+1) VIRTUAL,UNIQUE(a))`
		run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: `cannot use generated column "b" in column generation expression`, Detail: "A generated column cannot reference another generated column.", Position: fixture.GeneratedStoredPosition(q, "b+1")}))
		run(fixture.Select(`SELECT relname FROM pg_class WHERE relname='failed' OR relname='failed_a_seq' OR relname='failed_a_key'`, []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec(`CREATE TABLE failed(a INT GENERATED ALWAYS AS IDENTITY,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO failed DEFAULT VALUES`, 1))
		run(fixture.Select(`SELECT * FROM failed`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	})
}
