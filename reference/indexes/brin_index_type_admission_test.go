package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func brinIndexDomainTypes(t *testing.T) {
	t.Helper()
	t.Run("domain", func(t *testing.T) {
		brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE DOMAIN brin_number AS NUMERIC", 0))
			run(fixture.Exec("CREATE TABLE t(id INT,v brin_number)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,1),(2,1.00),(3,2),(4,NULL)", 4))
			for _, key := range []string{"v", "v pg_catalog.numeric_minmax_ops"} {
				run(fixture.Exec("CREATE INDEX bn ON t USING brin("+key+") WITH(pages_per_range=2)", 0))
				s := fixture.Select("SELECT id FROM t WHERE v=1::numeric ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
				run(s)
				run(fixture.Prepared(s))
				run(fixture.Exec("DROP INDEX bn", 0))
			}
		})
	})
}

func brinIndexEnumEligibility(t *testing.T) {
	t.Helper()
	t.Run("enum", func(t *testing.T) {
		brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TYPE brin_mood AS ENUM('calm','busy')", 0))
			run(fixture.Exec("CREATE TABLE t(v brin_mood)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('calm'),(NULL)", 2))
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING brin(v)", fixture.Diagnostic{Code: "42704", Message: `data type brin_mood has no default operator class for access method "brin"`, Hint: "You must specify an operator class for the index or define a default operator class for the data type."})
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT v::text FROM t WHERE v IS NOT NULL", []string{"v"}, []string{"TEXT"}, [][]any{{"calm"}}))
		})
	})
}
