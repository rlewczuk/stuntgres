package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Released-source expectations; pinned-oracle discovery is owned by the root.
func builtinVectorRows(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"id", "vector"}, []string{"INT4", "TEXT"}, rows)
}

func TestReferenceTriggerBuiltinsSuppress(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tb_s(id integer PRIMARY KEY,v integer,n text)",
			"CREATE TRIGGER z BEFORE UPDATE ON tb_s FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger('ignored')")
		run(fixture.Exec("INSERT INTO tb_s VALUES(1,10,NULL),(2,20,'x'),(3,30,NULL)", 3))
		rows := func(q string, want ...[]any) fixture.Step {
			return fixture.Select(q, []string{"id", "v", "n"}, []string{"INT4", "INT4", "TEXT"}, want)
		}
		run(rows("UPDATE tb_s SET v=v RETURNING id,v,n"))
		run(fixture.Prepared(rows("UPDATE tb_s SET n=n RETURNING id,v,n")))
		run(fixture.Exec("UPDATE tb_s SET v=v", 0))
		run(rows("UPDATE tb_s SET v=21 WHERE id=2 RETURNING id,v,n", []any{int64(2), int64(21), "x"}))
		run(fixture.Exec("UPDATE tb_s SET n=NULL WHERE id=1", 0))
		run(fixture.Exec("UPDATE tb_s SET n='now' WHERE id=1", 1))
		run(fixture.Exec("UPDATE tb_s SET n=NULL WHERE id=1", 1))
		mixed := rows("UPDATE tb_s SET v=10 RETURNING id,v,n", []any{int64(2), int64(10), "x"}, []any{int64(3), int64(10), nil})
		mixed.Bag = true
		run(mixed)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tb_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1; RETURN NEW; END$$",
			"CREATE FUNCTION tb_restore() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=OLD.v; RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE UPDATE ON tb_s FOR EACH ROW EXECUTE FUNCTION tb_add()")
		run(rows("UPDATE tb_s SET v=v WHERE id=1 RETURNING id,v,n", []any{int64(1), int64(11), nil}))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER b BEFORE UPDATE ON tb_s FOR EACH ROW EXECUTE FUNCTION tb_restore()")
		run(fixture.Exec("UPDATE tb_s SET v=999", 0))
		run(rows("SELECT id,v,n FROM tb_s ORDER BY id", []any{int64(1), int64(11), nil}, []any{int64(2), int64(10), "x"}, []any{int64(3), int64(10), nil}))
	})
}

func TestReferenceTriggerBuiltinsVectorSources(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_v(id integer PRIMARY KEY,title text,body text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_v FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title','body')")
		run(builtinVectorRows("INSERT INTO tb_v VALUES(1,'The Fat','Rats rats','supplied') RETURNING id,v::text AS vector", []any{int64(1), "'fat':2 'rats':3,4 'the':1"}))
		run(fixture.Prepared(builtinVectorRows("INSERT INTO tb_v VALUES(2,'foo','bar',NULL) RETURNING id,v::text AS vector", []any{int64(2), "'bar':2 'foo':1"})))
		run(builtinVectorRows("INSERT INTO tb_v VALUES(3,NULL,NULL,'supplied') RETURNING id,v::text AS vector", []any{int64(3), ""}))
		userOperatorExec(t, p, b, c, "SET default_text_search_config='pg_catalog.english'",
			"CREATE OR REPLACE TRIGGER a BEFORE INSERT OR UPDATE ON tb_v FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','body','title')")
		run(builtinVectorRows("UPDATE tb_v SET title=title WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'fat':4 'rats':1,2 'the':3"}))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT OR UPDATE ON tb_v FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title','body','title')")
		run(builtinVectorRows("UPDATE tb_v SET title=title WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'fat':2,6 'rats':3,4 'the':1,5"}))
		userOperatorExec(t, p, b, c,
			"CREATE TEXT SEARCH DICTIONARY public.tb_stops (TEMPLATE=pg_catalog.simple,STOPWORDS=english)",
			"CREATE TEXT SEARCH CONFIGURATION public.tb_stop_config (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION public.tb_stop_config ALTER MAPPING FOR asciiword WITH public.tb_stops",
			"CREATE OR REPLACE TRIGGER a BEFORE INSERT OR UPDATE ON tb_v FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','public.tb_stop_config','title','body')")
		run(builtinVectorRows("INSERT INTO tb_v VALUES(4,'cat the','dog',NULL) RETURNING id,v::text AS vector", []any{int64(4), "'cat':1 'dog':3"}))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT OR UPDATE ON tb_v FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','public.tb_stop_config','body','title')")
		run(builtinVectorRows("UPDATE tb_v SET body=body WHERE id=4 RETURNING id,v::text AS vector", []any{int64(4), "'cat':2 'dog':1"}))
	})
}

