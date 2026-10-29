package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

type ddlCommandCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	c *sql.Conn
}

func ddlCommandScenario(t *testing.T, body func(ddlCommandCase)) {
	eventTriggerScenario(t, []string{"et_dc_end", "et_dc_disabled"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.WithoutCancel(p.Ctx), "ROLLBACK")
		body(ddlCommandCase{t, p, b, c})
	})
}
func (d ddlCommandCase) run(s fixture.Step) {
	for _, typ := range s.Types {
		if s.SelectRows && typ == "OID" {
			fixture.OidReferenceRows(d.t, d.p.Ctx, d.b, d.c, s)
			return
		}
	}
	fixture.RunStep(d.t, d.p.Ctx, d.c, d.b.Recorder, s)
}
func (d ddlCommandCase) exec(queries ...string) {
	for _, q := range queries {
		d.run(fixture.Exec(q, 0))
	}
}
func (d ddlCommandCase) install(tags string) {
	d.exec("CREATE TABLE et_dc_audit(pass integer,classid oid,objid oid,objsubid integer,command_tag text,object_type text,schema_name text,object_identity text,in_extension boolean,present boolean)",
		"CREATE FUNCTION et_dc_end_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_audit SELECT 1,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); INSERT INTO public.et_dc_audit SELECT 2,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); END$$")
	eventTriggerCreate(d.t, d.p, d.b, d.c, "et_dc_end", "ddl_command_end", tags, "et_dc_end_fn")
}
func (d ddlCommandCase) clear() {
	if _, err := d.c.ExecContext(d.p.Ctx, "DELETE FROM et_dc_audit"); err != nil {
		d.t.Fatal(err)
	}
}
func (d ddlCommandCase) rows(want [][]any) {
	s := fixture.Select("SELECT pass,classid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM et_dc_audit", []string{"pass", "classid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "present"}, []string{"INT4", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, want)
	s.Bag = true
	d.run(s)
	d.run(fixture.Prepared(s))
}
func ddlCommandRows(class, tag, kind string, schema any, identity string) [][]any {
	oid, err := strconv.ParseInt(class, 10, 64)
	if err != nil {
		panic(err)
	}
	return [][]any{{int64(1), oid, int64(0), tag, kind, schema, identity, false, true}, {int64(2), oid, int64(0), tag, kind, schema, identity, false, true}}
}

func TestReferenceEventTriggerDDLCommandsCore(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.install("'CREATE TABLE','ALTER TABLE'")
		d.exec("CREATE TABLE et_dc_target(i integer)")
		d.rows(ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_target"))
		d.clear()
		d.exec("ALTER TABLE et_dc_target ADD COLUMN label text")
		d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_target"))
		d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_class c ON a.objid=c.oid WHERE a.classid='pg_class'::regclass AND c.relname='et_dc_target' AND c.relnamespace='public'::regnamespace", 2))
		d.run(fixture.Exec("INSERT INTO et_dc_target VALUES(7,'ok')", 1))
		d.run(fixture.Select("SELECT i,label FROM et_dc_target", []string{"i", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "ok"}}))
	})
}

func TestReferenceEventTriggerDDLCommandsPrepared(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.install("'CREATE TABLE','ALTER TABLE'")
		q := "CREATE TABLE et_dc_held(i integer)"
		held, err := d.c.PrepareContext(d.p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		d.rows(nil)
		d.run(fixture.Prepared(fixture.Exec("ALTER EVENT TRIGGER et_dc_end DISABLE", 0)))
		if _, err = held.ExecContext(d.p.Ctx); err != nil {
			t.Fatal(err)
		}
		d.rows(nil)
		d.exec("DROP TABLE et_dc_held", "ALTER EVENT TRIGGER et_dc_end ENABLE")
		if _, err = held.ExecContext(d.p.Ctx); err != nil {
			t.Fatal(err)
		}
		d.rows(ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_held"))
		d.clear()
		d.run(fixture.Prepared(fixture.Exec("ALTER TABLE et_dc_held ADD COLUMN label text", 0)))
		d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_held"))
	})
}

