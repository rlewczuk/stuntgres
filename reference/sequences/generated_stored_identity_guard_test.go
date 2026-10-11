package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedStoredIdentityGuard(t *testing.T) {
	for _, notNull := range []bool{false, true} {
		t.Run(map[bool]string{false: "nullable", true: "not-null"}[notNull], func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				suffix := ""
				message := `column "b" of relation "g" must be declared NOT NULL before identity can be added`
				if notNull {
					suffix = " NOT NULL"
					message = `column "b" of relation "g" already has a default value`
				}
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED`+suffix+`)`, 0))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
				run(fixture.ImplicitError(`ALTER TABLE g ALTER b ADD GENERATED ALWAYS AS IDENTITY`, fixture.Diagnostic{Code: "55000", Message: message}))
				run(fixture.Select(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
				run(fixture.Select(`SELECT relname FROM pg_class WHERE relname='g_b_seq'`, []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
				run(fixture.Select(`SELECT * FROM g WHERE a=4`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}}))
			})
		})
	}
}
