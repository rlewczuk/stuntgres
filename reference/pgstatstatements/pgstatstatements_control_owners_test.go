package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgssNamedOwnerControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_control_owner"})
	pgssCase(t, false, func(s *pgssScenario) {
		s.exec("CREATE ROLE pgss_control_owner SUPERUSER")
		s.exec("SET SESSION AUTHORIZATION pgss_control_owner")
		defer s.observer.ExecContext(s.p.Ctx, "RESET SESSION AUTHORIZATION")
		s.exec("CREATE EXTENSION pg_stat_statements SCHEMA pgss_part VERSION '1.12'")
		s.exec("CREATE TABLE pgss_part.pgss_owned_data(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_owned_data VALUES(73)", 1))
		s.exec("CREATE VIEW pgss_part.pgss_owned_consumer AS SELECT query,calls FROM pgss_part.pg_stat_statements(false)")
		s.exec("RESET SESSION AUTHORIZATION")
		owner := fixture.Select("SELECT e.extversion,r.rolname::text AS owner,p.proowner=e.extowner AS function_owned,c.relowner=e.extowner AS view_owned FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid AND p.proname='pg_stat_statements' JOIN pg_class c ON c.relnamespace=n.oid AND c.relname='pg_stat_statements' WHERE e.extname='pg_stat_statements'", []string{"extversion", "owner", "function_owned", "view_owned"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"1.12", "pgss_control_owner", true, true}})
		s.both(owner)
		pgssProviderRelationships(s)
		if s.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		connector, err := stuntgres.NewDriver().OpenConnector("pgss_named_owner_destination")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		pool, recorder := fixture.RecordedNativePool(t, connector)
		c, err := pool.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(step fixture.Step) { fixture.RunStep(t, s.p.Ctx, c, recorder, step) }
		run(fixture.Exec("SET pg_stat_statements.track = none", 0))
		// Shift destination allocation and retain a sentinel across rejection;
		// provider replay must remap symbolic owners, never copy source OIDs.
		run(fixture.Exec("CREATE TABLE public.pgss_owner_sentinel(k integer)", 0))
		run(fixture.Exec("INSERT INTO public.pgss_owner_sentinel VALUES(91)", 1))
		err = stuntgres.Restore(s.p.Ctx, raw, bytes.NewReader(dump.Bytes()))
		want := fixture.ControlsReferenceDiagnostic("42704", `role "pgss_control_owner" does not exist`)
		fixture.AssertDiagnostic(t, err, &want)
		run(fixture.Select("SELECT k FROM public.pgss_owner_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(91)}}))
		run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_stat_statements'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE ROLE pgss_control_owner SUPERUSER", 0))
		if err := stuntgres.Restore(s.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(owner)
		run(fixture.Prepared(owner))
		run(fixture.Select("SELECT k FROM pgss_part.pgss_owned_data", []string{"k"}, []string{"INT4"}, [][]any{{int64(73)}}))
		run(fixture.Select("SELECT * FROM pgss_part.pgss_owned_consumer WHERE false", []string{"query", "calls"}, []string{"TEXT", "INT8"}, nil))
		run(fixture.Prepared(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)", pgssNames, pgssTypes, nil)))
		s.both(owner)
	})
}
