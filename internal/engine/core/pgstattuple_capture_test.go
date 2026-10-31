package core_test

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgstattupleHeapUnitExpression(t *testing.T,e *core.Engine,name,target string) plan.InstalledPgstattupleExpr {
	t.Helper()
	binder:=newTestExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
	raw:=parser.FunctionExpr{Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:name}}},Args:[]parser.Expr{parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue,Text:target}}}}
	node,err:=binder.BindFunctionProvider(raw)
	if err!=nil { t.Fatal(err) }
	return node.(plan.InstalledPgstattupleExpr)
}

func heapUnitCapture(e *core.Engine,s *session.Session,n plan.InstalledPgstattupleExpr,target string,oid ids.OID) (storage.HeapStatistics,error) {
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	op,err:=s.Acquire(ctx)
	if err!=nil { return storage.HeapStatistics{},err }
	defer op.Done()
	scope,err:=e.StartStatement(op)
	if err!=nil { return storage.HeapStatistics{},err }
	// Admit the real query work boundary without routing an SQL result witness
	// through an internal test. Public outcomes live in reference/extensions.
	err=scope.Gate(&parser.Select{})
	if err!=nil { return storage.HeapStatistics{},scope.Finish(err) }
	stats,err:=heapUnitScopeCapture(e,scope,n,target,oid)
	return stats,scope.Finish(err)
}

func heapUnitScopeCapture(e *core.Engine,scope *core.StatementScope,n plan.InstalledPgstattupleExpr,target string,oid ids.OID) (storage.HeapStatistics,error) {
	typ,ok:=e.CatalogRead().LookupType(n.Input)
	if !ok {return storage.HeapStatistics{},fmt.Errorf("missing input type")}
	text:=target
	if n.Input==sqltype.Regclass {text=strconv.FormatUint(uint64(oid),10)}
	value,err:=typ.Input(scope.Op.Context(),text,-1)
	if err!=nil {return storage.HeapStatistics{},err}
	return core.HostOf(scope).Pgstattuple(n,value)
}

