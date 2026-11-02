package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsInstallation(t *testing.T) {
	t.Run("atomic_installation", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgss_install_user"})
		pgssCase(t, false, func(s *pgssScenario) {
			absent := fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_stat_statements'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			s.both(absent)
			s.run(fixture.ImplicitError("CREATE EXTENSION pg_stat_statements SCHEMA pgss_missing", fixture.Diagnostic{Code: "3F000", Message: `schema "pgss_missing" does not exist`}))
			s.both(absent)
			s.exec("CREATE ROLE pgss_install_user")
			s.exec("GRANT USAGE,CREATE ON SCHEMA pgss_part TO pgss_install_user")
			s.exec("SET ROLE pgss_install_user")
			s.run(fixture.ImplicitError("CREATE EXTENSION pg_stat_statements SCHEMA pgss_part", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "pg_stat_statements"`, Hint: "Must be superuser to create this extension."}))
			s.exec("RESET ROLE")
			s.both(absent)
			s.both(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pgss_part'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.exec("CREATE EXTENSION pg_stat_statements SCHEMA pgss_part VERSION '1.12'")
			s.both(fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname::text AS schema,e.extowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owned,e.oid>0 AS identified FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_stat_statements'", []string{"extversion", "extrelocatable", "schema", "owned", "identified"}, []string{"TEXT", "BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{"1.12", true, "pgss_part", true, true}}))
			s.run(fixture.ImplicitError("CREATE EXTENSION pg_stat_statements", fixture.Diagnostic{Code: "42710", Message: `extension "pg_stat_statements" already exists`}))
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, s.p, s.b.PG, &notices)
			fixture.RunStep(t, s.p.Ctx, conn, record, fixture.Exec("CREATE EXTENSION IF NOT EXISTS pg_stat_statements SCHEMA pgss_missing VERSION 'missing'", 0))
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "pg_stat_statements" already exists, skipping`}
			got := fixture.NoticeWitnesses(t, s.b.PG, notices, want)
			if !reflect.DeepEqual(got, []fixture.Diagnostic{want}) {
				t.Fatalf("installation notices: %#v, want %#v", got, want)
			}
			// PUBLIC view SELECT and function EXECUTE are real authority, not
			// inferred from a superuser's ability to inspect the catalogs.
			s.exec("SET ROLE pgss_install_user")
			for _, q := range []string{"SELECT * FROM pgss_part.pg_stat_statements WHERE false", "SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)"} {
				s.both(fixture.Select(q, pgssNames, pgssTypes, nil))
			}
			s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info WHERE false", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, nil))
			s.exec("RESET ROLE")
			s.exec("DROP EXTENSION pg_stat_statements")
			s.both(absent)
			s.both(fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pgss_part'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.both(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pgss_part'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}
