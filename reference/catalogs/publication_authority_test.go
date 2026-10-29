package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePublicationPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"publication_rt_owner", "publication_rt_reader", "publication_rt_successor"})
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		defer c.Close()
		run := func(s ...fixture.Step) { publicationRun(t, p, b, c, s...) }
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		run(fixture.Exec("CREATE ROLE publication_rt_owner", 0), fixture.Exec("CREATE ROLE publication_rt_reader", 0), fixture.Exec("CREATE ROLE publication_rt_successor", 0), fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO publication_rt_owner", 0), fixture.Exec("CREATE TABLE publication_rt_foreign(id integer)", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		quotedDatabase := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
		run(fixture.Exec("CREATE PUBLICATION publication_rt_admin", 0), fixture.Exec("SET ROLE publication_rt_owner", 0))
		for _, q := range []string{"CREATE PUBLICATION publication_rt_p", "CREATE PUBLICATION publication_rt_admin WITH(foo=true)", "CREATE PUBLICATION publication_rt_denied FOR ALL TABLES WITH(foo=true)"} {
			run(publicationDiagnostic(q, "42501", "permission denied for database "+database, "", ""))
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT CREATE ON DATABASE "+quotedDatabase+" TO publication_rt_owner", 0), fixture.Exec("SET ROLE publication_rt_owner", 0), fixture.Exec("CREATE TABLE publication_rt_owned(id integer PRIMARY KEY)", 0), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_owned", 0))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_foreign", "42501", "must be owner of table publication_rt_foreign", "", ""))
		run(publicationDiagnostic("CREATE PUBLICATION publication_rt_all FOR ALL TABLES", "42501", "must be superuser to create FOR ALL TABLES publication", "", ""), publicationDiagnostic("CREATE PUBLICATION publication_rt_schema FOR TABLES IN SCHEMA public", "42501", "must be superuser to create FOR TABLES IN SCHEMA publication", "", ""), publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA public", "42501", "must be superuser to add or set schemas", "", ""))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p OWNER TO publication_rt_successor", "42501", `must be able to SET ROLE "publication_rt_successor"`, "", ""), fixture.Exec("RESET ROLE", 0), fixture.Exec("GRANT publication_rt_successor TO publication_rt_owner", 0), fixture.Exec("SET ROLE publication_rt_owner", 0))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p OWNER TO publication_rt_successor", "42501", "permission denied for database "+database, "", ""), fixture.Exec("RESET ROLE", 0), fixture.Exec("GRANT CREATE ON DATABASE "+quotedDatabase+" TO publication_rt_successor", 0))
		q := "ALTER PUBLICATION publication_rt_p SET(publish='insert')"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run(fixture.Exec("SET ROLE publication_rt_reader", 0))
		for _, denied := range []string{q, "ALTER PUBLICATION publication_rt_p RENAME TO publication_rt_other", "ALTER PUBLICATION publication_rt_p OWNER TO publication_rt_reader", "DROP PUBLICATION publication_rt_p"} {
			run(publicationDiagnostic(denied, "42501", "must be owner of publication publication_rt_p", "", ""))
		}
		publicationRun(t, p, b, held, publicationDiagnostic(q, "42501", "must be owner of publication publication_rt_p", "", ""))
		run(publicationOptions("publication_rt_p", false, true, true, true, true, false, "n"), fixture.Exec("RESET ROLE", 0), fixture.Exec("SET ROLE publication_rt_owner", 0))
		publicationRun(t, p, b, held, fixture.Exec(q, 0))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p OWNER TO publication_rt_successor", 0), fixture.Exec(q, 0))
		// Owner still inherits the successor role until current membership is revoked.
		run(fixture.Exec("RESET ROLE", 0), fixture.Exec("REVOKE publication_rt_successor FROM publication_rt_owner", 0), fixture.Exec("SET ROLE publication_rt_owner", 0))
		publicationRun(t, p, b, held, publicationDiagnostic(q, "42501", "must be owner of publication publication_rt_p", "", ""))
		run(fixture.Exec("RESET ROLE", 0), fixture.Exec("SET ROLE publication_rt_successor", 0))
		publicationRun(t, p, b, held, fixture.Exec(q, 0))
		run(fixture.Select("SELECT r.rolname FROM pg_catalog.pg_publication p JOIN pg_catalog.pg_roles r ON r.oid=p.pubowner WHERE p.pubname='publication_rt_p'", []string{"rolname"}, []string{"NAME"}, [][]any{{"publication_rt_successor"}}), fixture.Exec("RESET ROLE", 0))
		// A superuser may transfer an ALL/SCHEMA publication to a non-superuser;
		// that owner cannot transfer it onward to another non-superuser.
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("GRANT publication_rt_successor TO publication_rt_owner", 0))
		for _, tc := range []struct{ name, clause, hint string }{
			{"publication_rt_all", "FOR ALL TABLES", "The owner of a FOR ALL TABLES publication must be a superuser."},
			{"publication_rt_schema", "FOR TABLES IN SCHEMA publication_rt_s", "The owner of a FOR TABLES IN SCHEMA publication must be a superuser."},
		} {
			run(fixture.Exec("CREATE PUBLICATION "+tc.name+" "+tc.clause, 0), fixture.Exec("ALTER PUBLICATION "+tc.name+" OWNER TO publication_rt_owner", 0), fixture.Exec("SET ROLE publication_rt_owner", 0), publicationDiagnostic("ALTER PUBLICATION "+tc.name+" OWNER TO publication_rt_successor", "42501", `permission denied to change owner of publication "`+tc.name+`"`, "", tc.hint), fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("REVOKE CREATE ON DATABASE "+quotedDatabase+" FROM publication_rt_successor", 0), fixture.Exec("SET ROLE publication_rt_successor", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p OWNER TO CURRENT_USER", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p OWNER TO CURRENT_ROLE", 0), fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("DROP PUBLICATION publication_rt_p,publication_rt_all,publication_rt_schema,publication_rt_admin", 0), fixture.Exec("DROP TABLE publication_rt_owned", 0), fixture.Exec("REVOKE CREATE ON DATABASE "+quotedDatabase+" FROM publication_rt_owner", 0))
		run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM publication_rt_owner", 0))
		publicationRoleDependencies(t, p, b)
	})
}