func TestPgstattupleHeapCapture(t *testing.T) {
	t.Run("statement_atomic_owner_boundary",heapUnitStatementBoundary)
	t.Run("new_heap_owner_provenance",heapUnitNewHeapOwners)
	e:=engine.New()
	pgstatindexUnitInstalled(t,e)
	for _,q:=range []string{"CREATE TABLE capture_heap(n integer)","INSERT INTO capture_heap VALUES(1)","CREATE INDEX capture_index ON capture_heap(n)"} { enginetest.Run(t,e,q) }
	base:=pgstatindexCaptureFixture(t,e,nil,"capture_index")
	exact:=pgstattupleHeapUnitExpression(t,e,"pgstattuple","capture_heap")
	approx:=pgstattupleHeapUnitExpression(t,e,"pgstattuple_approx","capture_heap")
	writer,peer:=sessionStart(t,e,nil),sessionStart(t,e,nil)
	for _,q:=range []string{"BEGIN","UPDATE capture_heap SET n=2","UPDATE capture_heap SET n=3"} { sessionSQL(t,writer,q) }
	for _,tc:=range []struct{ owner *session.Session; call plan.InstalledPgstattupleExpr; live,dead int64 }{{writer,exact,1,2},{writer,approx,2,1},{peer,exact,3,0},{peer,approx,1,2}} {
		stats,err:=heapUnitCapture(e,tc.owner,tc.call,"capture_heap",base.Parent.Address().ObjectID)
		if err!=nil || stats.TupleCount!=tc.live || stats.DeadCount!=tc.dead { t.Fatal("actual owner capture",stats,err,tc.live,tc.dead) }
	}
	// Opposing captures acquire all owners in one token order, never each
	// caller first. A bounded barrier releases both while the writer stays open.
	start:=make(chan struct{})
	result:=make(chan error,2)
	var release sync.Once
	t.Cleanup(func(){ release.Do(func(){close(start)}) })
	for _,owner:=range []*session.Session{writer,peer} {
		go func(s *session.Session){ <-start; _,err:=heapUnitCapture(e,s,exact,"capture_heap",base.Parent.Address().ObjectID); result<-err }(owner)
	}
	release.Do(func(){close(start)})
	for range 2 {
		select { case err:=<-result: if err!=nil {t.Error(err)}; case <-time.After(5*time.Second): t.Fatal("opposing owner capture did not complete") }
	}
	t.Run("prepare_transfer_and_close",func(t *testing.T){
		private:=writer.Transactions.Writes[base.Parent.Address()][0].PrivateHistory
		start:=make(chan struct{})
		result:=make(chan error,3)
		closing:=sessionStart(t,e,nil)
		go func(){<-start; stats,err:=heapUnitCapture(e,peer,exact,"capture_heap",base.Parent.Address().ObjectID);if err==nil && (stats.TupleCount!=3 || stats.DeadCount!=0){err=fmt.Errorf("transfer capture %+v",stats)};result<-err}()
		go func(){
			<-start
			op,err:=writer.Acquire(context.Background())
			if err!=nil {result<-err;return}
			scope,err:=e.StartStatement(op)
			if err==nil {_,err=scope.Execute(&parser.TransactionControl{Kind:parser.KindPrepareTransaction,GID:"capture_transfer"},0,nil);err=scope.Finish(err)}
			op.Done()
			result<-err
		}()
		go func(){<-start;closing.RequestEnd(session.CleanClose);ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();result<-closing.WaitEnd(ctx)}()
		close(start)
		for range 3 {select{case err:=<-result:if err!=nil{t.Error(err)};case <-time.After(5*time.Second):t.Fatal("capture/prepare/close workers did not join")}}
		entry:=core.EnginePreparedRef(e).Lookup("capture_transfer")
		if entry==nil || entry.Writes[base.Parent.Address()][0].PrivateHistory!=private {t.Fatal("prepared transfer lost genuine private root")}
		stats,err:=heapUnitCapture(e,peer,approx,"capture_heap",base.Parent.Address().ObjectID)
		if err!=nil || stats.TupleCount!=1 || stats.DeadCount!=2 {t.Fatal("prepared dedup capture",stats,err)}
		if err:=preparedUnitControl(t,e,peer,parser.KindRollbackPrepared,"capture_transfer");err!=nil{t.Fatal(err)}
	})
	// No public GIN constructor is claimed: supply its released descriptor at
	// the actual native dispatch seam, with an existing genuine physical root.
	state:=catalog.CloneState(e.Catalog.State)
	gin:=&catalog.AccessMethod{ObjectAddress:ids.ObjectAddress{ClassID:ids.AccessMethodClassID,ObjectID:2742},ObjectName:"gin",ObjectRevision:1,Handler:333}
	state.SetDescriptor(gin.Address(),gin)
	target:=*base.Index
	definition:=*target.SQLIndex
	definition.MethodOID=2742
	target.SQLIndex=&definition
	target.ObjectName="native_gin"
	view:=e.Catalog.Read(state)
	for _,kind:=range []functions.HeapStatisticsKind{functions.HeapStatisticsExact,functions.HeapStatisticsApprox,functions.HeapStatisticsPages} {
		err:=core.PgstattupleTargetCheckable(view,&target,peer.SessionToken,kind)
		if kind==functions.HeapStatisticsPages { if err!=nil {t.Fatal(err)}; continue }
		diagnostic,ok:=err.(*sqlstate.Error)
		message:="index \"native_gin\" (gin index) is not supported"
		detail:=""
		if kind==functions.HeapStatisticsApprox { message="relation \"native_gin\" is of wrong relation kind"; detail="This operation is not supported for indexes." }
		if !ok || diagnostic.Code!="0A000" || diagnostic.Message!=message || diagnostic.Detail!=detail { t.Fatal(err) }
	}
	base.Index=&target
	stats,err:=index.CaptureTupleStatistics(context.Background(),base,true)
	if err!=nil || stats.TableLen<=0 || stats.TableLen%8192!=0 { t.Fatal("native GIN descriptor page dispatch",stats,err) }
}

