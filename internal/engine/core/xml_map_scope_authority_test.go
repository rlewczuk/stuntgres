package core_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// Public reference scenarios assert all changed XML results. This complement
// keeps the real accepted recipe, definitions and fold payloads alive while
// later openings change current authority and namespace generations.
func xmlMapScopeHeldAuthorityOwnership(t *testing.T){
	e:=engine.NewWithSettings(nil,"xml_scope_held",false);s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{`CREATE ROLE xs_held_a`,`CREATE ROLE xs_held_b`,`CREATE SCHEMA xs_held`,`CREATE TABLE xs_held.t(a int)`,`INSERT INTO xs_held.t VALUES(1)`,`GRANT USAGE ON SCHEMA xs_held TO xs_held_a,xs_held_b`,`GRANT SELECT ON xs_held.t TO xs_held_a`,`SET ROLE xs_held_a`}{enginetest.SessionSQL(t,s,q)}
	transaction:=enginetest.TransactionBegin(t,s);defer enginetest.TransactionEnd(t,s,transaction,false)
	cycle:=s.NewProtocolCycle();command:=func(fn func(*core.StatementScope)error){enginetest.PreparedFoldProtocolCommand(t,e,s,cycle,fn)}
	command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("scope_held",`SELECT schema_to_xml($1::name,false,false,'') AS mapped,xmltext('owned'::text) AS folded_xml,length('owned') AS folded_length`,[]uint32{19});return err})
	registry:=core.PayloadOf(s).PreparedSQL;definition:=registry.Lookup("scope_held");if definition==nil{t.Fatal("missing held definition")}
	packet:=[]byte{0,0,0,1};packet=binary.BigEndian.AppendUint32(packet,uint32(len("xs_held")));packet=append(packet,"xs_held"...);packet=append(packet,0,0)
	bind:=func(name string)*core.PreparedBinding{t.Helper();command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal(name,"scope_held",&enginetest.ProtocolReader{Data:packet});return err});binding:=registry.Binding(name);if binding==nil||binding.Resource==nil||binding.Resource.Root==nil||binding.Folds==nil{t.Fatal("missing held accepted provider")};return binding}
	first:=bind("held_first");graph,holder:=first.Resource.Root,first.Folds;accepted:=*holder;entries:=slices.Clone(accepted.Entries);recipe,ok:=graph.Expressions[0].(functions.XMLMapping)
	firstResource:=first.Resource
	if !ok||recipe.Operation!=functions.XMLSchemaRows||len(entries)==0{t.Fatalf("missing owned scope recipe/folds: recipe=%T generation=%d entries=%d",graph.Expressions[0],accepted.Generation,len(entries))};children:=slices.Clone(recipe.Children())
	// Save the complete real graph, including XML construction and its separate
	// ordinary length fold; none is reconstructed for the ownership assertions.
	expressions:=slices.Clone(graph.Expressions)
	if len(expressions)!=3||len(definition.Description.Results)!=3{t.Fatal("missing complete held XML/fold projection")}
	lengthFold:=false;for _,entry:=range accepted.Entries{if entry.Result.Type.ID()==sqltype.Int4{n,err:=entry.Value.Integer();if err==nil&&n==5&&len(entry.Inputs)==1{input,err:=entry.Inputs[0].Value.Text();lengthFold=lengthFold||err==nil&&input=="owned"}}};if !lengthFold{t.Fatal("missing actual held length fold/typed input")}
	assertOriginal:=func(){t.Helper();if !reflect.DeepEqual(expressions,graph.Expressions)||!reflect.DeepEqual(recipe,graph.Expressions[0])||!reflect.DeepEqual(children,recipe.Children())||holder.Generation!=accepted.Generation||&holder.Entries[0]!=&accepted.Entries[0]{t.Fatal("later authority mutated retained scope graph/generation")};for i,entry:=range accepted.Entries{if !enginetest.SamePublishedFold(entry,entries[i])||!sqltype.SameDatum(entry.Value,entries[i].Value)||len(entry.Inputs)>0&&&entry.Inputs[0]!=&entries[i].Inputs[0]{t.Fatal("later authority mutated accepted datum/provenance",i)}}}
	execute:=func(name string){t.Helper();op,_,err:=s.AcquireCommand(context.Background());if err!=nil{t.Fatal(err)};scope,err:=e.StartProtocolCommand(op,cycle);if err!=nil{op.Done();t.Fatal(err)};count:=0;_,err=scope.ExecuteProtocolPortal(name,0,func(_ []session.ProtocolField)error{count++;return nil},nil,nil,core.CopyProtocolHooks{});depth:=core.ScopeActiveSQLRoutineDepth(scope);err=scope.Finish(err);op.Done();if err!=nil||count!=1||depth!=0{t.Fatal("current authority opening",count,depth,err)}}
	execute("held_first");command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal("held_first")});assertOriginal()
	for i,queries:=range [][]string{
		{`RESET ROLE`,`REVOKE SELECT ON xs_held.t FROM xs_held_a`,`SET ROLE xs_held_a`},
		{`RESET ROLE`,`GRANT SELECT ON xs_held.t TO xs_held_a`,`SET ROLE xs_held_b`},
		{`SET ROLE xs_held_a`},
		{`RESET ROLE`,`DROP SCHEMA xs_held CASCADE`,`CREATE SCHEMA xs_held`,`CREATE TABLE xs_held.t(a int)`,`INSERT INTO xs_held.t VALUES(8)`,`GRANT USAGE ON SCHEMA xs_held TO xs_held_a`,`GRANT SELECT ON xs_held.t TO xs_held_a`,`SET ROLE xs_held_a`},
	}{
		for _,query:=range queries{enginetest.SessionSQL(t,s,query)}
		name:=fmt.Sprintf("held_%d",i);binding:=bind(name);resource:=binding.Resource;if binding==first||resource==firstResource{t.Fatal("reused mutable portal authority")};execute(name);assertOriginal();command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal(name)});if resource.Root!=nil{t.Fatal("closed authority opening retained producer")};assertOriginal()
	}
}
