package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferencePGNotifyCatalog pins the released pg_proc declaration: OID 3036,
// text,text -> void, non-strict VOLATILE and parallel-restricted, internal
// implementation, cost 1, no rows, unnamed formals and no defaults.
func TestReferencePGNotifyCatalog(t *testing.T) {
	query := `SELECT p.oid::bigint, p.proname::text, p.pronamespace::bigint,
p.prolang::bigint, p.pronargs::integer, p.proargtypes::text, p.prorettype::bigint,
p.proisstrict, p.provolatile::text, p.proparallel::text, p.proretset, p.procost,
p.prorows, p.prosrc::text, p.proargnames::text, pg_get_function_result(p.oid),
pg_get_function_identity_arguments(p.oid), pg_get_function_arguments(p.oid)
FROM pg_catalog.pg_proc p WHERE p.oid = 3036`
	step := fixture.Select(query,
		[]string{
			"oid", "proname", "pronamespace", "prolang", "pronargs", "proargtypes",
			"prorettype", "proisstrict", "provolatile", "proparallel", "proretset",
			"procost", "prorows", "prosrc", "proargnames",
			"pg_get_function_result", "pg_get_function_identity_arguments",
			"pg_get_function_arguments",
		},
		[]string{
			"INT8", "TEXT", "INT8", "INT8", "INT4", "TEXT", "INT8", "BOOL",
			"TEXT", "TEXT", "BOOL", "FLOAT4", "FLOAT4", "TEXT", "TEXT",
			"TEXT", "TEXT", "TEXT",
		},
		[][]any{{
			int64(3036), "pg_notify", int64(11), int64(12), int64(2), "25 25",
			int64(2278), false, "v", "r", false, float64(1), float64(0),
			"pg_notify", nil, "void", "text, text", "text, text",
		}},
	)
	pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		s, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		fixture.RunStep(t, p.Ctx, s, b.Recorder, step)
		fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Prepared(step))
	})
}