func heapUnitStatementBoundary(t *testing.T) {
	e:=engine.New()
	pgstatindexUnitInstalled(t,e)
	for _,q:=range []string{"CREATE TABLE atomic_heap(n integer CHECK(n>0))","INSERT INTO atomic_heap VALUES(1)","CREATE INDEX atomic_index ON atomic_heap(n)"} {enginetest.Run(t,e,q)}
	base:=pgstatindexCaptureFixture(t,e,nil,"atomic_index")
	n:=pgstattupleHeapUnitExpression(t,e,"pgstattuple","atomic_heap")
	writer,peer:=sessionStart(t,e,nil),sessionStart(t,e,nil)
	sessionSQL(t,writer,"BEGIN")
	op:=transactionOperation(t,writer)
	scope,err:=e.StartStatement(op)
	if err!=nil {op.Done();t.Fatal(err)}
	_,err=scope.Execute(enginetest.Statement(t,"UPDATE atomic_heap SET n=2"),0,nil)
	if err!=nil {scope.Finish(err);op.Done();t.Fatal(err)}
	if len(scope.Stage.Rows[base.Parent.Address()])!=1 || len(writer.Transactions.Writes[base.Parent.Address()])!=0 {scope.Finish(nil);op.Done();t.Fatal("fixture did not stop at genuine private premerge boundary")}
	peerBefore,failure:=heapUnitCapture(e,peer,n,"atomic_heap",base.Parent.Address().ObjectID)
	ownDuring,ownFailure:=heapUnitScopeCapture(e,scope,n,"atomic_heap",base.Parent.Address().ObjectID)
	if peerBefore.TupleCount!=1 || peerBefore.DeadCount!=0 || failure!=nil || ownDuring.TupleCount!=1 || ownDuring.DeadCount!=1 || ownFailure!=nil {scope.Finish(nil);op.Done();t.Fatal("unpublished peer stage leaked, or caller stage was omitted",peerBefore,ownDuring,failure,ownFailure)}
	err=scope.Finish(nil)
	op.Done()
	if err!=nil {t.Fatal(err)}
	root:=writer.Transactions.Writes[base.Parent.Address()][0].PrivateHistory
	after,err:=heapUnitCapture(e,peer,n,"atomic_heap",base.Parent.Address().ObjectID)
	if err!=nil || after.TupleCount!=2 || after.DeadCount!=0 {t.Fatal("completed private command omitted",after,err)}
	sessionSQL(t,writer,"SAVEPOINT atomic_save")
	op=transactionOperation(t,writer)
	scope,err=e.StartStatement(op)
	if err!=nil {op.Done();t.Fatal(err)}
	_,failure=scope.Execute(enginetest.Statement(t,"UPDATE atomic_heap SET n=-1"),0,nil)
	failure=scope.Finish(failure)
	op.Done()
	if failure==nil {t.Fatal("actual failed statement was accepted")}
	sessionSQL(t,writer,"ROLLBACK TO atomic_save")
	if writer.Transactions.Writes[base.Parent.Address()][0].PrivateHistory!=root || base.Store.Retained!=nil {t.Fatal("failed command changed surviving owner or committed predecessor root")}
	after,err=heapUnitCapture(e,peer,n,"atomic_heap",base.Parent.Address().ObjectID)
	if err!=nil || after.TupleCount!=2 || after.DeadCount!=0 {t.Fatal("failed peer command leaked",after,err)}
	sessionSQL(t,writer,"ROLLBACK")
}

func heapUnitNewHeapOwners(t *testing.T) {
	e:=engine.New()
	pgstatindexUnitInstalled(t,e)
	for _,q:=range []string{"CREATE TABLE heap_owner_source(n integer)","INSERT INTO heap_owner_source VALUES(1)"} {enginetest.Run(t,e,q)}
	s:=sessionStart(t,e,nil)
	sessionSQL(t,s,"BEGIN")
	exact:=pgstattupleHeapUnitExpression(t,e,"pgstattuple","-")
	approx:=pgstattupleHeapUnitExpression(t,e,"pgstattuple_approx","-")
	for _,q:=range []string{"CREATE TABLE owner_ctas AS SELECT n FROM heap_owner_source","CREATE MATERIALIZED VIEW owner_mv AS SELECT n FROM heap_owner_source"} {sessionSQL(t,s,q)}
	for _,name:=range []string{"owner_ctas","owner_mv"} {
		var address ids.ObjectAddress
		for a,d:=range s.Transactions.Catalog.Descriptors {if r,ok:=d.(*catalog.Relation);ok && r.Name()==name {address=a}}
		if address==(ids.ObjectAddress{}) {t.Fatal("missing genuine private heap descriptor",name)}
		for _,tc:=range []struct{call plan.InstalledPgstattupleExpr;live,dead int64}{{exact,1,0},{approx,0,1}} {
			stats,err:=heapUnitCapture(e,s,tc.call,name,address.ObjectID)
			if err!=nil || stats.TupleCount!=tc.live || stats.DeadCount!=tc.dead {t.Fatal("private new-heap creator provenance",name,stats,err)}
		}
		if name=="owner_ctas" {
			sessionSQL(t,s,"DELETE FROM owner_ctas")
			for _,tc:=range []struct{call plan.InstalledPgstattupleExpr;live,dead int64}{{exact,0,1},{approx,1,0}} {
				stats,err:=heapUnitCapture(e,s,tc.call,name,address.ObjectID)
				if err!=nil || stats.TupleCount!=tc.live || stats.DeadCount!=tc.dead {t.Fatal("private new-heap end provenance",stats,err)}
			}
		}
	}
	sessionSQL(t,s,"ROLLBACK")
}

