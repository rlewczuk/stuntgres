package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedStoredLifecycleDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want fixture.Diagnostic
	}{
		{`ALTER TABLE g ALTER COLUMN b SET DEFAULT 2`, fixture.Diagnostic{Code: "42601", Message: `column "b" of relation "g" is a generated column`, Hint: "Use ALTER TABLE ... ALTER COLUMN ... SET EXPRESSION instead."}},
		{`ALTER TABLE g ALTER COLUMN b DROP DEFAULT`, fixture.Diagnostic{Code: "42601", Message: `column "b" of relation "g" is a generated column`, Hint: "Use ALTER TABLE ... ALTER COLUMN ... DROP EXPRESSION instead."}},
		{`ALTER TABLE g ALTER COLUMN a TYPE BIGINT`, fixture.Diagnostic{Code: "0A000", Message: "cannot alter type of a column used by a generated column", Detail: `Column "a" is used by generated column "b".`}},
		{`ALTER TABLE g ALTER COLUMN b TYPE BIGINT USING b::bigint`, fixture.Diagnostic{Code: "42611", Message: "cannot specify USING when altering type of generated column", Detail: `Column "b" is a generated column.`, Position: "28"}},
		{`ALTER TABLE g DROP COLUMN a`, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column a of table g because other objects depend on it", Detail: "column b of table g depends on column a of table g", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
				run(fixture.ImplicitError(tc.q, tc.want))
				run(fixture.Select(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			})
		})
	}
}

func TestReferenceGeneratedStoredLifecycle(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(extra INT,a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.Exec(`ALTER TABLE g DROP COLUMN extra`, 0))
		run(fixture.Exec(`UPDATE g SET a=8`, 1))
		run(fixture.Select(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
		run(fixture.Exec(`ALTER TABLE g DROP COLUMN a CASCADE`, 0))
		var owner int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='g'`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select(`SELECT adnum FROM pg_attrdef WHERE adrelid=$1`, []string{"adnum"}, []string{"INT2"}, nil, owner))
		run(fixture.Select(`SELECT * FROM g`, []string{}, []string{}, [][]any{{}}))
	})
}

func TestReferenceGeneratedStoredExpressionDomainDependency(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE DOMAIN generation_input AS INT`, 0))
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a::generation_input) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.Select(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}}))
		run(fixture.Exec(`DROP DOMAIN generation_input CASCADE`, 0))
		run(fixture.Select(`SELECT * FROM g`, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec(`INSERT INTO g VALUES(3)`, 1))
		run(fixture.Select(`SELECT * FROM g ORDER BY a`, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
	})
}
