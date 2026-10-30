package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func explainAnalyzeProtocolOwnership(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE TABLE ea_protocol(v integer)")
	enginetest.SessionSQL(t,s,"CREATE SEQUENCE ea_protocol_s")
	sequence:=enginetest.CatalogRef(t,e,e.CatalogRead(),"ea_protocol_s")
	called:=func()bool{e.Mu.Lock();defer e.Mu.Unlock();return e.Storage.Sequences[sequence.Address].Called}
	enginetest.SessionSQL(t,s,"BEGIN")
	cycle:=s.NewProtocolCycle()
	command:=func(fn func(*core.StatementScope)error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
	command(func(scope *core.StatementScope)error{
		_,err:=scope.ParseProtocol("ea_definition","EXPLAIN ANALYZE INSERT INTO ea_protocol VALUES(nextval('ea_protocol_s')),(nextval('ea_protocol_s')) RETURNING v",nil)
		return err
	})
	registry:=core.PayloadOf(s).PreparedSQL
	definition:=registry.Lookup("ea_definition")
	owner:=definition.Description.Folds
	if called() || owner==nil || owner.Live() || owner.Current().Generation!=0 {t.Fatal("Parse executed child or gained fold rights")}
	command(func(scope *core.StatementScope)error{
		_,err:=scope.BindProtocolPortal("ea_portal","ea_definition",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}})
		return err
	})
	binding:=registry.Binding("ea_portal")
	if called() || binding==nil || binding.Resource.Root!=nil || owner.Live() {t.Fatal("Bind executed or retained child graph")}
	command(func(scope *core.StatementScope)error{
		description,err:=scope.DescribeProtocolPortal("ea_portal")
		if err==nil && (len(description.Columns)!=1 || description.Columns[0].Name!="QUERY PLAN" || description.Columns[0].Usage.Type.ID()!=sqltype.Text){t.Fatal(description.Columns)}
		return err
	})
	finished:=false
	for pulls:=0;!finished;pulls++ {
		if pulls>100 {t.Fatal("plan portal did not settle")}
		command(func(scope *core.StatementScope)error{
			output,err:=scope.ExecuteProtocolPortal("ea_portal",1,func(fields []session.ProtocolField)error{if len(fields)!=1 {t.Fatal("child columns escaped",fields)};return nil},nil,nil,core.CopyProtocolHooks{})
			if err!=nil {return err}
			if !called() || core.ExplainActiveForTest(scope){t.Fatal("suspended output retained/skipped child execution")}
			if !output.Suspended {
				if output.CommandTag!="EXPLAIN" {t.Fatal(output)}
				finished=true
			}
			return nil
		})
	}
	if binding.Resource.Root!=nil || owner.Live() || owner.Current().Generation!=0 || len(owner.Current().Entries)!=0 {t.Fatal("protocol execution published retained child artifacts")}
	command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal("ea_portal")})
	command(func(scope *core.StatementScope)error{return scope.CloseProtocolStatement("ea_definition")})
	op,_,err:=s.AcquireCommand(context.Background())
	if err!=nil {t.Fatal(err)}
	err=e.SyncProtocolCycle(op,cycle)
	op.Done()
	if err!=nil {t.Fatal(err)}
	enginetest.TransactionRows(t,s,"SELECT v FROM ea_protocol ORDER BY v",1,2)
	enginetest.SessionSQL(t,s,"ROLLBACK")
	enginetest.TransactionRows(t,s,"SELECT v FROM ea_protocol")
	if !called() {t.Fatal("rollback incorrectly undid sequence effects")}
	enginetest.LockEmpty(t,e)
}