// Native DDL need not construct these targets: retain real storage and installed
// routines, changing only the immutable descriptor selected by production dispatch.
func TestPgstattupleHeapTargetDispatch(t *testing.T) {
	for _, nonheap := range []bool{false, true} {
		name := "invalid_index"
		if nonheap { name = "nonheap_table_am" }
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			pgstatindexUnitInstalled(t,e)
			for _, q := range []string{
				"CREATE TABLE dispatch_heap(n integer)",
				"INSERT INTO dispatch_heap VALUES(1)",
				"CREATE INDEX dispatch_index ON dispatch_heap(n)",
			} { enginetest.Run(t,e,q) }
			capture := pgstatindexCaptureFixture(t,e,nil,"dispatch_index")
			target := *capture.Index
			if nonheap {
				// A distinct table-AM identity uses the existing genuine heap
				// handler; pgstattuple still requires the released heap AM OID.
				method := accessMethodUnit(t,e,"dispatch_table_am",catalog.HeapAccessMethod,3)
				read := accessMethodPublish(t,e,method)
				target = *capture.Parent
				target.TableMethod = method.Address().ObjectID
				if err := catalog.ValidateRelationTableMethod(read,&target); err != nil { t.Fatal(err) }
			} else {
				definition := *target.SQLIndex
				definition.Valid = false
				target.SQLIndex = &definition
			}
			before := e.Catalog.State
			t.Cleanup(func() {
				e.Mu.Lock()
				e.Catalog.PublishLocked(before)
				e.Mu.Unlock()
			})
			state := catalog.CloneState(before)
			target.ObjectRevision++
			state.SetDescriptor(target.Address(),&target)
			e.Mu.Lock()
			e.Catalog.PublishLocked(state)
			e.Mu.Unlock()
			view := e.CatalogRead()
			for _, tc := range []struct {
				function string
				kind functions.HeapStatisticsKind
			}{
				{"pgstattuple",functions.HeapStatisticsExact},
				{"pgstattuple_approx",functions.HeapStatisticsApprox},
				{"pg_relpages",functions.HeapStatisticsPages},
			} {
				want := sqlstate.Error{Code:"0A000",Message:"only heap AM is supported"}
				if !nonheap {
					want = sqlstate.Error{Code:"55000",Message:`index "dispatch_index" is not valid`}
					if tc.kind == functions.HeapStatisticsApprox {
						// Wrong-kind admission precedes index validity for approx.
						want = sqlstate.Error{Code:"0A000",Message:`relation "dispatch_index" is of wrong relation kind`,Detail:"This operation is not supported for indexes."}
					}
				}
				err := core.PgstattupleTargetCheckable(view,&target,ids.SessionToken{},tc.kind)
				if tc.kind == functions.HeapStatisticsPages {
					if err != nil { t.Fatal("page descriptor admission",err) }
				} else {
					// Shared assertion compares the entire diagnostic, including
					// independently expected empty detail/location/context fields.
					amcheckUnitDiagnostic(t,err,want)
				}
				inputs := []string{"text","regclass"}
				if tc.kind == functions.HeapStatisticsApprox { inputs = []string{"regclass"} }
				for _, input := range inputs {
					query := fmt.Sprintf("SELECT * FROM %s('%s'::%s)",tc.function,target.Name(),input)
					err := amcheckUnitCall(t,e,query)
					if tc.kind == functions.HeapStatisticsPages {
						if err != nil { t.Fatal("page SQL admission",err) }
					} else {
						amcheckUnitDiagnostic(t,err,want)
					}
				}
			}
			s := sessionStart(t,e,nil)
			pages := pgstattupleHeapUnitExpression(t,e,"pg_relpages",target.Name())
			stats, err := heapUnitCapture(e,s,pages,target.Name(),target.Address().ObjectID)
			if err != nil || stats.TableLen<=0 || stats.TableLen%8192!=0 { t.Fatal("page host capture",stats,err) }
		})
	}
}
