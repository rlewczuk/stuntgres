package reference_test

import "testing"

func TestReferencePGDependExpressionCookingNullDefaults(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE pgdep_null_defaults(a INT DEFAULT NULL,b TEXT DEFAULT NULL,c INT DEFAULT 7)")
		table := pgDependRelation(t, b, "pgdep_null_defaults")
		definition := pgDependDefinition(t, b, table, 3)
		pgDependBag(t, p, b, 2604, definition, pgDependEdge(2604, definition, 0, 1259, table, 3, "a"))
		check := func(want [][]any) {
			for _, prepared := range []bool{false, true} {
				step := referenceSelect("SELECT adnum FROM pg_catalog.pg_attrdef WHERE adrelid=$1 ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, want, table)
				step.prepared = prepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, step)
			}
		}
		check([][]any{{int64(3)}})
		pgDescriptionExecutorRows(t, p, b, b.db, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND refclassid=1259 AND refobjid=$1 AND refobjsubid<3", [][]any{}, table))
		pgDependExec(t, p, b, "ALTER TABLE pgdep_null_defaults ALTER a SET DEFAULT NULL", "ALTER TABLE pgdep_null_defaults ALTER c SET DEFAULT NULL")
		check([][]any{})
		pgDependBag(t, p, b, 2604, definition)
		pgDescriptionExecutorRows(t, p, b, b.db, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND refclassid=1259 AND refobjid=$1", [][]any{}, table))
	})
}
