package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"eo_installer", "eo_reader", "eo_member"})
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		// Run before the borrowed connection is released, even after Fatal.
		// RoleReferenceCleanup alone cannot revoke a database CREATE grant.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, q := range []string{
				"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION",
				"DROP EXTENSION IF EXISTS unaccent CASCADE",
				"DROP SCHEMA IF EXISTS eo_old CASCADE",
				"DROP SCHEMA IF EXISTS eo_new CASCADE",
				"DROP SCHEMA IF EXISTS eo_other CASCADE",
			} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("authority cleanup", q, err)
				}
			}
			for _, role := range []string{"eo_installer", "eo_reader", "eo_member"} {
				var exists bool
				if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
					t.Error("authority cleanup role lookup", role, err)
					continue
				}
				if exists {
					if _, err := c.ExecContext(ctx, "DROP OWNED BY "+role); err != nil {
						t.Error("authority cleanup dependencies", role, err)
					}
				}
			}
		}()
		for _, q := range []string{
			"CREATE ROLE eo_installer", "CREATE ROLE eo_reader", "CREATE ROLE eo_member INHERIT",
			"CREATE SCHEMA eo_old", "CREATE SCHEMA eo_new", "CREATE SCHEMA eo_other",
			"GRANT USAGE,CREATE ON SCHEMA eo_old,eo_new,eo_other TO eo_installer",
		} {
			run(fixture.Exec(q, 0))
		}
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("GRANT CREATE ON DATABASE "+fixture.ExtensionTempIdentifier(database)+" TO eo_installer", 0))
		run(fixture.Exec("SET ROLE eo_installer", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA eo_old", 0))
		run(ownershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_old'::regnamespace AND proowner<>(SELECT extowner FROM pg_extension WHERE extname='unaccent')`, 4))
		ownershipMemberCount(run, 6)
		// Same-target succeeds before lower object movers. A real move reaches
		// generic function-owner checks despite the misleading extension.c comment.
		run(fixture.Exec("ALTER EXTENSION unaccent SET SCHEMA eo_old", 0))
		queries := []string{
			`SELECT oid::bigint,extowner::bigint,extnamespace::bigint,extversion FROM pg_extension WHERE extname='unaccent'`,
			`SELECT oid::bigint,pronamespace::bigint,proowner::bigint,proname,proargtypes::text,proacl::text FROM pg_proc WHERE pronamespace='eo_old'::regnamespace ORDER BY oid`,
			`SELECT oid::bigint,dictnamespace::bigint,dictowner::bigint,dictname,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictnamespace='eo_old'::regnamespace ORDER BY oid`,
			`SELECT oid::bigint,tmplnamespace::bigint,tmplname,tmplinit::bigint,tmpllexize::bigint FROM pg_ts_template WHERE tmplnamespace='eo_old'::regnamespace ORDER BY oid`,
			ownershipEdges,
			`SELECT refclassid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE classid='pg_extension'::regclass AND objid=(SELECT oid FROM pg_extension WHERE extname='unaccent') ORDER BY refclassid,refobjid,deptype`,
		}
		before := make([][][]any, len(queries))
		for i, q := range queries {
			before[i] = ownershipSnapshot(t, p, c, q)
		}
		run(ownershipError("ALTER EXTENSION unaccent SET SCHEMA eo_new", "42501", "must be owner of function unaccent"))
		for i, q := range queries {
			ownershipUnchanged(t, p, c, q, before[i])
		}
		ownershipCallable(run, "eo_old")
		run(ownershipError("ALTER EXTENSION unaccent DROP FUNCTION eo_old.unaccent(text)", "42501", "must be owner of function eo_old.unaccent"))
		run(ownershipError("ALTER EXTENSION unaccent ADD FUNCTION eo_old.unaccent(text)", "42501", "must be owner of function eo_old.unaccent"))
		run(fixture.Exec("CREATE FUNCTION eo_new.owned(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", 0))
		run(fixture.Exec("ALTER EXTENSION unaccent ADD FUNCTION eo_new.owned(integer)", 0))
		run(fixture.Exec("ALTER EXTENSION unaccent DROP FUNCTION eo_new.owned(integer)", 0))
		run(fixture.Exec("RESET ROLE", 0))
		// Actual superuser authority moves both owned functions and the
		// ownerless template; neither provider nor extension ownership is faked.
		run(fixture.Exec("ALTER EXTENSION unaccent SET SCHEMA eo_new", 0))
		ownershipCallable(run, "eo_new")
		run(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent' AND extnamespace='eo_new'::regnamespace AND extowner=(SELECT oid FROM pg_roles WHERE rolname='eo_installer')`, 1))
		run(ownershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_new'::regnamespace AND proname IN ('unaccent','unaccent_init','unaccent_lexize') AND proowner<>(SELECT extowner FROM pg_extension WHERE extname='unaccent')`, 4))
		run(fixture.Exec("ALTER FUNCTION eo_new.owned(integer) OWNER TO eo_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA eo_new TO eo_reader", 0))
		run(fixture.Exec("SET ROLE eo_reader", 0))
		run(ownershipError("ALTER EXTENSION unaccent ADD FUNCTION eo_new.owned(integer)", "42501", "must be owner of extension unaccent"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE eo_installer", 0))
		run(ownershipError("ALTER EXTENSION unaccent ADD FUNCTION eo_new.owned(integer)", "42501", "must be owner of function eo_new.owned"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE CREATE ON SCHEMA eo_other,eo_new FROM eo_installer", 0))
		run(fixture.Exec("SET ROLE eo_installer", 0))
		run(ownershipError("ALTER EXTENSION unaccent SET SCHEMA eo_other", "42501", "permission denied for schema eo_other"))
		run(ownershipError("ALTER EXTENSION unaccent SET SCHEMA eo_new", "42501", "permission denied for schema eo_new"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT CREATE ON SCHEMA eo_new TO eo_installer", 0))
		run(fixture.Exec("GRANT eo_installer TO eo_member", 0))
		run(fixture.Exec("SET ROLE eo_member", 0))
		run(fixture.Exec("ALTER EXTENSION unaccent SET SCHEMA eo_new", 0))
		run(ownershipError("ALTER EXTENSION unaccent SET SCHEMA eo_old", "42501", "must be owner of function unaccent"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE eo_installer FROM eo_member", 0))
		run(fixture.Exec("GRANT CREATE ON SCHEMA eo_new TO eo_member", 0))
		run(fixture.Exec("SET ROLE eo_member", 0))
		run(ownershipError("ALTER EXTENSION unaccent SET SCHEMA eo_new", "42501", "must be owner of extension unaccent"))
		run(fixture.Exec("RESET ROLE", 0))
		// Transfer genuine owned objects through their public ALTER commands.
		// Owning every function/dictionary still cannot grant ownership of an
		// ownerless template. The late veto must roll back all earlier moves.
		for _, target := range []string{
			"FUNCTION eo_new.unaccent(text)", "FUNCTION eo_new.unaccent(regdictionary,text)",
			"FUNCTION eo_new.unaccent_init(internal)",
			"FUNCTION eo_new.unaccent_lexize(internal,internal,internal,internal)",
			"TEXT SEARCH DICTIONARY eo_new.unaccent",
		} {
			run(fixture.Exec("ALTER "+target+" OWNER TO eo_installer", 0))
		}
		currentQueries := make([]string, len(queries))
		for i, q := range queries {
			currentQueries[i] = strings.ReplaceAll(q, "eo_old", "eo_new")
			before[i] = ownershipSnapshot(t, p, c, currentQueries[i])
		}
		run(fixture.Exec("SET ROLE eo_installer", 0))
		run(ownershipError("ALTER EXTENSION unaccent SET SCHEMA eo_old", "42501", "must be superuser to set schema of text search template eo_new.unaccent"))
		run(fixture.Exec("RESET ROLE", 0))
		for i, q := range currentQueries {
			ownershipUnchanged(t, p, c, q, before[i])
		}
		ownershipCallable(run, "eo_new")
		ownershipMemberCount(run, 6)
	})
}
