package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutineSequenceUsageSequenceColumns(t *testing.T) {
	rsuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE SEQUENCE rsu_column_s",
			"CREATE SEQUENCE rsu_column_control_s",
			"CREATE FUNCTION rsu_column_single() RETURNS bigint BEGIN ATOMIC SELECT last_value FROM public.rsu_column_s; END",
			"CREATE FUNCTION rsu_column_multi() RETURNS boolean BEGIN ATOMIC SELECT last_value FROM public.rsu_column_s; SELECT is_called FROM public.rsu_column_s; END",
			"CREATE FUNCTION rsu_column_mixed() RETURNS bigint BEGIN ATOMIC SELECT nextval('public.rsu_column_s'); SELECT last_value FROM public.rsu_column_s; END",
			"CREATE FUNCTION rsu_column_control() RETURNS bigint RETURN nextval('public.rsu_column_control_s')",
		} {
			run(fixture.Exec(q, 0))
		}
		var want [][]any
		for _, name := range []string{"rsu_column_single", "rsu_column_multi", "rsu_column_mixed"} {
			id := rsuIdentity(t, p, c, "public", name, "")
			// Membership and multiplicity follow the declared containing sequence,
			// not pg_depend or the provider's interpretation of attribute edges.
			want = append(want, rsuRow(database, "public", name, id, "public", "rsu_column_s"))
		}
		controlID := rsuIdentity(t, p, c, "public", "rsu_column_control", "")
		control := [][]any{rsuRow(database, "public", "rsu_column_control", controlID, "public", "rsu_column_control_s")}
		for _, s := range []fixture.Step{
			rsuProjection(b.PG, "routine_schema='public' AND routine_name IN ('rsu_column_single','rsu_column_multi','rsu_column_mixed')", want),
			// A valid attribute dependency must not poison capture of an unrelated
			// routine, nor an outer filter whose independently expected bag is empty.
			rsuProjection(b.PG, "routine_schema='public' AND routine_name='rsu_column_control'", control),
			rsuProjection(b.PG, "routine_schema='public' AND routine_name='rsu_column_absent'", nil),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
