package core_test

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"slices"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type xmlScopeProducerCancel struct{context.Context;cancel context.CancelFunc;scope *core.StatementScope;portal *core.ProtocolPortal;seen bool;polls xmlScopePolls}
func (c *xmlScopeProducerCancel)Err()error{
	c.polls.observe(c.scope,c.portal)
	if !c.seen&&c.scope!=nil&&core.ScopeActiveSQLRoutineDepth(c.scope)>0&&c.scope.View!=nil&&c.scope.Expected!=nil&&len(c.scope.Expected.Results)==1&&c.scope.Expected.Results[0].Name=="a"{c.seen=true;c.cancel()}
	return c.Context.Err()
}

func TestXMLMapScopePreparedOwnership(t *testing.T){
	t.Run("caller-frontier",func(t *testing.T){testXMLMappingPreparationVisibility(t,true)})
	t.Run("expression-actor",xmlMapScopeExpressionActorOwnership)
	t.Run("held-authority",xmlMapScopeHeldAuthorityOwnership)
	for _,outcome:=range []string{"success","error","cancel"}{t.Run(outcome,func(t *testing.T){
		e:=engine.NewWithSettings(nil,"xml_scope_owned",false);s:=enginetest.StartSession(t,e,nil)
		for _,q:=range []string{`CREATE SCHEMA xs_owned`,`CREATE TABLE xs_owned.t(a int)`,`INSERT INTO xs_owned.t VALUES(1)`,`CREATE SCHEMA xs_owned_bad`,`CREATE VIEW xs_owned_bad.t AS SELECT 1/0 AS a`}{enginetest.SessionSQL(t,s,q)}
		transaction:=enginetest.TransactionBegin(t,s);defer enginetest.TransactionEnd(t,s,transaction,false)
		cycle:=s.NewProtocolCycle();command:=func(fn func(*core.StatementScope)error){enginetest.PreparedFoldProtocolCommand(t,e,s,cycle,fn)}
		query:=`SELECT schema_to_xml($1::name,false,true,'') AS mapped,xmltext('owned'::text) AS folded_xml,length('owned') AS folded_length FROM (VALUES(1),(2)) AS input(n)`
		if outcome=="error"{query=`SELECT schema_to_xml(CASE WHEN n=1 THEN $1::name ELSE 'xs_owned_bad'::name END,false,true,'') AS mapped,xmltext('owned'::text) AS folded_xml,length('owned') AS folded_length FROM (VALUES(1),(2)) AS input(n)`}
		command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("scope_owned",query,[]uint32{19});return err})
		registry:=core.PayloadOf(s).PreparedSQL;definition:=registry.Lookup("scope_owned");if definition==nil||definition.Description.Analysis==nil||definition.Description.Folds==nil{t.Fatal("missing real scope definition")}
		analysis,owner:=definition.Description.Analysis,definition.Description.Folds
		packet:=[]byte{0,0,0,1};packet=binary.BigEndian.AppendUint32(packet,uint32(len("xs_owned")));packet=append(packet,"xs_owned"...);packet=append(packet,0,0)
		command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal("scope_portal","scope_owned",&enginetest.ProtocolReader{Data:packet});return err})
		binding:=registry.Binding("scope_portal");if binding==nil||binding.Resource==nil||binding.Resource.Root==nil||binding.Folds==nil{t.Fatal("missing actual accepted scope graph")}
		resource,graph,holder:=binding.Resource,binding.Resource.Root,binding.Folds;accepted:=*holder
		if len(graph.Expressions)!=3||len(accepted.Entries)==0||definition.Description.Parameters.Count!=1||definition.Description.Results[0].Usage.Type.ID()!=sqltype.XML{t.Fatalf("missing typed scope artifacts: width=%d generation=%d folds=%d parameters=%d result=%v",len(graph.Expressions),accepted.Generation,len(accepted.Entries),definition.Description.Parameters.Count,definition.Description.Results[0].Usage.Type.ID())}
		recipe,ok:=graph.Expressions[0].(functions.XMLMapping);if !ok||recipe.Operation!=functions.XMLSchemaRows||recipe.CallRoutineReference()==nil||recipe.CallRoutineReference().RoutineOID()!=2933{t.Fatal("missing accepted scope provider")}
		children:=slices.Clone(recipe.Children());entries:=slices.Clone(accepted.Entries);dependencies:=slices.Clone(analysis.Dependencies)
		// Parameterized AcceptFolds retains a binding-private group; generation
		// zero is valid (prepared_runtime.go), not an unpublished sentinel.
		ownerGeneration:=owner.Current().Generation
		expressions:=slices.Clone(graph.Expressions)
		// Keep XML construction in the real graph and use the same ordinary
		// accepted scalar-fold witness as the row-mapper owner proof.
		lengthFold:=false
		for _,entry:=range accepted.Entries{if entry.Result.Type.ID()==sqltype.Int4{n,err:=entry.Value.Integer();if err==nil&&n==5&&len(entry.Inputs)==1{input,err:=entry.Inputs[0].Value.Text();lengthFold=lengthFold||err==nil&&input=="owned"}}}
		if !lengthFold{t.Fatal("missing genuine length fold with its typed owned input")}
		assertAccepted:=func(){t.Helper();if accepted.Generation!=holder.Generation||&accepted.Entries[0]!=&holder.Entries[0]||!reflect.DeepEqual(analysis.Dependencies,dependencies)||!reflect.DeepEqual(expressions,graph.Expressions)||!reflect.DeepEqual(recipe,graph.Expressions[0])||!reflect.DeepEqual(children,recipe.Children()){t.Fatal("scope recipe/generation/backing changed")};for i,entry:=range accepted.Entries{if !enginetest.SamePublishedFold(entry,entries[i])||!sqltype.SameDatum(entry.Value,entries[i].Value)||entry.Source==entries[i].Source&&entry.Occurrence!=entries[i].Occurrence{t.Fatal("accepted provenance/datum mutated",i)};if len(entry.Inputs)>0&&&entry.Inputs[0]!=&entries[i].Inputs[0]{t.Fatal("operand backing changed")}}}
		// Reject a genuine candidate publication on cancellation without changing
		// the real accepted description or either retained datum backing.
		op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)}
		local:=enginetest.CloneFoldDescription(t,definition.Description)
		localGeneration:=local.Folds.Current().Generation
		e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);var environment core.PreparedEnvironment;if err==nil{environment,err=e.PreparedEnvironmentLocked(auth,true)};var publication *core.FoldPublication;if err==nil{publication,err=core.BeginFoldPublication(&local,definition.Body,environment,auth.Catalog,binding)}
		if err!=nil||publication==nil{e.Mu.Unlock();op.Done();t.Fatal("real publication",err)}
		base,cancel:=context.WithCancel(context.Background());cancel();err=publication.Accept(base,auth.Catalog,environment,auth.Catalog.Epoch());e.Mu.Unlock();op.Done()
		if !errors.Is(err,context.Canceled){t.Fatal("publication lost cancellation",err)}
		if publication.Group.Sealed{t.Fatal("canceled fixed publication sealed its candidate")}
		if got:=owner.Current().Generation;got!=ownerGeneration{t.Fatalf("canceled publication changed original definition generation: got %d want %d",got,ownerGeneration)}
		if got:=local.Folds.Current().Generation;got!=localGeneration{t.Fatalf("canceled publication changed cloned definition generation: got %d want %d",got,localGeneration)}
		assertAccepted()
		portal,ok:=resource.Closer.(*core.ProtocolPortal);if !ok{t.Fatalf("scope resource closer %T is not the actual protocol portal",resource.Closer)}
		var cancelObservation *xmlScopeProducerCancel
		execute:=func(cancelProducer bool)(int,error,bool){
			base,stop:=context.WithCancel(context.Background());defer stop();observed:=&xmlScopeProducerCancel{Context:base,cancel:stop,portal:portal}
			if cancelProducer{cancelObservation=observed}
			op,_,err:=s.AcquireCommand(base);if err!=nil{return 0,err,false};scope,err:=e.StartProtocolCommand(op,cycle);if err!=nil{op.Done();return 0,err,false};observed.scope=scope
			// Install the observer on the actual admitted child, preserving its
			// cancellation/deadline/value transport instead of polling its parent.
			if cancelProducer{observed.Context=op.Context();op.Ctx=observed}
			count:=0;_,err=scope.ExecuteProtocolPortal("scope_portal",1,func(_ []session.ProtocolField)error{count++;return nil},nil,nil,core.CopyProtocolHooks{})
			if core.ScopeActiveSQLRoutineDepth(scope)!=0{t.Fatal("scope producer leaked active depth")}
			err=scope.Finish(err);op.Done();return count,err,observed.seen
		}
		if count,err,_:=execute(false);err!=nil||count!=1{t.Fatal("initial actual scope producer",count,err)};assertAccepted()
		count,err,seen:=execute(outcome=="cancel")
		switch outcome{case "success":if err!=nil||count!=1{t.Fatal("scope reuse",count,err)};command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal("scope_portal")})
		case "error":var d *sqlstate.Error;if !errors.As(err,&d)||d.Code!="22012"||count!=0{t.Fatal("producer primary failure",count,err)}
		case "cancel":var d *sqlstate.Error;if !seen||count!=0||!(errors.Is(err,context.Canceled)||errors.As(err,&d)&&d.Code=="57014"){t.Fatal("actual producer cancellation",seen,count,err,cancelObservation.polls.String())}}
		if resource.Root!=nil{t.Fatal("terminal scope retained live graph")};assertAccepted()
		if registry.Lookup("scope_owned")!=definition||definition.Description.Analysis!=analysis||definition.Description.Folds!=owner||owner.Current().Generation!=ownerGeneration||accepted.Entries[0].Value.Type()==nil{t.Fatal("terminal scope mutated definition/accepted ownership")}
	})}
}
