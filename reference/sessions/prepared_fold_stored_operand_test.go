package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Source-grounded expectation:
// REL_18_6 dependency.c records a regprocedure Const's stored dependency, while
// setrefs.c fix_expr_common records only regclass Consts as plan dependencies.
// A routine rename preserves its OID and does not rewrite the containing view.
// The root POINT expression therefore keeps its accepted fold. This public
// presentation-only event does NOT reproduce the private semantic-revision
// contrast in TestPreparedFoldStoredOperandPurpose.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/catalog/dependency.c
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/optimizer/plan/setrefs.c
func TestReferencePreparedFoldStoredOperandRename(t *testing.T) {
	for _, route := range []string{"go", "sql"} {
		t.Run(route, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "fold_stored_operand")
			preparedFoldScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(step fixture.Step) { sqlPreparedStep(t, p, b, c, step) }
				run(fixture.Exec("CREATE SCHEMA fold_stored_operand", 0))
				run(fixture.Exec("CREATE FUNCTION fold_stored_operand.original(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", 0))
				run(fixture.Exec("CREATE VIEW fold_stored_operand.identifier AS SELECT 'fold_stored_operand.original(integer)'::regprocedure AS r", 0))
				run(fixture.Exec("CREATE VIEW fold_stored_operand.nested AS SELECT r FROM fold_stored_operand.identifier", 0))
				identity := func(name string) {
					run(fixture.Select("SELECT r = 'fold_stored_operand."+name+"(integer)'::regprocedure AS same_identity FROM fold_stored_operand.nested", []string{"same_identity"}, []string{"BOOL"}, [][]any{{true}}))
				}
				identity("original")
				run(fixture.Exec("SET extra_float_digits TO 3", 0))
				query := preparedFoldPointSQL + " FROM fold_stored_operand.nested"
				held := preparedFoldHold(t, p, b, c, route, "fold_stored_held", query)
				preparedFoldRead(t, p, b, held, false, true)
				run(fixture.Exec("SET extra_float_digits TO 0", 0))
				run(fixture.Exec("ALTER FUNCTION fold_stored_operand.original(integer) RENAME TO renamed", 0))
				identity("renamed")
				preparedFoldRead(t, p, b, held, false, true)
				fresh := preparedFoldHold(t, p, b, c, route, "fold_stored_fresh", query)
				preparedFoldRead(t, p, b, fresh, false, false)
				preparedFoldRead(t, p, b, held, false, true)
			})
		})
	}
}
