package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLCommandsStateAndExtension(t *testing.T) {
	t.Run("unmatched_inventory", ddlCommandsUnmatchedOrdinaryFunction)
	t.Run("same_version_extension", ddlCommandsSameVersionExtension)
}

// REL_18_6 event_trigger.c:1190-1262 installs complete-query state for any
// enabled end/drop/rewrite inventory, independently of TAG matching. utility.c:
// 1678-1681,1942-1944 collects CTAS only after ExecCreateTableAs returns. Thus an
// ordinary function executing its source query sees an active, empty bag, not
// 39P03 and not a fabricated early CREATE TABLE AS receipt.
func ddlCommandsUnmatchedOrdinaryFunction(t *testing.T) {
	for _, inventory := range []struct{ phase, tag string }{
		{"ddl_command_end", "'CREATE VIEW'"},
		{"sql_drop", "'DROP VIEW'"},
		{"table_rewrite", "'ALTER TABLE'"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", inventory.phase, prepared), func(t *testing.T) {
				eventTriggerScenario(t, []string{"et_dc_unmatched"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					d := ddlCommandCase{t, p, b, c}
					d.exec(
						"CREATE TABLE et_dc_state_calls(n bigint)",
						"CREATE TABLE et_dc_state_callbacks(phase text,tag text)",
						"CREATE TABLE et_dc_state_bag(classid oid,objid oid,objsubid integer,command_tag text,object_type text,schema_name text,object_identity text,in_extension boolean,present boolean)",
						"CREATE FUNCTION et_dc_state_read() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_state_calls SELECT count(*) FROM pg_event_trigger_ddl_commands(); INSERT INTO public.et_dc_state_bag SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); RETURN 7; END$$",
						"CREATE FUNCTION et_dc_state_callback() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_state_callbacks VALUES(TG_EVENT,TG_TAG); END$$",
					)
					eventTriggerCreate(t, p, b, c, "et_dc_unmatched", inventory.phase, inventory.tag, "et_dc_state_callback")
					query := "CREATE TABLE et_dc_state_result AS SELECT public.et_dc_state_read() AS i"
					step := fixture.Exec(query, 1)
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_state_calls", 0))
						d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_state_result'", 0))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, step)
					} else {
						d.run(step)
					}
					for _, s := range []fixture.Step{
						fixture.Select("SELECT i FROM et_dc_state_result", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}),
						fixture.Select("SELECT n FROM et_dc_state_calls", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
						fixture.Select("SELECT phase,tag FROM et_dc_state_callbacks", []string{"phase", "tag"}, []string{"TEXT", "TEXT"}, nil),
						fixture.Select("SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM et_dc_state_bag", []string{"classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "present"}, []string{"OID", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, nil),
						fixture.Select("SELECT a.attname,a.atttypid FROM pg_attribute a JOIN pg_class c ON a.attrelid=c.oid WHERE c.relname='et_dc_state_result' AND c.relnamespace='public'::regnamespace AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "atttypid"}, []string{"NAME", "OID"}, [][]any{{"i", int64(23)}}),
					} {
						d.run(s)
						d.run(fixture.Prepared(s))
					}
					// No collector survives the complete-query boundary, despite the
					// still-enabled unmatched inventory.
					outside := fixture.ErrorDiagnostic("SELECT count(*) FROM pg_event_trigger_ddl_commands()", eventTriggerDiagnostic("39P03", ddlCommandsContextMessage))
					outside.SelectRows, outside.ErrorStage = true, "execute"
					d.run(outside)
					d.run(fixture.Prepared(outside))
				})
			})
		}
	}
}