func TestReferenceEventTriggerDDLCommandsMultiObject(t *testing.T) {
	for _, serial := range []bool{false, true} {
		t.Run(map[bool]string{false: "primary", true: "serial"}[serial], func(t *testing.T) {
			ddlCommandScenario(t, func(d ddlCommandCase) {
				d.install("'CREATE TABLE','ALTER TABLE','DROP TABLE'")
				column := "integer"
				if serial {
					column = "serial"
				}
				d.exec("CREATE TABLE et_dc_multi(i " + column + " PRIMARY KEY)")
				want := append(ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_multi"), ddlCommandRows("1259", "CREATE INDEX", "index", "public", "public.et_dc_multi_pkey")...)
				if serial {
					want = append(want, ddlCommandRows("1259", "CREATE SEQUENCE", "sequence", "public", "public.et_dc_multi_i_seq")...)
					want = append(want, ddlCommandRows("1259", "ALTER SEQUENCE", "sequence", "public", "public.et_dc_multi_i_seq")...)
				}
				d.rows(want)
				if serial {
					d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN et_dc_audit b ON a.objid=b.objid AND a.pass=b.pass WHERE a.command_tag='CREATE SEQUENCE' AND b.command_tag='ALTER SEQUENCE'", 2))
				}
				d.clear()
				d.exec("ALTER TABLE et_dc_multi ADD COLUMN label text, ADD COLUMN enabled boolean")
				d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_multi"))
				d.clear()
				d.exec("CREATE TABLE IF NOT EXISTS et_dc_multi(i integer)")
				d.rows(nil)
				d.exec("DROP TABLE et_dc_multi")
				d.rows(nil)
			})
		})
	}
}

func TestReferenceEventTriggerDDLCommandsLifecycle(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_existing(i integer)")
		d.install("'CREATE TABLE','ALTER TABLE'")
		d.exec("BEGIN", "CREATE TABLE et_dc_rolled(i integer)", "ROLLBACK")
		d.rows(nil)
		d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_rolled'", 0))
		d.exec("CREATE OR REPLACE FUNCTION et_dc_end_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO et_dc_audit SELECT 1,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); RAISE EXCEPTION 'reject command'; END$$")
		s := fixture.ErrorDiagnostic("ALTER TABLE et_dc_existing ADD COLUMN rejected text", eventTriggerDiagnostic("P0001", "reject command"))
		s.Diagnostic.Context = "PL/pgSQL function et_dc_end_fn() line 1 at RAISE"
		d.run(s)
		d.rows(nil)
		d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='et_dc_existing'::regclass AND attname='rejected'", 0))
		d.exec("CREATE OR REPLACE FUNCTION et_dc_end_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO et_dc_audit SELECT 1,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); IF EXISTS(SELECT 1 FROM pg_event_trigger_ddl_commands() WHERE object_identity='public.et_dc_outer') THEN CREATE TABLE et_dc_inner_kept(i integer); BEGIN CREATE TABLE et_dc_inner_lost(i integer); ALTER TABLE et_dc_inner_lost ADD COLUMN lost text; RAISE EXCEPTION 'caught nested suffix'; EXCEPTION WHEN raise_exception THEN PERFORM command IS NOT NULL FROM pg_event_trigger_ddl_commands(); END; INSERT INTO et_dc_audit SELECT 2,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); END IF; END$$")
		d.exec("CREATE TABLE et_dc_outer(i integer)")
		want := ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_outer")
		want = append(want, []any{int64(1), int64(1259), int64(0), "CREATE TABLE", "table", "public", "public.et_dc_inner_kept", false, true})
		d.rows(want)
		d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_inner_lost'", 0))
	})
}

func TestReferenceEventTriggerDDLCommandsMetadata(t *testing.T) {
	eventTriggerScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		s := fixture.Select("SELECT prorettype,proretset,provolatile,proparallel,procost,prorows FROM pg_proc WHERE oid=4568", []string{"prorettype", "proretset", "provolatile", "proparallel", "procost", "prorows"}, []string{"OID", "BOOL", "CHAR", "CHAR", "FLOAT4", "FLOAT4"}, [][]any{{int64(2249), true, "s", "r", float64(10), float64(100)}})
		fixture.OidReferenceRows(t, p.Ctx, b, c, s)
		fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Prepared(s))
	})
}
