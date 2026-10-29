package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func rewritePersistentSetup(d rewriteCase, prepared bool) int64 {
	d.t.Helper()
	d.exec("CREATE TABLE et_rw_t(v integer)","CREATE SEQUENCE et_rw_seq START 1","CREATE TABLE et_rw_owner(name text)")
	d.run(fixture.Exec("INSERT INTO et_rw_owner VALUES(current_user)",1))
	d.run(fixture.Exec("INSERT INTO et_rw_t VALUES(7)",1)); d.setup()
	d.exec("CREATE FUNCTION et_rw_disabled_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'disabled rewrite ran'; END$$")
	eventTriggerCreate(d.t,d.p,d.b,d.c,"et_rw_disabled","table_rewrite","'ALTER TABLE'","et_rw_disabled_fn")
	d.exec("ALTER EVENT TRIGGER et_rw_disabled DISABLE")
	d.alter("ALTER TABLE et_rw_t ADD extra bigint DEFAULT nextval('et_rw_seq'), ALTER v TYPE bigint",prepared)
	d.audit("et_rw_t",6,"ALTER TABLE")
	d.run(fixture.Select("SELECT v,extra FROM et_rw_t",[]string{"v","extra"},[]string{"INT8","INT8"},[][]any{{int64(7),int64(1)}}))
	var historic int64
	if err:=d.c.QueryRowContext(d.p.Ctx,"SELECT target::bigint FROM et_rw_audit").Scan(&historic); err!=nil { d.t.Fatal(err) }
	if historic<=0 { d.t.Fatal("invalid historical relation OID",historic) }
	return historic
}

func rewritePersistentCheck(d rewriteCase, historic int64) {
	d.t.Helper()
	// Historical target integers remain user data. Only a fresh event is
	// required to identify the current restored relation.
	s:=fixture.Select("SELECT reason,tag,same_oid,same_reason FROM et_rw_audit",[]string{"reason","tag","same_oid","same_reason"},[]string{"INT4","TEXT","BOOL","BOOL"},[][]any{{int64(6),"ALTER TABLE",true,true}})
	d.run(s); d.run(fixture.Prepared(s))
	d.run(fixture.Select("SELECT target::bigint AS historic_target FROM et_rw_audit",[]string{"historic_target"},[]string{"INT8"},[][]any{{historic}}))
	d.run(fixture.Select("SELECT v,extra FROM et_rw_t",[]string{"v","extra"},[]string{"INT8","INT8"},[][]any{{int64(7),int64(1)}}))
	d.run(fixture.Select("SELECT evtname,evtevent,evtenabled FROM pg_event_trigger WHERE evtname IN ('et_rw','et_rw_disabled') ORDER BY evtname",[]string{"evtname","evtevent","evtenabled"},[]string{"NAME","NAME","CHAR"},[][]any{{"et_rw","table_rewrite","O"},{"et_rw_disabled","table_rewrite","D"}}))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc f ON f.oid=e.evtfoid JOIN pg_roles r ON r.oid=e.evtowner JOIN et_rw_owner o ON o.name=r.rolname WHERE e.evtname IN ('et_rw','et_rw_disabled') AND f.proowner=e.evtowner",2))
	d.run(fixture.Select("SELECT c.relpersistence,a.amname FROM pg_class c JOIN pg_am a ON a.oid=c.relam WHERE c.oid='et_rw_t'::regclass",[]string{"relpersistence","amname"},[]string{"CHAR","NAME"},[][]any{{"p","heap"}}))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname='et_rw' AND evttags=ARRAY['ALTER TABLE','ALTER TYPE']",1))
	d.outside()
	for _, prepared:=range []bool{false,true} {
		d.exec("BEGIN")
		insert:=fixture.Exec("INSERT INTO et_rw_t(v) VALUES(8)",1)
		insert.Prepared=prepared
		d.run(insert)
		d.run(fixture.Select("SELECT extra>1 AS default_ran FROM et_rw_t WHERE v=8",[]string{"default_ran"},[]string{"BOOL"},[][]any{{true}}))
		d.run(fixture.Exec("DELETE FROM et_rw_audit",1))
		d.alter("ALTER TABLE et_rw_t ALTER v TYPE integer",prepared)
		d.audit("et_rw_t",4,"ALTER TABLE")
		d.exec("ROLLBACK")
	}
}

