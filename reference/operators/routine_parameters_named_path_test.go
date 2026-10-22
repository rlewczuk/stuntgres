package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Named applicability precedes path masking. Equal declaration signatures need
// not have equal supplied-input shapes after each candidate maps its labels.
func TestReferenceRoutineParametersNamedPath(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_path_first", "rp_path_later")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA rp_path_first", "CREATE SCHEMA rp_path_later",
			"CREATE FUNCTION rp_path_first.pick(a int) RETURNS int LANGUAGE SQL AS 'SELECT 1'",
			"CREATE FUNCTION rp_path_later.pick(b int) RETURNS int LANGUAGE SQL AS 'SELECT 2'",
			"CREATE FUNCTION rp_path_first.reorder(a bigint,b int) RETURNS int LANGUAGE SQL AS 'SELECT 3'",
			"CREATE FUNCTION rp_path_later.reorder(b bigint,a int) RETURNS int LANGUAGE SQL AS 'SELECT 4'",
			"SET search_path TO rp_path_first,rp_path_later,public")
		parametersValue(t, p, b, c, "SELECT pick(b=>1) AS v", "v", "INT4", int64(2))
		parametersValue(t, p, b, c, "SELECT pick(a=>1) AS v", "v", "INT4", int64(1))
		parametersValue(t, p, b, c, "SELECT pick(1) AS v", "v", "INT4", int64(1))
		parametersValue(t, p, b, c, "SELECT reorder(a=>1::int,b=>2::bigint) AS v", "v", "INT4", int64(4))
		parametersValue(t, p, b, c, "SELECT reorder(a=>1::bigint,b=>2::int) AS v", "v", "INT4", int64(3))
		parametersHeld(t, p, b, c, "SELECT reorder(a=>$1::int,b=>$2::bigint) AS v", "INT4", [][]any{{1, 2}, {9, 8}}, []any{int64(4), int64(4)})
	})
}
