package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgTSConfigMapMissing(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgTSConfigMapDiagnostic(t, p, b, "SELECT missing FROM pg_catalog.pg_ts_config_map", "42703", "column \"missing\" does not exist", "8")
}

func pgTSConfigMapDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, q, code, message, position string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Position: position})
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func TestReferencePGTSConfigMapDiagnostics(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgTSConfigMapMissing(t, p, b)
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT /*é*/ missing FROM pg_catalog.pg_ts_config_map", "42703", "column \"missing\" does not exist", "14"},
			{"SELECT oid FROM pg_catalog.pg_ts_config_map", "42703", "column \"oid\" does not exist", "8"},
			{"SELECT 'bad'::oid FROM pg_catalog.pg_ts_config_map WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_catalog.pg_ts_config_map WHERE FALSE", "22003", "value \"4294967296\" is out of range for type oid", "8"},
			{"SELECT 'bad'::int4 FROM pg_catalog.pg_ts_config_map WHERE FALSE", "22P02", "invalid input syntax for type integer: \"bad\"", "8"},
			{"SELECT '2147483648'::int4 FROM pg_catalog.pg_ts_config_map WHERE FALSE", "22003", "value \"2147483648\" is out of range for type integer", "8"},
			{"SELECT '-2147483649'::int4 FROM pg_catalog.pg_ts_config_map WHERE FALSE", "22003", "value \"-2147483649\" is out of range for type integer", "8"},
		} {
			pgTSConfigMapDiagnostic(t, p, b, tc.q, tc.code, tc.message, tc.position)
		}
		if !b.PG {
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{
					"UPDATE pg_catalog.pg_ts_config_map SET mapdict=3765 WHERE FALSE",
					"DELETE FROM pg_catalog.pg_ts_config_map WHERE FALSE",
					"INSERT INTO pg_catalog.pg_ts_config_map SELECT * FROM pg_catalog.pg_ts_config_map WHERE FALSE",
				} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					pgTSConfigBoth(t, p, b, b.DB, pgTSConfigMapStep(b, pgTSConfigMapProbe, pgTSConfigMapStock(), int64(3748)))
				}
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_ts_config_map FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}
