package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestLtreeLqueryPreparedRecreationOwnership(t *testing.T) {
	for _,expression := range []string{
		"'a'::ltree ~ 'a'::lquery",
		"'a'::lquery ~ 'a'::ltree",
	} {
		t.Run(expression, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t,e,nil)
			for _,q := range []string{"CREATE SCHEMA provider", "CREATE EXTENSION ltree SCHEMA provider", "SET search_path=public,provider"} {
				enginetest.SessionSQL(t,s,q)
			}
			ast,description := sequencePreparedDescription(t,e,s,"SELECT "+expression+" AS matched")
			oldAnalysis := description.Analysis
			oldCaptures := append([]analyze.TemporalConstant(nil),description.Temporals...)
			if len(oldCaptures)!=2 { t.Fatal("real scalar operand captures missing",oldCaptures) }
			for _,capture := range oldCaptures {
				address := ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(capture.TypeID)}
				if oldAnalysis.Types[address]==nil || oldAnalysis.Types[address].SQLType!=capture.Value.Type() {
					t.Fatal("capture lost exact original type declaration")
				}
			}
			for _,q := range []string{"DROP EXTENSION ltree", "DROP SCHEMA provider", "CREATE SCHEMA provider"} {
				enginetest.SessionSQL(t,s,q)
			}
			// A failed candidate must not publish replacement values or provenance.
			if result,err := sequencePreparedExecute(e,s,ast,&description);err==nil {
				if result.Rows!=nil { result.Rows.Close() }
				t.Fatal("absent provider unexpectedly authorized held literal")
			}
			if !reflect.DeepEqual(description.Temporals,oldCaptures) || description.Analysis!=oldAnalysis {
				t.Fatal("failed reanalysis replaced accepted literal captures")
			}
			enginetest.SessionSQL(t,s,"CREATE EXTENSION ltree SCHEMA provider")
			result,err := sequencePreparedExecute(e,s,ast,&description)
			if err!=nil { t.Fatal(err) }
			got := enginetest.Tuples(result)
			result.Rows.Close()
			if len(got)!=1 || len(got[0])!=1 || got[0][0]!=true { t.Fatal("recreated held literal",got) }
			if description.Analysis==oldAnalysis || len(description.Temporals)!=len(oldCaptures) {
				t.Fatal("successful reanalysis did not publish a fresh complete owner")
			}
			for i,capture := range description.Temporals {
				old := oldCaptures[i]
				address := ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(capture.TypeID)}
				current,ok := e.CatalogRead().Type(address)
				if !ok || capture.TypeID==old.TypeID || capture.Value.Type()==old.Value.Type() || current.SQLType!=capture.Value.Type() || description.Analysis.Types[address]!=current || capture.Offset!=old.Offset {
					t.Fatal("replacement capture lacks fresh exact declaration/provenance",capture)
				}
			}
			for _,old := range oldCaptures {
				address := ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(old.TypeID)}
				if oldAnalysis.Types[address].SQLType!=old.Value.Type() || e.CatalogRead().Get(address)!=nil {
					t.Fatal("reanalysis changed or revived the original declaration")
				}
			}
		})
	}
}

