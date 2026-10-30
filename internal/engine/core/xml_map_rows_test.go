package core_test

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Public SQL outcomes live in reference/strings. This test retains the actual
// accepted definition, graph and fold backing across a real producer failure.
func TestXMLMapRowsPreparedOwnership(t *testing.T){
	e:=rootengine.New();s:=enginetest.StartSession(t,e,nil)
	transaction:=enginetest.TransactionBegin(t,s);defer enginetest.TransactionEnd(t,s,transaction,false)
	cycle:=s.NewProtocolCycle()
	command:=func(fn func(*core.StatementScope)error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
	command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("xml_map_owner",`SELECT query_to_xml(CASE WHEN n=1 THEN $1::text ELSE 'SELECT 1/0 AS a' END,false,false,'') AS mapped,length('owned') AS folded FROM (VALUES(1),(2)) AS input(n)`,[]uint32{25});return err})
	inner:="SELECT 1 AS a";packet:=[]byte{0,0,0,1};packet=binary.BigEndian.AppendUint32(packet,uint32(len(inner)));packet=append(packet,inner...);packet=append(packet,0,0)
	command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal("xml_map_owner_portal","xml_map_owner",&protocolUnitInput{data:packet});return err})
	registry:=core.PayloadOf(s).PreparedSQL;definition:=registry.Lookup("xml_map_owner");binding:=registry.Binding("xml_map_owner_portal")
	if definition==nil||binding==nil||binding.Resource==nil||binding.Resource.Root==nil||binding.Folds==nil{t.Fatal("missing real accepted mapper graph")}
	graph,accepted:=binding.Resource.Root,*binding.Folds
	project:=graph;if len(project.Expressions)!=2{t.Fatalf("accepted project width %d",len(project.Expressions))}
	recipe,ok:=project.Expressions[0].(functions.XMLMapping);if !ok||recipe.Operation!=functions.XMLQueryRows||recipe.CallRoutineReference()==nil||recipe.CallRoutineReference().RoutineOID()!=2924{t.Fatal("missing genuine mapper provider",project.Expressions[0])}
	children:=recipe.Children()
	if len(accepted.Entries)==0||definition.Description.Parameters.Count!=1||definition.Description.Results[0].Usage.Type.ID()!=sqltype.XML{t.Fatal("missing accepted parameter/fold/result ownership")}
	first,generation:=&accepted.Entries[0],accepted.Generation
	execute:=func()(int,error){op,_,err:=s.AcquireCommand(context.Background());if err!=nil{return 0,err};scope,err:=e.StartProtocolCommand(op,cycle);if err!=nil{op.Done();return 0,err};observed:=0;_,err=scope.ExecuteProtocolPortal("xml_map_owner_portal",1,func(_ []session.ProtocolField)error{observed++;return nil},nil,nil,core.CopyProtocolHooks{});err=scope.Finish(err);op.Done();return observed,err}
	if count,err:=execute();err!=nil||count!=1{t.Fatal("initial genuine producer admission",count,err)}
	count,err:=execute();var diagnostic *sqlstate.Error
	if !errors.As(err,&diagnostic)||diagnostic.Code!="22012"||count!=0||binding.Resource.Root!=nil{t.Fatal("failed mapper retained or published producer",count,err)}
	if registry.Lookup("xml_map_owner")!=definition||accepted.Generation!=generation||&accepted.Entries[0]!=first||!reflect.DeepEqual(recipe,project.Expressions[0])||!reflect.DeepEqual(children,recipe.Children()){t.Fatal("failure mutated original accepted graph/fold backing")}
}

func TestXMLMapRowsCommandTags(t *testing.T) {
	for _, tc := range []struct{ sql, want string }{
		{`SELECT 1 INTO xr_into`, "SELECT INTO"},
		{`CREATE TABLE xr_into AS SELECT 1`, "CREATE TABLE AS"},
		{`CREATE TABLE xr_into AS SELECT 1 WITH NO DATA`, "CREATE TABLE AS"},
		{`CREATE MATERIALIZED VIEW xr_into AS SELECT 1`, "CREATE MATERIALIZED VIEW"},
		{`SELECT 1`, "SELECT"},
		{`BEGIN`, "BEGIN"},
		{`COMMIT`, "COMMIT"},
		{`ROLLBACK`, "ROLLBACK"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			statements, err := parser.Parse(tc.sql)
			if err != nil || len(statements) != 1 {
				t.Fatalf("parse command: statements=%d error=%v", len(statements), err)
			}
			if got := core.RoutineCommandTag(statements[0]); got != tc.want {
				t.Fatalf("semantic SPI tag for %T: got %q want %q", statements[0], got, tc.want)
			}
			if created, ok := statements[0].(*parser.CreateQueryTable); ok && !created.NoData {
				completion, _, _, _ := session.ClassifyCommandTag(created, session.TxIdle)
				if completion != "SELECT" {
					t.Fatalf("query-creation completion tag changed: %q", completion)
				}
			}
		})
	}
}
