package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestSnapshotSyncForeignDemand(t *testing.T) {
	for _,tc:=range []struct{name,action,mode string;demand bool}{
		{"no-foreign-key","","",false},
		{"restrict","RESTRICT","",false},
		{"default","SET DEFAULT","",true},
		{"not-valid","SET DEFAULT"," NOT VALID",true},
		{"not-enforced","SET DEFAULT"," NOT ENFORCED",false},
	}{t.Run(tc.name,func(t *testing.T){
		e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_fk_parent(id TEXT PRIMARY KEY,v INT)")
		if tc.action!=""{
			enginetest.Run(t,e,"CREATE TABLE sync_fk_child(id INT,fk TEXT DEFAULT pg_export_snapshot())")
			enginetest.Run(t,e,"ALTER TABLE sync_fk_child ADD CONSTRAINT sync_fk FOREIGN KEY(fk) REFERENCES sync_fk_parent(id) ON DELETE "+tc.action+tc.mode)
		}
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
		s.StatementMutation.Statement=&parser.Select{} // Suspended/stale scratch is not the executing DELETE.
		statement:=&parser.Delete{}
		ref:=enginetest.RoleTableRef(t,e,"sync_fk_parent")
		e.Mu.Lock();relation,_:=e.Catalog.State.Relation(ref.Address)
		b:=analyze.Binding{Ref:ref,Relation:relation}
		core.ScopeInstallExportDemandFor(s,&b,statement);e.Mu.Unlock()
		if core.ScopeExportRootDemand(s)!=tc.demand||s.UniqueBinding!=nil{t.Fatal("wrong demand or eager unique binding",core.ScopeExportRootDemand(s))}
		if tc.name == "no-foreign-key" {
			e.Mu.Lock()
			allocations := testing.AllocsPerRun(10, func() {
				core.SetScopeExportRootDemand(s, false)
				core.ScopeInstallExportDemandFor(s, &b, statement)
			})
			e.Mu.Unlock()
			if allocations != 0 { t.Fatal("ordinary no-FK classification allocated candidate/visited backing", allocations) }
		}
		if err:=core.ScopeSnapshotDataPhase(s);err!=nil{t.Fatal(err)}
		roots,_,_,_:=core.ScopeExportCapture(s)
		if (roots!=nil)!=tc.demand{t.Fatal("ordinary no-FK/unenforced/restrict admission captured complete roots")}
	})}
	t.Run("transitive-current-recipes",func(t *testing.T){
		e:=engine.New()
		for _,q:=range []string{
			"CREATE TABLE sync_fk_parent(id TEXT PRIMARY KEY,v INT)",
			"CREATE TABLE sync_fk_child(id TEXT PRIMARY KEY REFERENCES sync_fk_parent(id) ON UPDATE CASCADE)",
			"CREATE FUNCTION sync_fk_value() RETURNS TEXT BEGIN ATOMIC SELECT 'fallback'::text; END",
			"CREATE TABLE sync_fk_grandchild(fk TEXT DEFAULT sync_fk_value() REFERENCES sync_fk_child(id) ON UPDATE SET DEFAULT)",
		}{enginetest.Run(t,e,q)}
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
		ref:=enginetest.RoleTableRef(t,e,"sync_fk_parent")
		classify:=func(target int)bool{
			core.SetScopeExportRootDemand(s,false)
			e.Mu.Lock();relation,_:=e.Catalog.State.Relation(ref.Address)
			b:=analyze.Binding{Ref:ref,Relation:relation,Targets:[]int{target}}
			core.ScopeInstallExportDemandFor(s,&b,&parser.Update{});e.Mu.Unlock()
			return core.ScopeExportRootDemand(s)
		}
		if classify(0){t.Fatal("known non-exporting transitive routine demanded roots")}
		enginetest.Run(t,e,"CREATE OR REPLACE FUNCTION sync_fk_value() RETURNS TEXT BEGIN ATOMIC SELECT CASE WHEN set_config('sync.token',pg_export_snapshot(),false) IS NOT NULL THEN 'fallback'::text ELSE 'fallback'::text END; END")
		if !classify(0){t.Fatal("current routine replacement did not update transitive demand")}
		if classify(1){t.Fatal("non-key UPDATE reached incoming key action")}
	})
	t.Run("cyclic-closure",func(t *testing.T){
		e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_fk_self(id INT PRIMARY KEY,parent INT REFERENCES sync_fk_self(id) ON UPDATE CASCADE ON DELETE CASCADE)")
		s:=snapshotSyncScope(t,e,false,session.ReadCommitted);s.StatementMutation.Statement=&parser.Update{}
		ref:=enginetest.RoleTableRef(t,e,"sync_fk_self")
		e.Mu.Lock();relation,_:=e.Catalog.State.Relation(ref.Address)
		b:=analyze.Binding{Ref:ref,Relation:relation,Targets:[]int{0}}
		core.ScopeInstallExportDemand(s,&b);e.Mu.Unlock()
		if core.ScopeExportRootDemand(s){t.Fatal("pure cyclic FK closure invented export demand")}
	})
}

