package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCoreImplicitTypes(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tr_implicit")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET search_path"); err != nil {
				t.Error(err)
			}
		}()
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA tr_implicit",
			"CREATE DOMAIN tr_implicit.name AS pg_catalog.text",
			"CREATE DOMAIN tr_implicit.text AS pg_catalog.text",
			"CREATE DOMAIN tr_implicit.oid AS pg_catalog.oid",
			"CREATE DOMAIN tr_implicit.int4 AS pg_catalog.int4",
			"SET search_path=tr_implicit,pg_catalog,public",
			"CREATE TABLE tr_implicit.data(id pg_catalog.int4,note pg_catalog.text)")
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tr_implicit.kind(pg_catalog.name) RETURNS pg_catalog.text LANGUAGE SQL AS 'SELECT ''builtin name'''",
			"CREATE FUNCTION tr_implicit.kind(pg_catalog.text) RETURNS pg_catalog.text LANGUAGE SQL AS 'SELECT ''builtin text'''",
			"CREATE FUNCTION tr_implicit.kind(pg_catalog.oid) RETURNS pg_catalog.text LANGUAGE SQL AS 'SELECT ''builtin oid'''",
			"CREATE FUNCTION tr_implicit.kind(pg_catalog.int4) RETURNS pg_catalog.text LANGUAGE SQL AS 'SELECT ''builtin integer'''",
			"CREATE FUNCTION tr_implicit.kind(pg_catalog.text[]) RETURNS pg_catalog.text LANGUAGE SQL AS 'SELECT ''builtin text array'''",
			"CREATE FUNCTION tr_implicit.kind(tr_implicit.name) RETURNS pg_catalog.text LANGUAGE SQL AS 'SELECT ''declared domain'''",
			"CREATE FUNCTION tr_implicit.local_type() RETURNS pg_catalog.text LANGUAGE plpgsql AS $$DECLARE local_slot name := 'domain'; BEGIN RETURN tr_implicit.kind(local_slot); END$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT tr_implicit.local_type() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"declared domain"}}))
		userOperatorExec(t, p, b, c, "DROP FUNCTION tr_implicit.local_type()")
		body := `CREATE FUNCTION tr_implicit.frame() RETURNS pg_catalog.trigger LANGUAGE plpgsql AS $$
DECLARE local_slot tr_implicit.name := 'domain';
BEGIN
 NEW.note := CASE WHEN
 tr_implicit.kind(TG_NAME)='builtin name' AND
 tr_implicit.kind(TG_WHEN)='builtin text' AND
 tr_implicit.kind(TG_LEVEL)='builtin text' AND
 tr_implicit.kind(TG_OP)='builtin text' AND
 tr_implicit.kind(TG_RELID)='builtin oid' AND
 tr_implicit.kind(TG_TABLE_NAME)='builtin name' AND
 tr_implicit.kind(TG_TABLE_SCHEMA)='builtin name' AND
 tr_implicit.kind(TG_RELNAME)='builtin name' AND
 tr_implicit.kind(TG_NARGS)='builtin integer' AND
 tr_implicit.kind(TG_ARGV)='builtin text array' AND
 tr_implicit.kind(local_slot)='declared domain' AND
 TG_NAME='a' AND TG_WHEN='BEFORE' AND TG_LEVEL='ROW' AND TG_OP='INSERT' AND
 TG_RELID='tr_implicit.data'::pg_catalog.regclass::pg_catalog.oid AND
 TG_TABLE_NAME='data' AND TG_TABLE_SCHEMA='tr_implicit' AND TG_RELNAME='data' AND
 TG_NARGS=1 AND TG_ARGV[0]='hello' AND local_slot='domain'
 THEN 'builtin slots; declared domain' ELSE 'wrong frame' END;
 RETURN NEW;
END$$`
		userOperatorExec(t, p, b, c, body, "CREATE TRIGGER a BEFORE INSERT ON tr_implicit.data FOR EACH ROW EXECUTE FUNCTION tr_implicit.frame('hello')")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET search_path=tr_implicit,pg_catalog,public", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("INSERT INTO tr_implicit.data VALUES(1,NULL) RETURNING id,note", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "builtin slots; declared domain"}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("ROLLBACK", 0))
		}
		parametersReplay(t, p, b, c, "tr_implicit", check)
	})
}

func TestReferenceTriggerCoreQuotedTiming(t *testing.T) {
	for _, timing := range []string{"before", "after"} {
		for _, prepared := range []bool{false, true} {
			name := timing + "/direct"
			if prepared {
				name = timing + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TABLE tr_quote(id integer,v integer)", "CREATE FUNCTION tr_quote_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+100; RETURN NEW; END$$")
					q := "CREATE TRIGGER a \"" + timing + "\" INSERT ON tr_quote FOR EACH ROW EXECUTE FUNCTION tr_quote_fn()"
					s := fixture.Error(q, "42601")
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_quote VALUES(1,5) RETURNING id,v", []any{int64(1), int64(5)}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relhastriggers FROM pg_class WHERE oid='tr_quote'::regclass", []string{"relhastriggers"}, []string{"BOOL"}, [][]any{{false}}))
					userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_quote FOR EACH ROW EXECUTE FUNCTION tr_quote_fn()")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_quote VALUES(2,5) RETURNING id,v", []any{int64(2), int64(105)}))
				})
			})
		}
	}
}