func TestReferencePublicationDependencies(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
		run(fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer,free integer)", 0), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t(id,v) WHERE(v>0)", 0))
		publicationDependencyEdges(t, p, b)
		run(publicationDiagnostic("ALTER TABLE publication_rt_t DROP COLUMN v RESTRICT", "2BP01", "cannot drop column v of table publication_rt_t because other objects depend on it", "publication of table publication_rt_t in publication publication_rt_p depends on column v of table publication_rt_t", "Use DROP ... CASCADE to drop the dependent objects too."))
		run(fixture.Exec("ALTER TABLE publication_rt_t DROP COLUMN free", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,v}", "(v > 0)"}))
		run(publicationDiagnostic("ALTER TABLE publication_rt_t SET UNLOGGED", "55000", `cannot change table "publication_rt_t" to unlogged because it is part of a publication`, "Unlogged relations cannot be replicated.", ""))
		run(publicationDiagnostic("ALTER TABLE publication_rt_t ALTER COLUMN v TYPE bigint", "0A000", "cannot alter type of a column used by a publication WHERE clause", `publication of table publication_rt_t in publication publication_rt_p depends on column "v"`, ""))
		run(fixture.Exec("ALTER TABLE publication_rt_t RENAME TO publication_rt_renamed", 0), fixture.Exec("ALTER TABLE publication_rt_renamed RENAME COLUMN v TO renamed", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_renamed", "{id,renamed}", "(renamed > 0)"}))
		run(fixture.Exec("ALTER TABLE publication_rt_renamed DROP COLUMN renamed CASCADE", 0), publicationMembers("publication_rt_p"), publicationOptions("publication_rt_p", false, true, true, true, true, false, "n"), fixture.Exec("ALTER TABLE publication_rt_renamed SET UNLOGGED", 0), fixture.Exec("ALTER TABLE publication_rt_renamed SET LOGGED", 0))
		run(fixture.Exec("ALTER TABLE publication_rt_renamed ADD COLUMN renamed integer", 0), publicationMembers("publication_rt_p"))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_renamed", 0), fixture.Exec("DROP TABLE publication_rt_renamed", 0), publicationMembers("publication_rt_p"))
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("CREATE TABLE publication_rt_s.t(id integer)", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", 0), publicationNamespace("publication_rt_p", "publication_rt_s"))
		// Schema-only membership does not prevent persistence transitions.
		run(fixture.Exec("ALTER TABLE publication_rt_s.t SET UNLOGGED", 0), publicationMembers("publication_rt_p"), fixture.Exec("ALTER TABLE publication_rt_s.t SET LOGGED", 0), publicationMembers("publication_rt_p", []any{"publication_rt_s", "t"}))
		run(fixture.Exec("DROP TABLE publication_rt_s.t", 0), fixture.Exec("ALTER SCHEMA publication_rt_s RENAME TO publication_rt_other", 0), publicationNamespace("publication_rt_p", "publication_rt_other"), fixture.Exec("DROP SCHEMA publication_rt_other RESTRICT", 0), publicationNamespace("publication_rt_p"), publicationOptions("publication_rt_p", false, true, true, true, true, false, "n"))
		run(fixture.Exec("CREATE TABLE publication_rt_survivor(id integer)", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_survivor", 0), fixture.Exec("DROP PUBLICATION publication_rt_p CASCADE", 0), fixture.Exec("INSERT INTO publication_rt_survivor VALUES(1)", 1))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_all FOR ALL TABLES", 0), publicationMembers("publication_rt_all", []any{"public", "publication_rt_survivor"}), fixture.Exec("ALTER TABLE publication_rt_survivor SET UNLOGGED", 0), publicationMembers("publication_rt_all"), fixture.Exec("ALTER TABLE publication_rt_survivor SET LOGGED", 0), publicationMembers("publication_rt_all", []any{"public", "publication_rt_survivor"}))
	})
}
