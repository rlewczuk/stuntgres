package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedAlterAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_owner", "alter_next", "alter_reader", "CURRENT_USER"})
	fixture.SchemaReferenceCleanup(t, "alter_owned")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, role := range []string{"alter_owner", "alter_next", "alter_reader", `"CURRENT_USER"`} {
				run(fixture.Exec("CREATE USER "+role, 0))
			}
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
			}
			alterSeed(run)
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv OWNER TO alter_owner", 0))
			run(fixture.Exec("GRANT SELECT(i) ON alter_mv TO alter_reader", 0))
			run(fixture.Exec("GRANT MAINTAIN ON alter_mv TO alter_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_reader", 0))
			for _, q := range alterForms("alter_mv") {
				alterError(t, p, c, q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view alter_mv"), "execute")
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_owner", 0))
			alterError(t, p, c, "ALTER MATERIALIZED VIEW alter_mv OWNER TO alter_next", fixture.ControlsReferenceDiagnostic("42501", `must be able to SET ROLE "alter_next"`), "execute")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT alter_next TO alter_owner", 0))
			run(fixture.Exec("CREATE SCHEMA alter_owned AUTHORIZATION alter_owner", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv SET SCHEMA alter_owned", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_owner", 0))
			alterError(t, p, c, "ALTER MATERIALIZED VIEW alter_owned.alter_mv OWNER TO alter_next", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema alter_owned"), "execute")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("ALTER SCHEMA alter_owned OWNER TO alter_next", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_owner", 0))
			run(fixture.Prepared(fixture.Exec("ALTER MATERIALIZED VIEW alter_owned.alter_mv OWNER TO alter_next", 0)))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE alter_next FROM alter_owner", 0))
			// Keep the column ACL witness in public; schema GRANT remains a PG-only setup.
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_owned.alter_mv SET SCHEMA public", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_reader", 0))
			run(fixture.Select("SELECT key FROM alter_mv ORDER BY key", []string{"key"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_owner", 0))
			alterError(t, p, c, "ALTER MATERIALIZED VIEW alter_mv RENAME TO saved", fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view alter_mv"), "execute")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec(`ALTER MATERIALIZED VIEW alter_mv OWNER TO "CURRENT_USER"`, 0))
			run(fixture.Select("SELECT matviewowner FROM pg_matviews WHERE matviewname='alter_mv'", []string{"matviewowner"}, []string{"NAME"}, [][]any{{"CURRENT_USER"}}))
		})
	}
}
func TestReferenceMaterializedAlterOwnerContext(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_new_owner", "alter_maintainer"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Exec("CREATE USER alter_new_owner", 0))
			run(fixture.Exec("CREATE USER alter_maintainer", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(fixture.Exec("CREATE TABLE owner_source(i INT)", 0))
			run(fixture.Exec("INSERT INTO owner_source VALUES(1)", 1))
			run(fixture.Exec("CREATE SEQUENCE owner_seq", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW owner_mv AS SELECT i,CURRENT_USER AS who,nextval('owner_seq'::regclass) AS n FROM owner_source", 1))
			run(fixture.Exec("GRANT SELECT ON owner_source TO alter_new_owner", 0))
			run(fixture.Exec("ALTER SEQUENCE owner_seq OWNER TO alter_new_owner", 0))
			run(fixture.Prepared(fixture.Exec("ALTER MATERIALIZED VIEW owner_mv OWNER TO alter_new_owner", 0)))
			run(fixture.Exec("GRANT MAINTAIN ON owner_mv TO alter_maintainer", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_maintainer", 0))
			run(fixture.Prepared(fixture.Exec("REFRESH MATERIALIZED VIEW owner_mv", 0)))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT i,who,n FROM owner_mv", []string{"i", "who", "n"}, []string{"INT4", "NAME", "INT8"}, [][]any{{int64(1), "alter_new_owner", int64(2)}}))
			run(fixture.Exec("REVOKE SELECT ON owner_source FROM alter_new_owner", 0))
			alterError(t, p, c, "REFRESH MATERIALIZED VIEW owner_mv", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table owner_source"), "execute")
			run(fixture.Select("SELECT i,who,n FROM owner_mv", []string{"i", "who", "n"}, []string{"INT4", "NAME", "INT8"}, [][]any{{int64(1), "alter_new_owner", int64(2)}}))
			run(fixture.Exec("GRANT SELECT ON owner_source TO alter_new_owner", 0))
			run(fixture.Exec("ALTER SEQUENCE owner_seq OWNER TO alter_maintainer", 0))
			alterError(t, p, c, "REFRESH MATERIALIZED VIEW owner_mv", fixture.ControlsReferenceDiagnostic("42501", "permission denied for sequence owner_seq"), "execute")
		})
	}
}
