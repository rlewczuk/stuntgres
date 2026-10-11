package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependExpressionCookingNullDefaults(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_null_defaults(a INT DEFAULT NULL,b TEXT DEFAULT NULL,c INT DEFAULT 7)")
		table := fixture.PgDependRelation(t, b, "pgdep_null_defaults")
		definition := fixture.PgDependDefinition(t, b, table, 3)
		fixture.PgDependBag(t, p, b, 2604, definition, fixture.PgDependEdge(2604, definition, 0, 1259, table, 3, "a"))
		check := func(want [][]any) {
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT adnum FROM pg_catalog.pg_attrdef WHERE adrelid=$1 ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, want, table)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			}
		}
		check([][]any{{int64(3)}})
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND refclassid=1259 AND refobjid=$1 AND refobjsubid<3", [][]any{}, table))
		fixture.PgDependExec(t, p, b, "ALTER TABLE pgdep_null_defaults ALTER a SET DEFAULT NULL", "ALTER TABLE pgdep_null_defaults ALTER c SET DEFAULT NULL")
		check([][]any{})
		fixture.PgDependBag(t, p, b, 2604, definition)
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND refclassid=1259 AND refobjid=$1", [][]any{}, table))
	})
}
