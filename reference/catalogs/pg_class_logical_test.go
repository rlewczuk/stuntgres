package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgClassRelations(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB,
			"CREATE TABLE pc18_rel_heap(id integer, note text, CHECK(id>0))",
			"CREATE INDEX pc18_rel_idx ON pc18_rel_heap(id)",
			"CREATE INDEX pc18_rel_hash ON pc18_rel_heap USING hash(id)",
			"CREATE SEQUENCE pc18_rel_seq", "CREATE UNLOGGED SEQUENCE pc18_rel_useq",
			"CREATE VIEW pc18_rel_view AS SELECT id FROM pc18_rel_heap",
			"CREATE MATERIALIZED VIEW pc18_rel_mv AS SELECT id FROM pc18_rel_heap WITH NO DATA",
			"CREATE TYPE pc18_rel_pair AS (id integer,note text)", "CREATE TABLE pc18_rel_typed OF pc18_rel_pair",
			"CREATE TABLE pc18_rel_child() INHERITS(pc18_rel_heap)",
			"CREATE TABLE pc18_rel_ctas AS SELECT id FROM pc18_rel_heap",
			"SELECT id INTO pc18_rel_into FROM pc18_rel_heap",
			"CREATE TABLE pc18_rel_auto(id integer GENERATED ALWAYS AS IDENTITY, serial_id serial, CONSTRAINT pc18_rel_pk PRIMARY KEY(id))")
		q := "SELECT relname,relkind,relpersistence,relnatts,relchecks,relhasrules,relispopulated,relreplident,relispartition FROM pg_class WHERE " + pgTableViewsNames("relname", []string{"pc18_rel_child", "pc18_rel_ctas", "pc18_rel_hash", "pc18_rel_heap", "pc18_rel_idx", "pc18_rel_into", "pc18_rel_mv", "pc18_rel_pair", "pc18_rel_seq", "pc18_rel_typed", "pc18_rel_useq", "pc18_rel_view"}) + " ORDER BY relname"
		want := [][]any{
			{"pc18_rel_child", "r", "p", int64(2), int64(1), false, true, "d", false},
			{"pc18_rel_ctas", "r", "p", int64(1), int64(0), false, true, "d", false},
			{"pc18_rel_hash", "i", "p", int64(1), int64(0), false, true, "n", false},
			{"pc18_rel_heap", "r", "p", int64(2), int64(1), false, true, "d", false},
			{"pc18_rel_idx", "i", "p", int64(1), int64(0), false, true, "n", false},
			{"pc18_rel_into", "r", "p", int64(1), int64(0), false, true, "d", false},
			{"pc18_rel_mv", "m", "p", int64(1), int64(0), true, false, "d", false},
			{"pc18_rel_pair", "c", "p", int64(2), int64(0), false, true, "n", false},
			{"pc18_rel_seq", "S", "p", int64(3), int64(0), false, true, "n", false},
			{"pc18_rel_typed", "r", "p", int64(2), int64(0), false, true, "d", false},
			{"pc18_rel_useq", "S", "u", int64(3), int64(0), false, true, "n", false},
			{"pc18_rel_view", "v", "p", int64(1), int64(0), true, true, "n", false},
		}
		s := fixture.Select(q, []string{"relname", "relkind", "relpersistence", "relnatts", "relchecks", "relhasrules", "relispopulated", "relreplident", "relispartition"}, []string{"NAME", "CHAR", "CHAR", "INT2", "INT2", "BOOL", "BOOL", "CHAR", "BOOL"}, want)
		pgClassCheck(t, p, b, b.DB, s)
		pgClassCheck(t, p, b, b.DB, fixture.Prepared(s))
		for _, name := range []string{"pc18_rel_heap", "pc18_rel_child", "pc18_rel_ctas", "pc18_rel_into", "pc18_rel_mv", "pc18_rel_pair", "pc18_rel_typed", "pc18_rel_view"} {
			q := "SELECT oid='" + name + "'::regclass::oid AS identity,reltype='" + name + "'::regtype::oid AS rowtype,relnamespace=(SELECT relnamespace FROM pg_class WHERE oid='pc18_rel_heap'::regclass::oid) AS namespace,relowner=(SELECT relowner FROM pg_class WHERE oid='pc18_rel_heap'::regclass::oid) AS owner FROM pg_class WHERE relname='" + name + "'"
			pgClassCheck(t, p, b, b.DB, fixture.Select(q, []string{"identity", "rowtype", "namespace", "owner"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		}
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT reltype<>reloftype AS distinct_types,reloftype='pc18_rel_pair'::regtype::oid AS source FROM pg_class WHERE relname='pc18_rel_typed'", []string{"distinct_types", "source"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relname,reltype,relam FROM pg_class WHERE relname='pc18_rel_hash' OR relname='pc18_rel_idx' OR relname='pc18_rel_seq' ORDER BY relname", []string{"relname", "reltype", "relam"}, []string{"NAME", "OID", "OID"}, [][]any{{"pc18_rel_hash", int64(0), int64(405)}, {"pc18_rel_idx", int64(0), int64(403)}, {"pc18_rel_seq", int64(0), int64(0)}}))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT c.relkind,c.oid=i.indexrelid AS identity FROM pg_class c JOIN pg_index i ON c.oid=i.indexrelid WHERE i.indrelid='pc18_rel_auto'::regclass::oid", []string{"relkind", "identity"}, []string{"CHAR", "BOOL"}, [][]any{{"i", true}}))
		// Automatic sequences are selected by dependency ownership, not allocator offsets.
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT c.relkind FROM pg_class c JOIN pg_depend d ON c.oid=d.objid WHERE d.classid=1259 AND d.refclassid=1259 AND d.refobjid='pc18_rel_auto'::regclass::oid AND c.relkind='S' ORDER BY c.relname", []string{"relkind"}, []string{"CHAR"}, [][]any{{"S"}, {"S"}}))
	})
}

