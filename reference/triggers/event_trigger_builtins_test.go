package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var builtinTriggerNames = []string{"et_do_drop", "et_do_end", "et_bi_rw"}

func builtinUtility(d droppedCase, q string, prepared bool, quiet func()) {
	d.t.Helper()
	if !prepared {
		d.run(fixture.Exec(q, 0))
		return
	}
	stmt, err := d.c.PrepareContext(d.p.Ctx, q)
	if err != nil {
		d.t.Fatal("utility Prepare", err)
	}
	defer stmt.Close()
	quiet()
	fixture.RunStep(d.t, d.p.Ctx, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: q}, d.b.Recorder, fixture.Exec(q, 0))
}

func builtinCoexistSetup(d droppedCase) {
	d.t.Helper()
	d.setup()
	d.exec(
		"CREATE TABLE public.et_bi_ddl(pass integer,classid oid,objid oid,objsubid integer,command_tag text,object_type text,schema_name text,object_identity text,in_extension boolean,present boolean)",
		"CREATE TABLE public.et_bi_rewrite(target oid,reason integer,same_oid boolean,same_reason boolean)",
		"CREATE TABLE public.et_bi_phase(seq bigint,phase text,tag text)",
		"CREATE FUNCTION public.et_bi_phase_read(ev text,command text) RETURNS integer LANGUAGE SQL AS $$INSERT INTO public.et_bi_phase SELECT count(*)+1,ev,command FROM public.et_bi_phase; SELECT 1$$",
		"CREATE OR REPLACE FUNCTION public.et_do_drop_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_do_read(1) INTO n; SELECT public.et_do_read(2) INTO n; INSERT INTO public.et_do_phase SELECT 'drop',count(*),0 FROM public.et_do_audit WHERE pass=1; SELECT public.et_bi_phase_read('drop',TG_TAG) INTO n; END$$",
		"CREATE OR REPLACE FUNCTION public.et_do_end_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN INSERT INTO public.et_bi_ddl SELECT 1,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); INSERT INTO public.et_bi_ddl SELECT 2,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); INSERT INTO public.et_do_phase SELECT 'end',count(*),0 FROM public.et_do_audit WHERE pass=1; SELECT public.et_bi_phase_read('end',TG_TAG) INTO n; END$$",
		"CREATE FUNCTION public.et_bi_rw_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN INSERT INTO public.et_bi_rewrite SELECT pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason(),pg_event_trigger_table_rewrite_oid()=pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason()=pg_event_trigger_table_rewrite_reason(); SELECT public.et_bi_phase_read('rewrite',TG_TAG) INTO n; END$$",
	)
	d.install("'CREATE TABLE','ALTER TABLE','DROP TABLE'")
	eventTriggerCreate(d.t, d.p, d.b, d.c, "et_bi_rw", "table_rewrite", "'ALTER TABLE'", "public.et_bi_rw_fn")
}

func builtinDDLBag(d droppedCase, id int64, tags ...string) {
	d.t.Helper()
	var rows [][]any
	for _, tag := range tags {
		for _, pass := range []int64{1, 2} {
			rows = append(rows, []any{pass, int64(1259), id, int64(0), tag, "table", "public", "public.et_bi_target", false, true})
		}
	}
	s := fixture.Select("SELECT pass,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM public.et_bi_ddl",
		[]string{"pass", "classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "present"},
		[]string{"INT4", "OID", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, rows)
	s.Bag = true
	fixture.OidReferenceRows(d.t, d.p.Ctx, d.b, d.c, s)
	fixture.OidReferenceRows(d.t, d.p.Ctx, d.b, d.c, fixture.Prepared(s))
}

func builtinRewriteBag(d droppedCase, id int64) {
	d.t.Helper()
	s := fixture.Select("SELECT target,reason,same_oid,same_reason FROM public.et_bi_rewrite", []string{"target", "reason", "same_oid", "same_reason"}, []string{"OID", "INT4", "BOOL", "BOOL"}, [][]any{{id, int64(4), true, true}})
	fixture.OidReferenceRows(d.t, d.p.Ctx, d.b, d.c, s)
	fixture.OidReferenceRows(d.t, d.p.Ctx, d.b, d.c, fixture.Prepared(s))
}

func builtinCoexistOutside(d droppedCase) {
	d.t.Helper()
	for _, tc := range []struct{ query, message string }{
		{"SELECT * FROM pg_event_trigger_ddl_commands()", "pg_event_trigger_ddl_commands() can only be called in an event trigger function"},
		{"SELECT * FROM pg_event_trigger_dropped_objects()", droppedContextMessage},
		{"SELECT pg_event_trigger_table_rewrite_oid()", "pg_event_trigger_table_rewrite_oid() can only be called in a table_rewrite event trigger function"},
		{"SELECT pg_event_trigger_table_rewrite_reason()", "pg_event_trigger_table_rewrite_reason() can only be called in a table_rewrite event trigger function"},
	} {
		s := fixture.ErrorDiagnostic(tc.query, eventTriggerDiagnostic("39P03", tc.message))
		s.SelectRows, s.ErrorStage = true, "execute"
		d.run(s)
		d.run(fixture.Prepared(s))
	}
}

func builtinTargetData(d droppedCase, typ string) {
	d.t.Helper()
	d.run(fixture.Select("SELECT v FROM public.et_bi_target", []string{"v"}, []string{typ}, [][]any{{int64(7)}}))
}

func builtinCoexistLinks(d droppedCase) {
	d.t.Helper()
	d.run(fixture.Select("SELECT evtname,evtevent,evtenabled FROM pg_event_trigger WHERE evtname IN ('et_do_drop','et_do_end','et_bi_rw') ORDER BY evtname", []string{"evtname", "evtevent", "evtenabled"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"et_bi_rw", "table_rewrite", "O"}, {"et_do_drop", "sql_drop", "O"}, {"et_do_end", "ddl_command_end", "O"}}))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc f ON f.oid=e.evtfoid JOIN pg_namespace n ON n.oid=f.pronamespace WHERE e.evtname IN ('et_do_drop','et_do_end','et_bi_rw') AND f.proowner=e.evtowner AND n.nspname='public' AND f.proname=CASE e.evtname WHEN 'et_do_drop' THEN 'et_do_drop_fn' WHEN 'et_do_end' THEN 'et_do_end_fn' ELSE 'et_bi_rw_fn' END", 3))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname IN ('et_do_drop','et_do_end') AND evttags=ARRAY['CREATE TABLE','ALTER TABLE','DROP TABLE']", 2))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname='et_bi_rw' AND evttags=ARRAY['ALTER TABLE']", 1))
}

