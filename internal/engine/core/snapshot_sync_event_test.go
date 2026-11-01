package core_test

import (
	"testing"
	"errors"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestSnapshotSyncUtilityEventDemand(t *testing.T) {
	for _,route:=range []string{"matching-start","matching-end","no-data-event","disabled","tag-mismatch","phase-mismatch","setting-off"}{t.Run(route,func(t *testing.T){
		e:=engine.New()
		enginetest.Run(t,e,"CREATE MATERIALIZED VIEW sync_event_refresh AS SELECT 1 AS id WITH NO DATA")
		enginetest.Run(t,e,"CREATE FUNCTION sync_event_export() RETURNS event_trigger LANGUAGE plpgsql STABLE AS $$BEGIN PERFORM pg_export_snapshot();END$$")
		phase,filter:="ddl_command_start"," WHEN TAG IN ('REFRESH MATERIALIZED VIEW')"
		if route=="matching-end"{phase="ddl_command_end"}
		if route=="tag-mismatch"{filter=" WHEN TAG IN ('CREATE TABLE')"}
		if route=="phase-mismatch"{phase,filter="sql_drop",""}
		enginetest.Run(t,e,"CREATE EVENT TRIGGER sync_event_export ON "+phase+filter+" EXECUTE FUNCTION sync_event_export()")
		if route=="disabled"{enginetest.Run(t,e,"ALTER EVENT TRIGGER sync_event_export DISABLE")}
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
		if route=="setting-off"{if _,err:=s.Execute(enginetest.Statement(t,"SET event_triggers=off"),0,nil);err!=nil{t.Fatal(err)}}
		query:="REFRESH MATERIALIZED VIEW sync_event_refresh"
		if route=="no-data-event"{query+=" WITH NO DATA"}
		if err:=core.ScopeSnapshotExecutionAdmission(s,enginetest.Statement(t,query));err!=nil{t.Fatal(err)}
		want:=route=="matching-start"||route=="matching-end"||route=="no-data-event"
		if core.ScopeExportRootDemand(s)!=want{t.Fatalf("demand=%v want=%v",core.ScopeExportRootDemand(s),want)}
		if roots,_,_,_:=core.ScopeExportCapture(s);(roots!=nil)!=want{t.Fatal("event closure not paired with early utility capture")}
		if core.EngineExportCount(e)!=0{t.Fatal("event classification executed callback")}
	})}
	t.Run("new-phase-closure-fenced",func(t *testing.T) {
		e:=engine.New()
		enginetest.Run(t,e,"CREATE MATERIALIZED VIEW sync_event_refresh AS SELECT 1 AS id WITH NO DATA")
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
		if err:=core.ScopeSnapshotExecutionAdmission(s,enginetest.Statement(t,"REFRESH MATERIALIZED VIEW sync_event_refresh"));err!=nil{t.Fatal(err)}
		if roots,_,_,_:=core.ScopeExportCapture(s);roots!=nil{t.Fatal("pristine utility captured all roots")}
		enginetest.Run(t,e,"CREATE FUNCTION sync_event_export() RETURNS event_trigger LANGUAGE plpgsql STABLE AS $$BEGIN PERFORM pg_export_snapshot();END$$")
		enginetest.Run(t,e,"CREATE EVENT TRIGGER sync_event_export ON ddl_command_end WHEN TAG IN ('REFRESH MATERIALIZED VIEW') EXECUTE FUNCTION sync_event_export()")
		s.EventCommand=&core.EventCommand{Tag:"REFRESH MATERIALIZED VIEW"}
		if err:=s.FinishEventCommand(nil,nil);!errors.Is(err,ids.ErrCatalogReplaced){t.Fatal("new phase closure retrofitted old pair",err)}
		if core.EngineExportCount(e)!=0{t.Fatal("fenced phase published a token")}
	})
}
