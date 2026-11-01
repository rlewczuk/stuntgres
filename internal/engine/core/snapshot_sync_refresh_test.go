package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestSnapshotSyncUtilityCaptureDemand(t *testing.T) {
	for _, route := range []string{"plain", "known-builtin", "export", "nested-current-routine", "opaque", "policy", "no-data"} {
		t.Run(route, func(t *testing.T) {
			e := engine.New()
			source := "SELECT 1 AS id"
			if route == "export" || route == "no-data" { source = "SELECT pg_export_snapshot() AS token" }
			if route == "known-builtin" { source = "SELECT abs(-1) AS id" }
			if route == "opaque" {
				enginetest.Run(t,e,"CREATE FUNCTION sync_refresh_opaque() RETURNS TEXT LANGUAGE plpgsql AS $$BEGIN RETURN 'ordinary';END$$")
				source = "SELECT sync_refresh_opaque() AS token"
			}
			if route == "policy" {
				enginetest.Run(t,e,"CREATE TABLE sync_refresh_data(id INT)")
				enginetest.Run(t,e,"ALTER TABLE sync_refresh_data ENABLE ROW LEVEL SECURITY")
				enginetest.Run(t,e,"CREATE POLICY sync_refresh_policy ON sync_refresh_data USING(pg_export_snapshot() IS NOT NULL)")
				source = "SELECT id FROM sync_refresh_data"
			}
			if route == "nested-current-routine" {
				enginetest.Run(t,e,"CREATE FUNCTION sync_refresh_value() RETURNS TEXT BEGIN ATOMIC SELECT 'ordinary'::text; END")
				enginetest.Run(t,e,"CREATE VIEW sync_refresh_view AS SELECT sync_refresh_value() AS token")
				source = "SELECT token FROM sync_refresh_view"
			}
			enginetest.Run(t,e,"CREATE MATERIALIZED VIEW sync_refresh AS "+source+" WITH NO DATA")
			if route == "nested-current-routine" {
				enginetest.Run(t,e,"CREATE OR REPLACE FUNCTION sync_refresh_value() RETURNS TEXT BEGIN ATOMIC SELECT pg_export_snapshot(); END")
			}
			for _, internal := range []bool{false,true} {
				s := snapshotSyncScope(t,e,false,session.ReadCommitted)
				query := "REFRESH MATERIALIZED VIEW sync_refresh"
				if route == "no-data" { query += " WITH NO DATA" }
				statement := enginetest.Statement(t,query)
				var err error
				if internal { err = s.AcquireRelation(statement,true) } else { err = core.ScopeSnapshotExecutionAdmission(s,statement) }
				if err != nil { t.Fatal(err) }
				want := route == "export" || route == "nested-current-routine" || route == "opaque" || route == "policy"
				if core.ScopeExportRootDemand(s) != want { t.Fatalf("internal %v: demand=%v want=%v",internal,core.ScopeExportRootDemand(s),want) }
				roots,_,_,_ := core.ScopeExportCapture(s)
				if (roots != nil) != want { t.Fatal("REFRESH did not classify before its utility cut") }
				if core.EngineExportCount(e) != 0 { t.Fatal("metadata-only REFRESH admission executed a recipe") }
			}
		})
	}
	t.Run("unbound-ctas",func(t *testing.T) {
		e:=engine.New()
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
		statement:=enginetest.Statement(t,"CREATE TABLE sync_ctas AS SELECT pg_export_snapshot()")
		if err:=core.ScopeSnapshotExecutionAdmission(s,statement);err!=nil{t.Fatal(err)}
		if roots,_,_,_:=core.ScopeExportCapture(s);roots!=nil||core.ScopeExportRootDemand(s){t.Fatal("CTAS retained an execution pair before source admission")}
		if err:=s.AcquireRelation(statement,true);err!=nil{t.Fatal(err)}
		core.SetScopeExportRootDemand(s,true)
		if err:=core.ScopeSnapshotPhase(s,statement);err!=nil{t.Fatal(err)}
		if roots,_,_,_:=core.ScopeExportCapture(s);roots==nil{t.Fatal("CTAS lost post-source utility pair")}
		if core.EngineExportCount(e)!=0{t.Fatal("CTAS metadata admission executed a recipe")}
	})
	t.Run("unbound-partition",func(t *testing.T) {
		e:=engine.New()
		enginetest.Run(t,e,"CREATE TABLE sync_partitioned(k TEXT) PARTITION BY LIST(k)")
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
		statement:=enginetest.Statement(t,"CREATE TABLE sync_partition PARTITION OF sync_partitioned FOR VALUES IN(pg_export_snapshot())")
		if err:=s.AcquireRelation(statement,true);err!=nil{t.Fatal(err)}
		if roots,_,_,_:=core.ScopeExportCapture(s);roots==nil||!core.ScopeExportRootDemand(s){t.Fatal("unbound partition utility lost its prewait capture")}
		if core.EngineExportCount(e)!=0{t.Fatal("partition metadata admission evaluated a bound")}
	})
}

func TestSnapshotSyncPolicySourceDemand(t *testing.T){
	for _,route:=range []string{"plain","disabled","select","check-only","analysis-only"}{
		t.Run(route,func(t *testing.T){
			e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_read_policy(id INT)")
			if route!="plain"{
				clause:="USING(pg_export_snapshot() IS NOT NULL)"
				if route=="check-only"{clause="FOR UPDATE WITH CHECK(pg_export_snapshot() IS NOT NULL)"}
				enginetest.Run(t,e,"CREATE POLICY sync_read_export ON sync_read_policy "+clause)
			}
			if route!="disabled"{enginetest.Run(t,e,"ALTER TABLE sync_read_policy ENABLE ROW LEVEL SECURITY")}
			s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
			ref:=enginetest.RoleTableRef(t,e,"sync_read_policy")
			e.Mu.Lock();relation,_:=e.Catalog.State.Relation(ref.Address)
			b:=analyze.Binding{QuerySources:[]exec.Leaf{{Ref:ref,Relation:relation,AnalysisOnly:route=="analysis-only"}}}
			core.ScopeInstallExportDemandFor(s,&b,&parser.Select{});e.Mu.Unlock()
			want:=route=="select"
			if core.ScopeExportRootDemand(s)!=want{t.Fatal("ordinary read policy demand",route,core.ScopeExportRootDemand(s))}
			if err:=core.ScopeSnapshotDataPhase(s);err!=nil{t.Fatal(err)}
			roots,_,_,_:=core.ScopeExportCapture(s)
			if (roots!=nil)!=want||core.EngineExportCount(e)!=0{t.Fatal("policy metadata executed a recipe or selected wrong capture")}
		})
	}
}
