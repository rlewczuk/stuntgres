package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDropOperatorDependency(t *testing.T) {
	routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
		routineExec(t, p, b, c, "CREATE FUNCTION rd_op(integer,integer) RETURNS boolean LANGUAGE SQL AS $$SELECT $1=$2$$", "CREATE OPERATOR public.#=# (LEFTARG=integer, RIGHTARG=integer, FUNCTION=rd_op)")
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function rd_op(integer,integer) because other objects depend on it")
		d.Detail = "operator #=#(integer,integer) depends on function rd_op(integer,integer)"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		routineDropError(t, p, b, c, "DROP FUNCTION rd_op(integer,integer) RESTRICT", d)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT rd_op(4,4) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		routineExec(t, p, b, c, "DROP ROUTINE rd_op(integer,integer) CASCADE")
		notice := routineDropNotice("drop cascades to operator #=#(integer,integer)")
		routineDropNotices(t, n, notice)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_operator WHERE oprname='#=#' AND oprnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
