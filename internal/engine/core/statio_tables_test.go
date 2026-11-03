package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func withStatioTableRuntime(t *testing.T,e *core.Engine,s *session.Session,body func(pgcatalog.Runtime,pgcatalog.Input,*core.StatementScope)) {
	t.Helper()
	op:=enginetest.TransactionOperation(t,s)
	scope,err:=e.StartStatement(op);if err!=nil {op.Done();t.Fatal(err)}
	defer op.Done()
	defer func(){if err:=scope.Finish(nil);err!=nil {t.Fatal("reader scope completion",err)}}()
	e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);e.Mu.Unlock();if err!=nil {t.Fatal(err)}
	read:=auth.Catalog;address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatioAllTablesOID}
	runtime:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:op.EffectiveSettings()})
	in:=pgcatalog.Input{Context:context.Background(),Catalog:read,Roles:auth.Roles,Owner:read.Owner(),Ref:auth.Ref(address),Source:read.MustRelation(address),Runtime:runtime}
	body(runtime,in,scope)
}

func TestStatioAllTablesReader(t *testing.T) {
	t.Run("shared_index_sum_not_generic_requests",func(t *testing.T) {
		e:=engine.New();statioSeed(t,e)
		parent:=enginetest.RoleTableRef(t,e,"statio_events")
		index,unused:=statioKey(t,e,"statio_events_i"),statioKey(t,e,"statio_events_unused")
		for n:=range int64(3) {
			writer:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,writer,"SET enable_seqscan=off")
			capture:=bufferUnitCapture(t,e,writer,"SELECT k FROM statio_events WHERE k=1")
			stage:=statioOpen(t,capture,plan.HashCandidates)
			row:=make([]sqltype.Value,1)
			if present,err:=stage.Next(capture.op.Ctx,row);err!=nil||!present {t.Fatal("actual hash lookup",present,err)}
			stage.Close();capture.close(t);statsFunctionRetire(t,writer)
			statioFact(t,e,index,1,n);statioFact(t,e,unused,0,0)
			observer:=enginetest.StartSession(t,e,nil)
			withStatioTableRuntime(t,e,observer,func(runtime pgcatalog.Runtime,in pgcatalog.Input,_ *core.StatementScope) {
				in.Keep=pgcatalog.Keep{Column:0,Test:func(v sqltype.Value)bool{id,_:=v.OID();return id==uint32(parent.Address.ObjectID)}}
				produce,_:=pgcatalog.ProducerFor(pgcatalog.StatioAllTablesOID)
				rows,err:=produce(in);if err!=nil||len(rows)!=1 {t.Fatal(rows,err)}
				fact:=e.Statistics.Read(statistics.Key{Object:parent,Category:statistics.Relation})
				for i,want:=range []int64{fact.BlocksFetched-fact.BlocksHit,fact.BlocksHit,1,n} {got,err:=rows[0][i+3].Integer();if err!=nil||got!=want {t.Fatal("independent heap/index facts",i,got,want,err)}}
				for _,value:=range rows[0][7:] {if !value.IsNull() {t.Fatal("absent TOAST direct and index counters are NULL",rows[0])}}
				if got:=runtime.IndexBlockStatistics(index.Object.Address);got!=(pgcatalog.IndexBlockFact{Read:1,Hit:n}) {t.Fatal("same index reader",got)}
			})
			statsFunctionRetire(t,observer)
		}
	})
	t.Run("actual_opens_modes_and_clear",func(t *testing.T) {
		for _,mode:=range []string{"none","cache","snapshot"} {t.Run(mode,func(t *testing.T) {
			e:=engine.New();enginetest.Run(t,e,"CREATE TABLE statio_table_reader(k integer)");enginetest.Run(t,e,"INSERT INTO statio_table_reader VALUES(1),(2)")
			ref:=enginetest.RoleTableRef(t,e,"statio_table_reader")
			s:=enginetest.StartSession(t,e,nil);configureStats(s,mode);enginetest.TransactionBegin(t,s)
			withStatioTableRuntime(t,e,s,func(runtime pgcatalog.Runtime,in pgcatalog.Input,scope *core.StatementScope) {
				if got:=runtime.TableBlockStatistics(ref.Address);got!=(pgcatalog.IndexBlockFact{}) {t.Fatal("initial",got)}
				for n:=range int64(3) {
					writer:=enginetest.StartSession(t,e,nil)
					capture:=bufferUnitCapture(t,e,writer,"SELECT k FROM statio_table_reader")
					// Admission/description has not invoked the physical source.
					blocksUnitPublished(t,e,ref,n)
					_,stage:=blocksUnitOpen(t,capture)
					if err:=stage.Drain(capture.op.Ctx);err!=nil {t.Fatal(err)}
					stage.Close();capture.close(t);statsFunctionRetire(t,writer)
					blocksUnitPublished(t,e,ref,n+1)
					want:=int64(0);if mode=="none" {want=n+1}
					if got:=runtime.TableBlockStatistics(ref.Address);got!=(pgcatalog.IndexBlockFact{Hit:want}) {t.Fatal("same shared Reader",mode,n,got,want)}
				}
				ctx:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
				if _,err:=ctx.StatsSnapshotValue(true,enginetest.Usage(e,sqltype.Void).Type);err!=nil {t.Fatal(err)}
				if got:=runtime.TableBlockStatistics(ref.Address);got!=(pgcatalog.IndexBlockFact{Hit:3}) {t.Fatal("clear refresh",got)}
				produce,_:=pgcatalog.ProducerFor(pgcatalog.StatioAllTablesOID)
				in.Keep=pgcatalog.Keep{Column:0,Test:func(v sqltype.Value)bool {n,_:=v.OID();return n==uint32(ref.Address.ObjectID)}}
				rows,err:=produce(in);if err!=nil || len(rows)!=1 {t.Fatal(rows,err)}
				read,_:=rows[0][3].Integer();hit,_:=rows[0][4].Integer();if read!=0||hit!=3 {t.Fatal("view reuses facts",rows)}
				for _,value:=range rows[0][5:] {if !value.IsNull() {t.Fatal("absent indexes and TOAST retain NULL",rows[0])}}
				// The core capture switch must carry the same admitted reader.
				e.Mu.Lock();captured,err:=core.EngineOpenCatalogSourceLocked(e,scope,in.Ref,in.Source,nil);if err!=nil {t.Fatal(err)};defer captured.Close()
				found:=false
				for _,row:=range captured.Anonymous {id,_:=row[0].OID();if id==uint32(ref.Address.ObjectID) {found=true;n,_:=row[4].Integer();if n!=3 {t.Fatal("runtime registration lost reader",row)}}}
				if !found {t.Fatal("core capture omitted table")}
			})
			statsFunctionExec(t,s,"ROLLBACK")
		})}
	})
	t.Run("arithmetic_not_hardcoded_zero",func(t *testing.T) {
		e:=engine.New();enginetest.Run(t,e,"CREATE TABLE statio_table_arithmetic(k integer)")
		ref:=enginetest.RoleTableRef(t,e,"statio_table_arithmetic")
		var writer statistics.Writer;writer.Add(&e.Statistics,statistics.Key{Object:ref,Category:statistics.Relation},statistics.Fact{BlocksFetched:7,BlocksHit:5});writer.Flush(&e.Statistics,time.Now(),true)
		s:=enginetest.StartSession(t,e,nil)
		withStatioTableRuntime(t,e,s,func(runtime pgcatalog.Runtime,in pgcatalog.Input,_ *core.StatementScope) {if got:=runtime.TableBlockStatistics(ref.Address);got!=(pgcatalog.IndexBlockFact{Read:2,Hit:5}) {t.Fatal("F-H,H",got)}})
	})
	t.Run("policy_and_retired_identity",func(t *testing.T) {
		e:=engine.New();enginetest.Run(t,e,"CREATE TABLE statio_table_policy(k integer)");enginetest.Run(t,e,"INSERT INTO statio_table_policy VALUES(1)")
		old:=enginetest.RoleTableRef(t,e,"statio_table_policy")
		for _,enabled:=range []bool{false,true} {
			s:=enginetest.StartSession(t,e,nil);value:="off";if enabled {value="on"};statsFunctionExec(t,s,"SET track_counts="+value,"SET track_io_timing=off","SELECT k FROM statio_table_policy");statsFunctionRetire(t,s)
			want:=int64(0);if enabled {want=1};blocksUnitPublished(t,e,old,want)
		}
		enginetest.Run(t,e,"DROP TABLE statio_table_policy");enginetest.Run(t,e,"CREATE TABLE statio_table_policy(k integer)")
		next:=enginetest.RoleTableRef(t,e,"statio_table_policy");if next==old {t.Fatal("reused qualified identity")}
		s:=enginetest.StartSession(t,e,nil)
		withStatioTableRuntime(t,e,s,func(runtime pgcatalog.Runtime,_ pgcatalog.Input,_ *core.StatementScope) {if got:=runtime.TableBlockStatistics(next.Address);got!=(pgcatalog.IndexBlockFact{}) {t.Fatal("new history",got)}})
		blocksUnitPublished(t,e,old,0)
	})
	for _,kind:=range []string{"private","temp"} {t.Run(kind+"_generic_eligibility",func(t *testing.T) {
		e:=bufferUnitEngine(t);s:=enginetest.StartSession(t,e,nil);statsFunctionExec(t,s,"BEGIN")
		create:="CREATE TABLE statio_table_private(k integer)";if kind=="temp" {create="CREATE TEMP TABLE statio_table_private(k integer)"}
		statsFunctionExec(t,s,create,"INSERT INTO statio_table_private VALUES(1)")
		capture:=bufferUnitCapture(t,e,s,"SELECT k FROM statio_table_private");ref:=capture.source.Ref
		_,stage:=blocksUnitOpen(t,capture);if err:=stage.Drain(capture.op.Ctx);err!=nil {t.Fatal(err)};stage.Close();capture.close(t)
		blocksUnitPending(t,e,s,ref,1)
		// COMMIT publishes private identity and its actual-work facts; a TEMP
		// owner remains live until after the observer reads its generic facts.
		statsFunctionExec(t,s,"COMMIT")
		observer:=enginetest.StartSession(t,e,nil)
		withStatioTableRuntime(t,e,observer,func(runtime pgcatalog.Runtime,_ pgcatalog.Input,_ *core.StatementScope) {if got:=runtime.TableBlockStatistics(ref.Address);got!=(pgcatalog.IndexBlockFact{Hit:1}) {t.Fatal("generic eligibility inherited cache exclusions",kind,got)}})
		statsFunctionRetire(t,s)
	})}
}