func TestReferencePgClassBuiltins(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, name := range []string{"pg_class", "pg_cast", "pg_tablespace", "pg_shdescription", "pg_views", "pg_tables", "pg_indexes", "pg_matviews", "pg_prepared_xacts", "pg_roles", "pg_group", "pg_user", "pg_sequences", "pg_available_extensions", "table_privileges"} {
			kind, rules, method, replica := "v", true, int64(0), "n"
			if name == "pg_class" || name == "pg_cast" || name == "pg_tablespace" || name == "pg_shdescription" {
				kind, rules, method = "r", false, 2
			}
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relkind,relhasrules,relam,relreplident FROM pg_class WHERE relname='"+name+"'", []string{"relkind", "relhasrules", "relam", "relreplident"}, []string{"CHAR", "BOOL", "OID", "CHAR"}, [][]any{{kind, rules, method, replica}}))
			// Actually open the source: classification must not alter source dispatch.
			schema := "pg_catalog"
			if name == "table_privileges" {
				schema = "information_schema"
			}
			rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM "+schema+"."+name+" WHERE FALSE")
			if err != nil {
				t.Fatal("builtin source", name, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("builtin empty", name, rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relname,relnatts,relisshared,reltablespace FROM pg_class WHERE oid=1259 OR oid=1213 OR oid=2396 ORDER BY relname", []string{"relname", "relnatts", "relisshared", "reltablespace"}, []string{"NAME", "INT2", "BOOL", "OID"}, [][]any{{"pg_class", int64(34), false, int64(0)}, {"pg_shdescription", int64(3), true, int64(1664)}, {"pg_tablespace", int64(5), true, int64(1664)}}))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT reltype='pg_catalog.pg_class'::regtype::oid AS rowtype FROM pg_class WHERE oid=1259", []string{"rowtype"}, []string{"BOOL"}, [][]any{{true}}))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT c.relnamespace,n.nspname='pg_catalog' AS namespace FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=1259", []string{"relnamespace", "namespace"}, []string{"OID", "BOOL"}, [][]any{{int64(11), true}}))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT t.typrelid=c.oid AS reciprocal,n.nspname='pg_catalog' AS namespace FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=1259", []string{"reciprocal", "namespace"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		if !b.PG {
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT oid,relname FROM pg_class WHERE oid=12000 OR oid=12001 OR oid=12002 OR oid=12003 OR oid=12006 ORDER BY oid", []string{"oid", "relname"}, []string{"OID", "NAME"}, [][]any{{int64(12000), "pg_views"}, {int64(12001), "pg_prepared_xacts"}, {int64(12002), "pg_indexes"}, {int64(12003), "pg_tables"}, {int64(12006), "pg_matviews"}}))
		}
	})
}

func TestReferencePgClassFlags(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_flags_heap(id integer,dead text, CHECK(id>0))", "CREATE INDEX pc18_flags_idx ON pc18_flags_heap(id)", "CREATE TABLE pc18_flags_child() INHERITS(pc18_flags_heap)")
		q := "SELECT relhasindex,relhassubclass,relnatts,relchecks,relrowsecurity,relforcerowsecurity FROM pg_class WHERE relname='pc18_flags_heap'"
		held := fixture.ViewHold(t, p, b, q)
		check := func(enabled, forced bool) {
			s := fixture.Select(q, []string{"relhasindex", "relhassubclass", "relnatts", "relchecks", "relrowsecurity", "relforcerowsecurity"}, []string{"BOOL", "BOOL", "INT2", "INT2", "BOOL", "BOOL"}, [][]any{{true, true, int64(2), int64(1), enabled, forced}})
			pgClassCheck(t, p, b, b.DB, s)
			pgClassCheck(t, p, b, held, s)
		}
		check(false, false)
		pgClassExec(t, p, b, b.DB, "DROP INDEX pc18_flags_idx", "DROP TABLE pc18_flags_child", "ALTER TABLE pc18_flags_heap DROP COLUMN dead")
		check(false, false)
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pc18_flags_heap ENABLE ROW LEVEL SECURITY", "ALTER TABLE pc18_flags_heap FORCE ROW LEVEL SECURITY")
		check(true, true)
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pc18_flags_heap DISABLE ROW LEVEL SECURITY")
		check(false, true)
		pgClassExec(t, p, b, b.DB, "CREATE MATERIALIZED VIEW pc18_flags_mv AS SELECT id FROM pc18_flags_heap WITH NO DATA")
		mq := "SELECT relispopulated,relhasrules FROM pg_class WHERE relname='pc18_flags_mv'"
		mh := fixture.ViewHold(t, p, b, mq)
		for _, phase := range []struct {
			sql       string
			populated bool
		}{{"", false}, {"REFRESH MATERIALIZED VIEW pc18_flags_mv WITH DATA", true}, {"REFRESH MATERIALIZED VIEW pc18_flags_mv WITH NO DATA", false}} {
			if phase.sql != "" {
				pgClassExec(t, p, b, b.DB, phase.sql)
			}
			pgClassCheck(t, p, b, mh, fixture.Select(mq, []string{"relispopulated", "relhasrules"}, []string{"BOOL", "BOOL"}, [][]any{{phase.populated, true}}))
		}
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_flags_checks(id integer NOT NULL UNIQUE, CHECK(id>0))", "ALTER TABLE pc18_flags_checks ADD CONSTRAINT pc18_flags_unvalidated CHECK(id<100) NOT VALID")
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relchecks,relhassubclass FROM pg_class WHERE relname='pc18_flags_checks'", []string{"relchecks", "relhassubclass"}, []string{"INT2", "BOOL"}, [][]any{{int64(2), false}}))
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_flags_a(id integer,CONSTRAINT pc18_flags_shared CHECK(id>0))", "CREATE TABLE pc18_flags_b(id integer,CONSTRAINT pc18_flags_shared CHECK(id>0))", "CREATE TABLE pc18_flags_merged() INHERITS(pc18_flags_a,pc18_flags_b)")
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relchecks FROM pg_class WHERE relname='pc18_flags_merged'", []string{"relchecks"}, []string{"INT2"}, [][]any{{int64(1)}}))
		// Failed child publication must not install sticky subclass history.
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc18_flags_root(id integer) PARTITION BY RANGE(id)", "CREATE TABLE pc18_flags_candidate(id integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pc18_flags_candidate VALUES(99)", 1))
		failed := fixture.ImplicitError("ALTER TABLE pc18_flags_root ATTACH PARTITION pc18_flags_candidate FOR VALUES FROM(0) TO(10)", fixture.Diagnostic{Code: "23514", Message: `partition constraint of relation "pc18_flags_candidate" is violated by some row`, Schema: "public", Table: "pc18_flags_candidate"})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, failed)
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relhassubclass FROM pg_class WHERE relname='pc18_flags_root'", []string{"relhassubclass"}, []string{"BOOL"}, [][]any{{false}}))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relispartition,relpartbound IS NULL AS no_bound FROM pg_class WHERE relname='pc18_flags_candidate'", []string{"relispartition", "no_bound"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM pc18_flags_candidate", 1))
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pc18_flags_root ATTACH PARTITION pc18_flags_candidate FOR VALUES FROM(0) TO(10)", "DROP TABLE pc18_flags_candidate")
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relhassubclass FROM pg_class WHERE relname='pc18_flags_root'", []string{"relhassubclass"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