func TestReferenceEventTriggerRewritePersistence(t *testing.T) {
	rewriteScenario(t,func(d rewriteCase) {
		historic:=rewritePersistentSetup(d,false); rewritePersistentCheck(d,historic)
		if d.b.PG { return }
		var saved []byte
		for range 2 {
			var dump bytes.Buffer
			if err:=stuntgres.Dump(d.p.Ctx,d.p.Native,&dump); err!=nil { t.Fatal(err) }
			saved=append([]byte(nil),dump.Bytes()...)
			if err:=stuntgres.Reset(d.p.Ctx,d.p.Native); err!=nil { t.Fatal(err) }
			if err:=stuntgres.Restore(d.p.Ctx,d.p.Native,bytes.NewReader(saved)); err!=nil { t.Fatal(err) }
			rewritePersistentCheck(d,historic)
			if d.p.Postgres!=nil {
				func() {
					c,err:=d.p.Postgres.Conn(d.p.Ctx); if err!=nil { t.Fatal(err) }; defer c.Close()
					defer func() {
						eventTriggerCleanup(t,d.p.Postgres,rewriteTriggerNames)
						for _,q:=range []string{"DROP SCHEMA public CASCADE","CREATE SCHEMA public"} { if _,err:=c.ExecContext(d.p.Ctx,q); err!=nil { t.Error(err) } }
					}()
					if _,err:=c.ExecContext(d.p.Ctx,string(saved)); err!=nil { t.Fatal("unchanged rewrite dump replay",err) }
					rewritePersistentCheck(rewriteCase{t,d.p,fixture.Backend{PG:true,Recorder:d.p.PostgresRecord},c},historic)
				}()
			}
		}
		if err:=stuntgres.Restore(d.p.Ctx,d.p.Native,io.MultiReader(bytes.NewReader(saved),fixture.CatalogControlFailingReader{Err:io.ErrUnexpectedEOF})); !errors.Is(err,io.ErrUnexpectedEOF) { t.Fatal("Restore reader failure",err) }
		rewritePersistentCheck(d,historic)
		ctx,cancel:=context.WithCancel(d.p.Ctx); cancel()
		if err:=stuntgres.Restore(ctx,d.p.Native,bytes.NewReader(saved)); !errors.Is(err,context.Canceled) { t.Fatal("Restore cancellation",err) }
		rewritePersistentCheck(d,historic)
		connector,err:=stuntgres.NewDriver().OpenConnector("rewrite-fresh/"+t.Name()); if err!=nil { t.Fatal(err) }
		fresh:=sql.OpenDB(connector); defer fresh.Close()
		if err:=stuntgres.Restore(d.p.Ctx,fresh,bytes.NewReader(saved)); err!=nil { t.Fatal(err) }
		recorded,recorder:=fixture.RecordedNativePool(t,connector.(*stuntgres.Connector))
		c,err:=recorded.Conn(d.p.Ctx); if err!=nil { t.Fatal(err) }; defer c.Close()
		rewritePersistentCheck(rewriteCase{t,d.p,fixture.Backend{Recorder:recorder},c},historic)
	})
}

func TestReferenceEventTriggerRewriteSmoke(t *testing.T) {
	for _, prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
			rewriteScenario(t,func(d rewriteCase) {
				historic:=rewritePersistentSetup(d,prepared)
				d.alter("ALTER TABLE et_rw_t ALTER v TYPE bigint USING v",prepared)
				d.audit("et_rw_t",6,"ALTER TABLE"); d.outside()
				if !d.b.PG {
					var dump bytes.Buffer
					if err:=stuntgres.Dump(d.p.Ctx,d.p.Native,&dump); err!=nil { t.Fatal(err) }
					if err:=stuntgres.Reset(d.p.Ctx,d.p.Native); err!=nil { t.Fatal(err) }
					if err:=stuntgres.Restore(d.p.Ctx,d.p.Native,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
				}
				rewritePersistentCheck(d,historic)
			})
		})
	}
}