func TestLtreeLqueryPreparedRetirementFences(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE EXTENSION ltree")
	_,description := sequencePreparedDescription(t,e,s,"SELECT 'a'::ltree ~ 'a'::lquery")
	original := append([]analyze.TemporalConstant(nil),description.Temporals...)
	if len(original)!=2 { t.Fatal("real operand captures missing",original) }
	enginetest.SessionSQL(t,s,"CREATE SCHEMA changed_namespace")
	enginetest.SessionSQL(t,s,"SET search_path=changed_namespace,public")
	op,err := s.Acquire(context.Background())
	if err!=nil { t.Fatal(err) }
	defer op.Done()
	e.Mu.Lock()
	auth,err := e.AuthorizationLocked(op)
	if err!=nil { e.Mu.Unlock();t.Fatal(err) }
	path,err := core.EngineSequenceEnvironmentLocked(e,auth)
	e.Mu.Unlock()
	if err!=nil { t.Fatal(err) }
	retire := func(d *core.Description) []analyze.TemporalConstant {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return core.EngineRetirePreparedInstalledScalarLiteralTemporalsLocked(e,auth,d,path,d.Temporals)
	}
	if got:=retire(&description); &got[0]!=&description.Temporals[0] {
		t.Fatal("namespace change retired a live source")
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	for _,capture := range original {
		address := ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(capture.TypeID)}
		if err:=stage.Remove(e.Catalog.Domain.Ref(auth.Catalog.Epoch(),address));err!=nil { t.Fatal(err) }
	}
	auth.Catalog=e.StageRead(stage)
	if got:=retire(&description);len(got)!=0 { t.Fatal("authentic absent sources not retired after namespace change",got) }
	sticky := description
	stickyAnalysis := *description.Analysis
	sticky.Analysis=&stickyAnalysis
	stickyAnalysis.NeedsReanalysis=true
	if got:=retire(&sticky);len(got)!=0 { t.Fatal("sticky invalidation prevented fresh literal analysis",got) }
	for _,namespaceOnly:=range []bool{false,true} {
		d:=description
		a:=*description.Analysis
		d.Analysis=&a
		if namespaceOnly { a.Path=path } else { a.Namespace=auth.Catalog.State().NamespaceGeneration() }
		if got:=retire(&d);len(got)!=0 {
			t.Fatal("independent namespace/path evolution failed to retire authentic absent sources",namespaceOnly,got)
		}
	}
	for _,tc := range []struct{
		name string
		change func(*core.Description,*core.Analysis)
	}{
		{"missing analysis",func(d *core.Description,_ *core.Analysis){d.Analysis=nil}},
		{"domain",func(_ *core.Description,a *core.Analysis){a.Domain=new(ids.Domain)}},
		{"epoch",func(_ *core.Description,a *core.Analysis){a.Epoch++}},
		{"session",func(_ *core.Description,a *core.Analysis){a.Session=a.Domain.Session(a.Session.ID()+1)}},
		{"private journal",func(_ *core.Description,a *core.Analysis){a.Private=new(session.Journal)}},
		{"discard generation",func(d *core.Description,_ *core.Analysis){d.PlanGeneration++}},
		{"sticky without namespace or path evolution",func(_ *core.Description,a *core.Analysis){
			a.Namespace=auth.Catalog.State().NamespaceGeneration()
			a.Path=path
			a.NeedsReanalysis=true
		}},
		{"missing declarations",func(_ *core.Description,a *core.Analysis){a.Types=nil}},
		{"missing dependencies",func(_ *core.Description,a *core.Analysis){a.Dependencies=nil}},
		{"foreign dependencies",func(_ *core.Description,a *core.Analysis){
			for i:=range a.Dependencies {a.Dependencies[i].Ref=new(ids.Domain).Ref(auth.Catalog.Epoch(),a.Dependencies[i].Ref.Address)}
		}},
		{"future dependencies",func(_ *core.Description,a *core.Analysis){
			for i:=range a.Dependencies {a.Dependencies[i].Ref=e.Catalog.Domain.Ref(auth.Catalog.Epoch()+1,a.Dependencies[i].Ref.Address)}
		}},
		{"stale dependencies",func(_ *core.Description,a *core.Analysis){
			for i:=range a.Dependencies {a.Dependencies[i].Ref=e.Catalog.Domain.Ref(auth.Catalog.Epoch()-1,a.Dependencies[i].Ref.Address)}
		}},
		{"wrong capture type",func(d *core.Description,_ *core.Analysis){
			for i:=range d.Temporals {d.Temporals[i].TypeID++}
		}},
		{"foreign source pointers",func(d *core.Description,_ *core.Analysis){
			for i:=range d.Temporals {
				source:=d.Temporals[i].Value.Type()
				constructor:=sqltype.Ltree
				if source.IsLquery(){constructor=sqltype.Lquery}
				foreign,err:=constructor(source.ID(),source.Name(),source.SQLName())
				if err!=nil {t.Fatal(err)}
				d.Temporals[i].Value=sqltype.Null(foreign)
			}
		}},
	}{
		t.Run(tc.name,func(t *testing.T){
			d:=description
			a:=*description.Analysis
			a.Dependencies=append([]catalog.ExpressionDependency(nil),a.Dependencies...)
			d.Analysis=&a
			d.Temporals=append([]analyze.TemporalConstant(nil),original...)
			tc.change(&d,&a)
			before:=append([]analyze.TemporalConstant(nil),d.Temporals...)
			got:=retire(&d)
			if len(got)!=len(before) || &got[0]!=&d.Temporals[0] || !reflect.DeepEqual(got,before) {
				t.Fatal("rejected provenance changed candidate captures",got)
			}
		})
	}
	if !reflect.DeepEqual(description.Temporals,original) {
		t.Fatal("retirement attempts mutated accepted capture backing")
	}
}