func TestSnapshotSyncForeignActionPair(t *testing.T){
	for _,isolation:=range []session.Isolation{session.ReadCommitted,session.RepeatableRead}{
		t.Run(session.IsolationText(isolation),func(t *testing.T){
			e:=engine.New()
			for _,query:=range []string{
				"CREATE TABLE sync_pair_parent(id TEXT PRIMARY KEY)",
				"CREATE TABLE sync_pair_child(fk TEXT DEFAULT pg_export_snapshot() REFERENCES sync_pair_parent(id) ON DELETE SET DEFAULT)",
				"CREATE TABLE sync_pair_unread(id INT)",
			}{enginetest.Run(t,e,query)}
			s:=snapshotSyncScope(t,e,true,isolation)
			if err:=core.ScopeSnapshotDataPhase(s);err!=nil{t.Fatal(err)}
			parentRoots,_,parentSnapshot,parentFrontier:=core.ScopeExportCapture(s)
			enginetest.Run(t,e,"INSERT INTO sync_pair_unread VALUES(1)")
			read:=e.CatalogRead()
			parentRef:=enginetest.RoleTableRef(t,e,"sync_pair_parent")
			childRef:=enginetest.RoleTableRef(t,e,"sync_pair_child")
			parent,_:=read.Relation(parentRef.Address);child,_:=read.Relation(childRef.Address)
			job:=dml.ForeignKeyJob{Parent:parent,Child:child,Definition:child.Constraints.ForeignKeys[0]}
			restore,err:=s.WriteHost().BeginForeignActionSnapshot(&job,dml.ForeignActionTransition{},parser.ForeignKeySetDefault)
			if err!=nil{t.Fatal(err)}
			childRoots,_,childSnapshot,childFrontier:=core.ScopeExportCapture(s)
			if isolation==session.ReadCommitted{
				if restore==nil||childSnapshot==parentSnapshot||childFrontier<=parentFrontier||reflect.ValueOf(childRoots).Pointer()==reflect.ValueOf(parentRoots).Pointer(){t.Fatal("generated action did not acquire a fresh coherent pair")}
				restore()
			}else if restore!=nil||childSnapshot!=parentSnapshot||childFrontier!=parentFrontier{t.Fatal("stable action replaced transaction pair")}
			restoredRoots,_,restoredSnapshot,restoredFrontier:=core.ScopeExportCapture(s)
			if restoredSnapshot!=parentSnapshot||restoredFrontier!=parentFrontier||reflect.ValueOf(restoredRoots).Pointer()!=reflect.ValueOf(parentRoots).Pointer(){t.Fatal("action unwind replaced parent authority")}
		})
	}
}
