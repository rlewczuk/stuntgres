package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func deferredSavedDeepSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE SCHEMA dm",
		"CREATE SCHEMA dl",
		"CREATE TABLE ds.deep_root(id integer) PARTITION BY RANGE(id)",
		"CREATE TABLE dm.deep_mid PARTITION OF ds.deep_root FOR VALUES FROM(0) TO(100) PARTITION BY RANGE(id)",
		"CREATE TABLE dl.deep_leaf PARTITION OF dm.deep_mid FOR VALUES FROM(0) TO(100)",
		"CREATE TABLE ds.deep_log(id integer,schema_name text,table_name text,label text)",
		"CREATE FUNCTION ds.deep_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds.deep_log VALUES(NEW.id,TG_TABLE_SCHEMA,TG_TABLE_NAME,TG_NAME); RETURN NULL; END$$",
		"CREATE CONSTRAINT TRIGGER deep_c AFTER INSERT ON ds.deep_root DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ds.deep_event()",
		"ALTER TRIGGER deep_c ON ds.deep_root RENAME TO deep_live",
		"ALTER TABLE ds.deep_root RENAME CONSTRAINT deep_c TO root_deep",
		"ALTER TABLE dm.deep_mid RENAME CONSTRAINT deep_c TO mid_deep",
		"ALTER TABLE dl.deep_leaf RENAME CONSTRAINT deep_c TO leaf_deep")
}

func deferredSavedDeepCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, deferredOIDCarriers(b, s)) }
	// Joining each actual tgparentid distinguishes both partition edges from
	// flattened ancestry; constraint ancestry remains independently zero/local.
	run(fixture.Select("SELECT n.nspname,r.relname,t.tgname,c.conname,pn.nspname AS parent_schema,pr.relname AS parent_table,t.tgconstraint=c.oid AND t.oid<>c.oid AND t.oid>0 AND c.oid>0 AS linked,c.connamespace=r.relnamespace AS own_schema,c.conparentid,c.conislocal,c.coninhcount,c.connoinherit,t.tgdeferrable,t.tginitdeferred,c.condeferrable,c.condeferred FROM pg_trigger t JOIN pg_class r ON r.oid=t.tgrelid JOIN pg_namespace n ON n.oid=r.relnamespace JOIN pg_constraint c ON c.oid=t.tgconstraint LEFT JOIN pg_trigger pt ON pt.oid=t.tgparentid LEFT JOIN pg_class pr ON pr.oid=pt.tgrelid LEFT JOIN pg_namespace pn ON pn.oid=pr.relnamespace WHERE (n.nspname='ds' AND r.relname='deep_root') OR (n.nspname='dm' AND r.relname='deep_mid') OR (n.nspname='dl' AND r.relname='deep_leaf') ORDER BY n.nspname",
		[]string{"nspname", "relname", "tgname", "conname", "parent_schema", "parent_table", "linked", "own_schema", "conparentid", "conislocal", "coninhcount", "connoinherit", "tgdeferrable", "tginitdeferred", "condeferrable", "condeferred"},
		[]string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "OID", "BOOL", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
			{"dl", "deep_leaf", "deep_live", "leaf_deep", "dm", "deep_mid", true, true, "0", true, int64(0), true, true, true, true, true},
			{"dm", "deep_mid", "deep_live", "mid_deep", "ds", "deep_root", true, true, "0", true, int64(0), true, true, true, true, true},
			{"ds", "deep_root", "deep_live", "root_deep", nil, nil, true, true, "0", true, int64(0), true, true, true, true, true},
		}))
	run(fixture.Select("SELECT count(DISTINCT t.oid) AS triggers,count(DISTINCT c.oid) AS constraints FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid IN('ds.deep_root'::regclass,'dm.deep_mid'::regclass,'dl.deep_leaf'::regclass)", []string{"triggers", "constraints"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))
	run(deferredCount("ds.deep_root", 0))
	run(deferredCount("ds.deep_log", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO ds.deep_root VALUES($1),($2)", 2, 1, 2)))
	run(deferredCount("ds.deep_log", 0))
	run(fixture.Prepared(fixture.Exec("SET CONSTRAINTS ds.root_deep IMMEDIATE", 0)))
	run(deferredCount("ds.deep_log", 0))
	run(fixture.Exec("SET CONSTRAINTS dm.mid_deep IMMEDIATE", 0))
	run(deferredCount("ds.deep_log", 0))
	run(fixture.Prepared(fixture.Exec("SET CONSTRAINTS dl.leaf_deep IMMEDIATE", 0)))
	run(fixture.Select("SELECT id,schema_name,table_name,label FROM ds.deep_log ORDER BY id", []string{"id", "schema_name", "table_name", "label"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "dl", "deep_leaf", "deep_live"}, {int64(2), "dl", "deep_leaf", "deep_live"}}))
	run(fixture.Exec("INSERT INTO dl.deep_leaf VALUES(3)", 1))
	run(fixture.Select("SELECT id,schema_name,table_name,label FROM ds.deep_log ORDER BY id", []string{"id", "schema_name", "table_name", "label"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "dl", "deep_leaf", "deep_live"}, {int64(2), "dl", "deep_leaf", "deep_live"}, {int64(3), "dl", "deep_leaf", "deep_live"}}))
	run(fixture.Exec("ROLLBACK", 0))
	run(deferredCount("ds.deep_root", 0))
	run(deferredCount("ds.deep_log", 0))
}