// REL_18_6 extension.c:3516-3521 returns InvalidObjectAddress after this exact
// NOTICE. event_trigger.c:2098-2100 ignores invalid simple receipts. The callback
// itself still runs; zero command rows must not be confused with no callback.
func ddlCommandsSameVersionExtension(t *testing.T) {
	eventTriggerScenario(t, []string{"et_dc_same_version"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		pool, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer pool.Close()
		defer c.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "DROP EXTENSION IF EXISTS unaccent CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		b.Recorder = recorder
		d := ddlCommandCase{t, p, b, c}
		d.exec(
			"CREATE EXTENSION unaccent VERSION '1.1'",
			"CREATE TABLE et_dc_version_data(i integer,label text DEFAULT public.unaccent('Hôtel'))",
			"CREATE TABLE et_dc_version_calls(phase text,tag text,n bigint)",
			"CREATE TABLE et_dc_version_bag(classid oid,objid oid,objsubid integer,command_tag text,object_type text,schema_name text,object_identity text,in_extension boolean,present boolean)",
			"CREATE FUNCTION et_dc_version_capture() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_version_calls SELECT TG_EVENT,TG_TAG,count(*) FROM pg_event_trigger_ddl_commands(); INSERT INTO public.et_dc_version_bag SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); END$$",
		)
		d.run(fixture.Exec("INSERT INTO et_dc_version_data(i) VALUES(7)", 1))
		ext := ddlCommandsStateOID(d, "SELECT oid FROM pg_extension WHERE extname='unaccent'")
		namespace := ddlCommandsStateOID(d, "SELECT extnamespace FROM pg_extension WHERE extname='unaccent'")
		owner := ddlCommandsStateOID(d, "SELECT extowner FROM pg_extension WHERE extname='unaccent'")
		members := []struct {
			class int64
			query string
		}{
			{1255, "SELECT 'public.unaccent(regdictionary,text)'::regprocedure::oid"},
			{1255, "SELECT 'public.unaccent(text)'::regprocedure::oid"},
			{1255, "SELECT 'public.unaccent_init(internal)'::regprocedure::oid"},
			{1255, "SELECT 'public.unaccent_lexize(internal,internal,internal,internal)'::regprocedure::oid"},
			{3764, "SELECT oid FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace='public'::regnamespace"},
			{3600, "SELECT oid FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace='public'::regnamespace"},
		}
		ids := make([]int64, len(members))
		var edges [][]any
		for i, member := range members {
			ids[i] = ddlCommandsStateOID(d, member.query)
			edges = append(edges, []any{member.class, ids[i], int64(0), int64(3079), ext, int64(0), "e"})
		}
		edgeStep := fixture.Select("SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"OID", "OID", "INT4", "OID", "OID", "INT4", "CHAR"}, edges, ext)
		edgeStep.Bag = true
		// These definitions are independently specified by the released unaccent
		// 1.1 SQL, not snapshots of native output or fabricated script execution.
		state := []fixture.Step{
			fixture.Select("SELECT oid,extnamespace,extowner,extversion,extrelocatable,extconfig,extcondition FROM pg_extension WHERE extname='unaccent'", []string{"oid", "extnamespace", "extowner", "extversion", "extrelocatable", "extconfig", "extcondition"}, []string{"OID", "OID", "OID", "TEXT", "BOOL", "_OID", "_TEXT"}, [][]any{{ext, namespace, owner, "1.1", true, nil, nil}}),
			edgeStep,
			fixture.Select("SELECT oid,proname,proargtypes::text,prorettype,prosrc,probin,provolatile,proisstrict FROM pg_proc WHERE oid IN ($1,$2,$3,$4) ORDER BY proname,pronargs", []string{"oid", "proname", "proargtypes", "prorettype", "prosrc", "probin", "provolatile", "proisstrict"}, []string{"OID", "NAME", "TEXT", "OID", "TEXT", "TEXT", "CHAR", "BOOL"}, [][]any{
				{ids[1], "unaccent", "25", int64(25), "unaccent_dict", "$libdir/unaccent", "s", true},
				{ids[0], "unaccent", "3769 25", int64(25), "unaccent_dict", "$libdir/unaccent", "s", true},
				{ids[2], "unaccent_init", "2281", int64(2281), "unaccent_init", "$libdir/unaccent", "v", false},
				{ids[3], "unaccent_lexize", "2281 2281 2281 2281", int64(2281), "unaccent_lexize", "$libdir/unaccent", "v", false},
			}, ids[0], ids[1], ids[2], ids[3]),
			fixture.Select("SELECT oid,tmplnamespace,tmplinit::oid,tmpllexize::oid FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace='public'::regnamespace", []string{"oid", "tmplnamespace", "tmplinit", "tmpllexize"}, []string{"OID", "OID", "OID", "OID"}, [][]any{{ids[4], namespace, ids[2], ids[3]}}),
			fixture.Select("SELECT oid,dictnamespace,dictowner,dicttemplate,dictinitoption FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace='public'::regnamespace", []string{"oid", "dictnamespace", "dictowner", "dicttemplate", "dictinitoption"}, []string{"OID", "OID", "OID", "OID", "TEXT"}, [][]any{{ids[5], namespace, owner, ids[4], "rules = 'unaccent'"}}),
			fixture.Select("SELECT i,label,public.unaccent('Hôtel') AS live FROM et_dc_version_data", []string{"i", "label", "live"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "Hotel", "Hotel"}}),
			fixture.Select("SELECT attname,atttypid,atthasdef FROM pg_attribute WHERE attrelid='public.et_dc_version_data'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atthasdef"}, []string{"NAME", "OID", "BOOL"}, [][]any{{"i", int64(23), false}, {"label", int64(25), true}}),
		}
		checkState := func() {
			for _, s := range state {
				d.run(s)
				d.run(fixture.Prepared(s))
			}
		}
		checkState()
		eventTriggerCreate(t, p, b, c, "et_dc_same_version", "ddl_command_end", "'ALTER EXTENSION'", "et_dc_version_capture")
		for i, prepared := range []bool{false, true} {
			notices = nil
			s := fixture.Exec("ALTER EXTENSION unaccent UPDATE TO '1.1'", 0)
			s.Prepared = prepared
			d.run(s)
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "version \"1.1\" of extension \"unaccent\" is already installed"}
			if got := fixture.NoticeWitnesses(t, b.PG, notices, wantNotice); !reflect.DeepEqual(got, []fixture.Diagnostic{wantNotice}) {
				t.Fatalf("same-version notice=%#v", got)
			}
			wantCalls := make([][]any, i+1)
			for j := range wantCalls {
				wantCalls[j] = []any{"ddl_command_end", "ALTER EXTENSION", int64(0)}
			}
			calls := fixture.Select("SELECT phase,tag,n FROM et_dc_version_calls", []string{"phase", "tag", "n"}, []string{"TEXT", "TEXT", "INT8"}, wantCalls)
			calls.Bag = true
			d.run(calls)
			d.run(fixture.Prepared(calls))
			bag := fixture.Select("SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM et_dc_version_bag", []string{"classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "present"}, []string{"OID", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, nil)
			d.run(bag)
			d.run(fixture.Prepared(bag))
			checkState()
		}
	})
}

// Capture only allocator-selected identities from public catalogs. Every later
// projection validates raw OID carriers and logical metadata with the incumbent
// fixture; command results and definitions have independent expectations above.
func ddlCommandsStateOID(d ddlCommandCase, query string) int64 {
	d.t.Helper()
	var raw any
	if err := d.c.QueryRowContext(d.p.Ctx, query).Scan(&raw); err != nil {
		d.t.Fatal(err)
	}
	oid := fixture.OidReferenceValue(d.t, d.b.PG, "OID", raw).(int64)
	if oid == 0 {
		d.t.Fatalf("invalid local identity: %s", query)
	}
	return oid
}
