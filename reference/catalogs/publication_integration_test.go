package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func publicationExplicit(name string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT n.nspname,c.relname FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid JOIN pg_catalog.pg_class c ON c.oid=r.prrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE p.pubname=$1 ORDER BY n.nspname,c.relname", []string{"nspname", "relname"}, []string{"NAME", "NAME"}, rows, name)
}

func publicationHeldIdentity(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		defer c.Close()
		run := func(s ...fixture.Step) { publicationRun(t, p, b, c, s...) }
		run(fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer NOT NULL)", 0), fixture.Exec("CREATE UNIQUE INDEX publication_rt_idx ON publication_rt_t(v)", 0), fixture.Exec("INSERT INTO publication_rt_t VALUES(1,10)", 1))
		q := "UPDATE publication_rt_t SET v=v WHERE false"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		execute := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
		execute(fixture.Exec(q, 0))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t WHERE(v>0)", 0))
		execute(publicationIdentityError(q, "update", "publication_rt_t", "filter"))
		run(fixture.Exec("ALTER TABLE publication_rt_t REPLICA IDENTITY USING INDEX publication_rt_idx", 0))
		execute(fixture.Exec(q, 0))
		run(fixture.Exec("DROP INDEX publication_rt_idx", 0))
		execute(publicationIdentityError(q, "update", "publication_rt_t", "filter"))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t WHERE(true)", 0))
		execute(publicationIdentityError(q, "update", "publication_rt_t", "missing"))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='insert')", 0))
		execute(fixture.Exec(q, 0))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='update')", 0), fixture.Exec("ALTER TABLE publication_rt_t REPLICA IDENTITY FULL", 0))
		execute(fixture.Exec(q, 0))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t(id,v)", 0))
		execute(publicationIdentityError(q, "update", "publication_rt_t", "columns"))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_t", 0))
		execute(fixture.Exec(q, 0))
		run(fixture.Select("SELECT id,v FROM publication_rt_t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
	})
}

func publicationRoleDependencies(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
	run(fixture.Exec("CREATE TABLE publication_rt_t(id integer)", 0), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p OWNER TO publication_rt_owner", 0))
	run(publicationDiagnostic("DROP ROLE publication_rt_owner", "2BP01", "role \"publication_rt_owner\" cannot be dropped because some objects depend on it", "owner of publication publication_rt_p", ""))
	run(fixture.Exec("REASSIGN OWNED BY publication_rt_owner TO publication_rt_successor", 0), fixture.Select("SELECT r.rolname FROM pg_catalog.pg_publication p JOIN pg_catalog.pg_roles r ON r.oid=p.pubowner WHERE p.pubname='publication_rt_p'", []string{"rolname"}, []string{"NAME"}, [][]any{{"publication_rt_successor"}}), publicationExplicit("publication_rt_p", []any{"public", "publication_rt_t"}))
	run(fixture.Exec("DROP OWNED BY publication_rt_successor", 0), publicationExplicit("publication_rt_p"), fixture.Select("SELECT pubname FROM pg_catalog.pg_publication WHERE pubname='publication_rt_p'", []string{"pubname"}, []string{"NAME"}, nil), fixture.Exec("INSERT INTO publication_rt_t VALUES(1)", 1))
}

func publicationDependencyEdges(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	q := "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_publication_rel r ON d.objid=r.oid JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid WHERE p.pubname='publication_rt_p' AND d.classid='pg_catalog.pg_publication_rel'::regclass AND d.refclassid='pg_catalog.pg_publication'::regclass AND d.refobjid=p.oid AND d.deptype='a') AS publication_edge,EXISTS(SELECT 1 FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_publication_rel r ON d.objid=r.oid JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid WHERE p.pubname='publication_rt_p' AND d.classid='pg_catalog.pg_publication_rel'::regclass AND d.refclassid='pg_catalog.pg_class'::regclass AND d.refobjid=r.prrelid AND d.refobjsubid=0 AND d.deptype='a') AS table_edge,EXISTS(SELECT 1 FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_publication_rel r ON d.objid=r.oid JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid JOIN pg_catalog.pg_attribute a ON a.attrelid=r.prrelid AND a.attname='v' WHERE p.pubname='publication_rt_p' AND d.classid='pg_catalog.pg_publication_rel'::regclass AND d.refclassid='pg_catalog.pg_class'::regclass AND d.refobjid=r.prrelid AND d.refobjsubid=a.attnum AND d.deptype='n') AS column_edge"
	publicationRun(t, p, b, b.DB, fixture.Select(q, []string{"publication_edge", "table_edge", "column_edge"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
}