func TestReferenceEventTriggerBuiltins(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			eventTriggerScenario(t, builtinTriggerNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := droppedCase{t, p, b, c}
				builtinCoexistSetup(d)
				empty := func() {
					for _, table := range []string{"et_bi_ddl", "et_bi_rewrite", "et_bi_phase", "et_do_audit", "et_do_phase"} {
						d.run(eventTriggerCount("SELECT count(*) AS n FROM public."+table, 0))
					}
					d.run(eventTriggerCount("SELECT count(*) AS n FROM "+droppedRelationLookup("public", "et_bi_target"), 0))
				}
				empty()
				builtinUtility(d, "CREATE TABLE public.et_bi_target(v integer)", prepared, empty)
				root := droppedTable("public", "et_bi_target", false)[0]
				id := currentDroppedOID(d, root)
				builtinDDLBag(d, id, "CREATE TABLE")
				d.run(fixture.Exec("INSERT INTO public.et_bi_target VALUES(7)", 1))
				beforeAlter := func() {
					builtinDDLBag(d, id, "CREATE TABLE")
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_bi_rewrite", 0))
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_bi_phase", 1))
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 0))
					builtinTargetData(d, "INT4")
				}
				builtinUtility(d, "ALTER TABLE public.et_bi_target ALTER v TYPE bigint", prepared, beforeAlter)
				builtinDDLBag(d, id, "CREATE TABLE", "ALTER TABLE")
				builtinRewriteBag(d, id)
				builtinTargetData(d, "INT8")
				expected := d.capture(droppedTable("public", "et_bi_target", false))
				d.clear()
				d.exec("TRUNCATE public.et_bi_ddl")
				beforeDrop := func() {
					builtinDDLBag(d, id)
					builtinRewriteBag(d, id)
					builtinTargetData(d, "INT8")
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_bi_phase", 3))
					d.bag(1, nil)
					d.bag(2, nil)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
				}
				builtinUtility(d, "DROP TABLE public.et_bi_target", prepared, beforeDrop)
				d.bag(1, expected)
				d.bag(2, expected)
				d.phases(3)
				// Dropped objects are no longer identifiable at ddl_command_end:
				// the released collector emits no synthetic DROP receipt.
				builtinDDLBag(d, id)
				d.run(fixture.Select("SELECT seq,phase,tag FROM public.et_bi_phase ORDER BY seq", []string{"seq", "phase", "tag"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), "end", "CREATE TABLE"}, {int64(2), "rewrite", "ALTER TABLE"}, {int64(3), "end", "ALTER TABLE"}, {int64(4), "drop", "DROP TABLE"}, {int64(5), "end", "DROP TABLE"}}))
				d.exec("CREATE TABLE public.et_bi_target(v integer)")
				d.run(fixture.Exec("INSERT INTO public.et_bi_target VALUES(7)", 1))
				droppedReconstructionCheck(d, "public.et_do_audit", []droppedRecord{expected[0]})
				builtinCoexistOutside(d)
				builtinCoexistLinks(d)
				if b.PG {
					return
				}
				// Persist ordinary definitions/rows only. The historical dropped OIDs
				// remain data; each new address resolves against current catalogs.
				savedDDLTarget := currentDroppedOID(d, root)
				builtinDDLBag(d, savedDDLTarget, "CREATE TABLE")
				d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_bi_phase", 6))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 3))
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				builtinCoexistLinks(d)
				builtinTargetData(d, "INT4")
				d.bag(1, expected)
				d.bag(2, expected)
				builtinDDLBag(d, savedDDLTarget, "CREATE TABLE")
				builtinRewriteBag(d, id)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_bi_phase", 6))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 3))
				droppedReconstructionCheck(d, "public.et_do_audit", []droppedRecord{expected[0]})
				d.exec("TRUNCATE public.et_bi_ddl", "TRUNCATE public.et_bi_rewrite")
				builtinUtility(d, "ALTER TABLE public.et_bi_target ALTER v TYPE bigint", prepared, func() {
					builtinDDLBag(d, 0)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_bi_rewrite", 0))
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_bi_phase", 6))
					builtinTargetData(d, "INT4")
				})
				current := currentDroppedOID(d, root)
				builtinDDLBag(d, current, "ALTER TABLE")
				builtinRewriteBag(d, current)
				builtinTargetData(d, "INT8")
				builtinCoexistOutside(d)
			})
		})
	}
}
