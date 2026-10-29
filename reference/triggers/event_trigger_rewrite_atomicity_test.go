package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerRewriteAtomicity(t *testing.T) {
	for _, phase:=range []string{"table_rewrite","ddl_command_end"} {
		for _, prepared:=range []bool{false,true} {
			t.Run(phase+"/"+map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
				rewriteScenario(t,func(d rewriteCase) {
					defer d.c.ExecContext(context.Background(),"DROP ACCESS METHOD IF EXISTS et_rw_heap CASCADE")
					d.exec("CREATE TABLE et_rw_t(v integer)","CREATE SEQUENCE et_rw_seq START 1","CREATE ACCESS METHOD et_rw_heap TYPE TABLE HANDLER heap_tableam_handler")
					d.run(fixture.Exec("INSERT INTO et_rw_t VALUES(7)",1)); d.setup()
					d.run(fixture.Exec("INSERT INTO et_rw_audit VALUES(0,0,'sentinel',false,false)",1))
					fn:="et_rw_fn"
					if phase=="ddl_command_end" {
						fn="et_rw_end_fn"
						d.exec("CREATE FUNCTION et_rw_end_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'rewrite veto'; END$$")
						eventTriggerCreate(t,d.p,d.b,d.c,"et_rw_end",phase,"'ALTER TABLE'",fn)
					} else {
						d.exec("CREATE OR REPLACE FUNCTION et_rw_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO et_rw_audit VALUES(pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason(),TG_TAG,true,true); RAISE EXCEPTION 'rewrite veto'; END$$")
					}
					check:=func() {
						d.run(fixture.Select("SELECT v FROM et_rw_t",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
						d.run(fixture.Select("SELECT relpersistence,a.amname FROM pg_class c JOIN pg_am a ON a.oid=c.relam WHERE c.oid='et_rw_t'::regclass",[]string{"relpersistence","amname"},[]string{"CHAR","NAME"},[][]any{{"p","heap"}}))
						d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='et_rw_t'::regclass AND attnum>0 AND NOT attisdropped",1))
						d.run(fixture.Select("SELECT reason,tag FROM et_rw_audit",[]string{"reason","tag"},[]string{"INT4","TEXT"},[][]any{{int64(0),"sentinel"}}))
					}
					q:="ALTER TABLE et_rw_t SET UNLOGGED, ADD extra bigint DEFAULT nextval('et_rw_seq'), ALTER v TYPE bigint, SET ACCESS METHOD et_rw_heap"
					fail:=fixture.ErrorDiagnostic(q,eventTriggerFailure(fn,"rewrite veto")); fail.Prepared=prepared
					d.run(fail); check(); d.outside()
					d.exec("BEGIN","SAVEPOINT rw_before"); d.run(fail); d.exec("ROLLBACK TO rw_before"); check(); d.exec("COMMIT")
					if phase=="ddl_command_end" { d.exec("ALTER EVENT TRIGGER et_rw_end DISABLE") } else {
						d.exec("CREATE OR REPLACE FUNCTION et_rw_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO et_rw_audit VALUES(pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason(),TG_TAG,true,true); END$$")
					}
					d.run(fixture.Exec("DELETE FROM et_rw_audit",1))
					d.alter("ALTER TABLE et_rw_t ALTER v TYPE bigint",prepared); d.audit("et_rw_t",4,"ALTER TABLE"); d.outside()
				})
			})
		}
	}
}
