package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerRewriteContext(t *testing.T) {
	t.Run("outside",func(t *testing.T) { rewriteScenario(t,func(d rewriteCase) { d.outside() }) })
	for _, phase:=range []string{"ddl_command_start","ddl_command_end","sql_drop"} {
		t.Run(phase,func(t *testing.T) {
			rewriteScenario(t,func(d rewriteCase) {
				d.exec("CREATE TABLE et_rw_t(v integer,doomed integer)","CREATE TABLE et_rw_guard_log(event text,helper text,state text,message text)")
				if phase!="ddl_command_start" { d.setup() }
				d.exec("CREATE FUNCTION et_rw_guard_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM pg_event_trigger_table_rewrite_oid(); EXCEPTION WHEN SQLSTATE '39P03' THEN INSERT INTO et_rw_guard_log VALUES(TG_EVENT,'oid',SQLSTATE,SQLERRM); END; BEGIN PERFORM pg_event_trigger_table_rewrite_reason(); EXCEPTION WHEN SQLSTATE '39P03' THEN INSERT INTO et_rw_guard_log VALUES(TG_EVENT,'reason',SQLSTATE,SQLERRM); END; END$$")
				// The real rewrite initializes and clears release scratch before
				// end/drop. No-rewrite release reason scratch is uninitialized.
				eventTriggerCreate(t,d.p,d.b,d.c,"et_rw_guard",phase,"'ALTER TABLE'","et_rw_guard_fn")
				d.exec("ALTER TABLE et_rw_t DROP COLUMN doomed, ALTER COLUMN v TYPE bigint")
				d.run(fixture.Select("SELECT event,helper,state,message FROM et_rw_guard_log ORDER BY helper",[]string{"event","helper","state","message"},[]string{"TEXT","TEXT","TEXT","TEXT"},[][]any{{phase,"oid","39P03",rewriteProtocol("pg_event_trigger_table_rewrite_oid").Message},{phase,"reason","39P03",rewriteProtocol("pg_event_trigger_table_rewrite_reason").Message}}))
				d.outside()
			})
		})
	}
	for _, prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"nested_direct",true:"nested_prepared"}[prepared],func(t *testing.T) {
			rewriteScenario(t,func(d rewriteCase) {
				d.exec("CREATE TABLE et_rw_t(v integer)","CREATE TABLE et_rw_inner(v integer)","CREATE TABLE et_rw_lost(v integer)")
				d.setup()
				d.exec("CREATE TABLE et_rw_pairs(label text,target oid,reason integer)",
					"CREATE FUNCTION et_rw_inner_fail() RETURNS integer LANGUAGE SQL AS $$ALTER TABLE et_rw_lost ALTER v TYPE bigint; SELECT 1$$",
					"CREATE OR REPLACE FUNCTION et_rw_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN IF pg_event_trigger_table_rewrite_oid()='et_rw_lost'::regclass THEN RAISE EXCEPTION 'inner rejected'; END IF; INSERT INTO et_rw_pairs VALUES('before',public.et_rw_read(),pg_event_trigger_table_rewrite_reason()); IF pg_event_trigger_table_rewrite_oid()='et_rw_t'::regclass THEN BEGIN SELECT et_rw_inner_fail() INTO n; EXCEPTION WHEN raise_exception THEN n:=0; END; ALTER TABLE et_rw_inner ALTER v TYPE bigint; ALTER TABLE et_rw_inner ADD plain integer; INSERT INTO et_rw_pairs VALUES('after',public.et_rw_read(),pg_event_trigger_table_rewrite_reason()); END IF; END$$")
				d.exec("CREATE TABLE et_rw_nested_guard(n integer)",
					"CREATE FUNCTION et_rw_nested_guard_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM pg_event_trigger_table_rewrite_oid(); RAISE EXCEPTION 'nested command borrowed outer rewrite'; EXCEPTION WHEN SQLSTATE '39P03' THEN INSERT INTO et_rw_nested_guard VALUES(1); END; END$$")
				eventTriggerCreate(t,d.p,d.b,d.c,"et_rw_guard","ddl_command_start","'ALTER TABLE'","et_rw_nested_guard_fn")
				d.alter("ALTER TABLE et_rw_t ALTER v TYPE bigint",prepared)
				d.run(fixture.Select("SELECT label,target::regclass::text AS target,reason FROM et_rw_pairs ORDER BY target,label",[]string{"label","target","reason"},[]string{"TEXT","TEXT","INT4"},[][]any{{"before","et_rw_inner",int64(4)},{"after","et_rw_t",int64(4)},{"before","et_rw_t",int64(4)}}))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM et_rw_nested_guard",3))
				d.outside()
				d.exec("CREATE OR REPLACE FUNCTION et_rw_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'outer rejected'; END$$")
				s:=fixture.ErrorDiagnostic("ALTER TABLE et_rw_t ALTER v TYPE integer",eventTriggerFailure("et_rw_fn","outer rejected")); s.Prepared=prepared; d.run(s)
				d.outside()
			})
		})
	}
	t.Run("held_current_target",func(t *testing.T) {
		rewriteScenario(t,func(d rewriteCase) {
			d.exec("CREATE TABLE et_rw_t(v integer)"); d.setup()
			q:="ALTER TABLE et_rw_t ALTER v TYPE bigint"
			s,err:=d.c.PrepareContext(d.p.Ctx,q); if err!=nil { t.Fatal(err) }; defer s.Close()
			for range 2 {
				fixture.RunStep(t,d.p.Ctx,fixture.HeldStatement{Executor:d.c,Statement:s,SQL:q},d.b.Recorder,fixture.Exec(q,0))
				d.audit("et_rw_t",4,"ALTER TABLE")
				d.run(fixture.Exec("DELETE FROM et_rw_audit",1))
				d.exec("DROP TABLE et_rw_t","CREATE TABLE et_rw_t(v integer)")
			}
			d.outside()
		})
	})
}

