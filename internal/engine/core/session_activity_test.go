package core_test

import (
	"context"
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestSessionActivityProtocolAuthorityAndFolds(t *testing.T) {
	e:=engine.New(); s:=enginetest.StartSession(t,e,nil); peer:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"BEGIN")
	cycle:=s.NewProtocolCycle()
	query:="/* retained λ */ SELECT current_query(), pg_backend_pid() FROM generate_series(1,2)"
	command:=func(fn func(*core.StatementScope)error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
	command(func(scope *core.StatementScope)error{ _,err:=scope.ParseProtocol("activity",query,nil); if scope.Op.OriginalSQL!=query || !scope.Op.SQLAvailable { t.Fatal("Parse lost provenance") }; return err })
	definition:=core.PayloadOf(s).PreparedSQL.Lookup("activity")
	owner:=definition.Description.Folds
	if owner==nil || owner.Current().Entries!=nil { t.Fatal("Parse evaluated runtime leaf") }
	for _,portal:=range []string{"first","second"} {
		command(func(scope *core.StatementScope)error{ _,err:=scope.BindProtocolPortal(portal,"activity",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}}); if scope.Op.OriginalSQL!=query || !scope.Op.SQLAvailable { t.Fatal("Bind lost source") }; return err })
	}
	accepted:=owner.Current()
	binding:=core.PayloadOf(s).PreparedSQL.Binding("first")
	root:=binding.Resource.Root
	if root==nil { t.Fatal("Bind failed to own graph") }
	analysis:=definition.Description.Analysis
	if analysis==nil || analysis.Domain==nil || analysis.Session!=s.Token() || len(analysis.Dependencies)==0 { t.Fatal("missing retained analysis provenance") }
	domain,epoch,dependency:=analysis.Domain,analysis.Epoch,&analysis.Dependencies[0]
	for range 2 {
		// Another physical session has no access to this command's source.
		op,_,err:=peer.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }; if op.SQLAvailable || op.OriginalSQL!="" { t.Fatal("peer inherited source") };op.OriginalSQL="peer";op.SQLAvailable=true;op.Done()
		command(func(scope *core.StatementScope)error{
			scope.Op.OriginalSQL="wrong";scope.Op.SQLAvailable=true
			rows:=0
			out,err:=scope.ExecuteProtocolPortal("first",1,func(fields []session.ProtocolField)error {
				rows++; if len(fields)!=2 || fields[0].Null || string(fields[0].Bytes)!=query { t.Fatal("extended source encoding",fields) };return nil
			},nil,nil,core.CopyProtocolHooks{})
			if err==nil && (rows!=1 || !out.Suspended || scope.Op.OriginalSQL!=query) { t.Fatal("resume provenance/demand",rows,out) };return err
		})
		if definition.Description.Folds!=owner || len(owner.Current().Entries)!=0 || owner.Current().Generation!=accepted.Generation || binding.Resource.Root!=root { t.Fatal("runtime captured/mutated retained artifacts") }
		if definition.Description.Analysis!=analysis || analysis.Domain!=domain || analysis.Epoch!=epoch || &analysis.Dependencies[0]!=dependency { t.Fatal("repeated execution replaced immutable analysis backing") }
	}
	command(func(scope *core.StatementScope)error{ return scope.CloseProtocolPortal("first") })
	command(func(scope *core.StatementScope)error{ return scope.CloseProtocolPortal("second") })
	enginetest.SessionSQL(t,s,"ROLLBACK")
}

func TestSessionActivityRuntimeIdentityBounds(t *testing.T) {
	for _,id:=range []ids.SessionID{math.MaxInt32,math.MaxInt32+1,math.MaxUint64} {
		e:=engine.New();s,err:=e.StartSession(context.Background(),session.SessionStart{ID:id});if err!=nil { t.Fatal(err) }
		op,_,err:=s.AcquireCommand(context.Background());if err!=nil { t.Fatal(err) }
		scope,err:=e.StartStatement(op);if err!=nil { t.Fatal(err) }
		ctx:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
		value,err:=ctx.SessionActivityValue(functions.ActivityBackendPID,enginetest.Usage(e,sqltype.Int4).Type)
		if id==math.MaxInt32 { n,failure:=value.Integer();if err!=nil || failure!=nil || n!=math.MaxInt32 { t.Fatal(value,err,failure) } } else if err==nil { t.Fatal("unrepresentable identity truncated",id,value) }
		if err:=scope.Finish(nil);err!=nil { t.Fatal(err) };op.Done();s.RequestEnd(session.CleanClose);if err:=s.WaitEnd(context.Background());err!=nil { t.Fatal(err) }
	}
	zero := &exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(&core.StatementScope{Op:&session.Operation{Session:&session.Session{}}})}}
	if _,err:=zero.SessionActivityValue(functions.ActivityBackendPID,enginetest.Usage(engine.New(),sqltype.Int4).Type);err==nil { t.Fatal("zero public identity accepted") }
	var detached exec.Context
	if _,err:=detached.SessionActivityValue(functions.ActivityQuery,nil);err==nil { t.Fatal("missing authority accepted") }
}