func TestReferenceTriggerBuiltinsRowConfig(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c,
			"CREATE TEXT SEARCH DICTIONARY public.tb_reject (TEMPLATE=pg_catalog.simple,Accept=false)",
			"CREATE TEXT SEARCH CONFIGURATION public.tb_live (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION public.tb_live ALTER MAPPING FOR asciiword WITH public.tb_reject",
			"CREATE TABLE tb_r(id integer PRIMARY KEY,cfg regconfig,title text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_r FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title')")
		q := "INSERT INTO tb_r VALUES($1,'public.tb_live','The Fat Rats',NULL) RETURNING id,v::text AS vector"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := builtinVectorRows(q, []any{int64(1), ""})
		s.Args = []any{1}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		userOperatorExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION public.tb_live ALTER MAPPING FOR asciiword WITH pg_catalog.simple")
		s = builtinVectorRows(q, []any{int64(2), "'fat':2 'rats':3 'the':1"})
		s.Args = []any{2}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(builtinVectorRows("UPDATE tb_r SET cfg='pg_catalog.simple' WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), ""}))
		run(builtinVectorRows("UPDATE tb_r SET title=title WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'fat':2 'rats':3 'the':1"}))
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tb_config_first() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.cfg:='public.tb_live'::regconfig; RETURN NEW; END$$",
			"CREATE TRIGGER aa BEFORE UPDATE ON tb_r FOR EACH ROW EXECUTE FUNCTION tb_config_first()",
			"ALTER TEXT SEARCH CONFIGURATION public.tb_live ALTER MAPPING FOR asciiword WITH public.tb_reject")
		// a runs before aa: rename gives the configuration modifier precedence.
		userOperatorExec(t, p, b, c, "ALTER TRIGGER aa ON tb_r RENAME TO a0", "ALTER TRIGGER a ON tb_r RENAME TO z")
		run(builtinVectorRows("UPDATE tb_r SET title=title WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), ""}))
	})
}

func TestReferenceTriggerBuiltinsUpdateTargets(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_u(id integer PRIMARY KEY,title text,other integer,v tsvector)",
			"CREATE TRIGGER z BEFORE INSERT OR UPDATE ON tb_u FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title')")
		run(fixture.Exec("INSERT INTO tb_u VALUES(1,'Rats',0,NULL)", 1))
		run(builtinVectorRows("UPDATE tb_u SET v='manual:9' WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'manual':9"}))
		run(builtinVectorRows("UPDATE tb_u SET other=1 WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'manual':9"}))
		run(fixture.Prepared(builtinVectorRows("UPDATE tb_u SET title=title WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'rats':1"})))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION tb_source_first() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.title:='Cats'; RETURN NEW; END$$", "CREATE TRIGGER a BEFORE UPDATE ON tb_u FOR EACH ROW EXECUTE FUNCTION tb_source_first()")
		run(builtinVectorRows("UPDATE tb_u SET other=2 WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'rats':1"}))
		run(builtinVectorRows("UPDATE tb_u SET title=title WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'cats':1"}))
		userOperatorExec(t, p, b, c, "DROP TRIGGER a ON tb_u", "ALTER TABLE tb_u RENAME COLUMN title TO renamed")
		run(fixture.ErrorDiagnostic("UPDATE tb_u SET other=3 WHERE id=1", fixture.ControlsReferenceDiagnostic("42703", `column "title" does not exist`)))
		run(fixture.Select("SELECT other,renamed,v::text AS vector FROM tb_u", []string{"other", "renamed", "vector"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "Cats", "'cats':1"}}))
	})
}