func TestReferenceEventTriggerRewriteMetadata(t *testing.T) {
	for _, prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
			rewriteScenario(t,func(d rewriteCase) {
				d.exec("CREATE TABLE et_rw_t(v integer)",
					"CREATE TABLE et_rw_scalar AS SELECT pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason() WHERE false",
					"CREATE TABLE et_rw_from AS SELECT * FROM pg_event_trigger_table_rewrite_oid() WHERE false",
					"CREATE TABLE et_rw_range AS SELECT * FROM pg_event_trigger_table_rewrite_oid() AS r WHERE false",
					"CREATE TABLE et_rw_alias AS SELECT * FROM pg_event_trigger_table_rewrite_oid() AS r(target) WHERE false",
					"CREATE TABLE et_rw_reason AS SELECT * FROM pg_event_trigger_table_rewrite_reason() WHERE false")
				d.setup()
				d.exec("CREATE OR REPLACE FUNCTION et_rw_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO et_rw_scalar SELECT pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason(); INSERT INTO et_rw_from SELECT * FROM pg_event_trigger_table_rewrite_oid(); INSERT INTO et_rw_range SELECT * FROM pg_event_trigger_table_rewrite_oid() AS r; INSERT INTO et_rw_alias SELECT * FROM pg_event_trigger_table_rewrite_oid() AS r(target); INSERT INTO et_rw_reason SELECT * FROM pg_event_trigger_table_rewrite_reason(); END$$")
				d.alter("ALTER TABLE et_rw_t ALTER v TYPE bigint",prepared)
				for _, tc:=range []struct{table string; names,types []string}{
					{"et_rw_scalar",[]string{"pg_event_trigger_table_rewrite_oid","pg_event_trigger_table_rewrite_reason"},[]string{"OID","INT4"}},
					{"et_rw_from",[]string{"oid"},[]string{"OID"}},
					{"et_rw_range",[]string{"oid"},[]string{"OID"}},
					{"et_rw_alias",[]string{"target"},[]string{"OID"}},
					{"et_rw_reason",[]string{"pg_event_trigger_table_rewrite_reason"},[]string{"INT4"}},
				} {
					s:=fixture.Select("SELECT * FROM "+tc.table+" WHERE false",tc.names,tc.types,[][]any{})
					d.run(s); d.run(fixture.Prepared(s))
				}
				d.run(fixture.Select("SELECT pg_event_trigger_table_rewrite_oid='et_rw_t'::regclass AS correct_target,pg_event_trigger_table_rewrite_reason FROM et_rw_scalar",[]string{"correct_target","pg_event_trigger_table_rewrite_reason"},[]string{"BOOL","INT4"},[][]any{{true,int64(4)}}))
				d.run(fixture.Select("SELECT a.attname,a.atttypid::integer AS type_oid FROM pg_attribute a WHERE a.attrelid='et_rw_scalar'::regclass AND a.attnum>0 ORDER BY a.attnum",[]string{"attname","type_oid"},[]string{"NAME","INT4"},[][]any{{"pg_event_trigger_table_rewrite_oid",int64(26)},{"pg_event_trigger_table_rewrite_reason",int64(23)}}))
				d.outside()
			})
		})
	}
}
