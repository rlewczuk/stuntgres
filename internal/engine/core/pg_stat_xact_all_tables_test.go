package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatXactAllTablesCatalogIdentity(t *testing.T) {
	e:=engine.New();read:=e.CatalogRead();address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatXactAllTablesOID};view:=read.MustRelation(address)
	if pgcatalog.StatXactAllTablesOID!=12151||view.RowType!=12153||!view.VirtualView||len(view.Columns)!=12{t.Fatal("exact view identity",view)}
	names:=strings.Fields("relid schemaname relname seq_scan seq_tup_read idx_scan idx_tup_fetch n_tup_ins n_tup_upd n_tup_del n_tup_hot_upd n_tup_newpage_upd")
	for i,c:=range view.Columns{kind:=sqltype.Int8;if i==0{kind=sqltype.Oid}else if i<3{kind=sqltype.Name};if c.Name!=names[i]||c.Usage.Type.ID()!=kind||c.Usage.Typmod!=-1||!c.Nullable||!c.NullabilityKnown||c.OriginTable!=12151||c.OriginAttribute!=int16(i+1){t.Fatal("typed origin",i,c)}}
	row,ok:=read.LookupType(sqltype.ID(12153));if !ok||row.AssociatedArray()!=sqltype.ID(12152){t.Fatal("row/array identity",row)}
	registered:=0;for _,spec:=range pgcatalog.Specs{if spec.Name=="pg_stat_xact_all_tables"{registered++;if spec.OID!=12151{t.Fatal("source OID",spec)}}};if registered!=1{t.Fatal("source multiplicity",registered)}
	def:=pgcatalog.SummaryViewDefinition(pgcatalog.StatXactAllTablesOID);for _,term:=range []string{"GROUP BY","sum(pg_stat_get_xact_numscans(i.indexrelid))","pg_stat_get_xact_tuples_fetched(c.oid)"}{if !strings.Contains(def,term){t.Fatal("released definition",term,def)}}
}

func TestPGStatXactAllTablesOwnership(t *testing.T) {
	e:=engine.New();sess:=enginetest.StartSession(t,e,nil)
	statsFunctionExec(t,sess,"CREATE TABLE xat_owner(n integer)","BEGIN","SELECT n FROM xat_owner","SAVEPOINT child","INSERT INTO xat_owner VALUES(7)","ROLLBACK TO child","RELEASE child")
	key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"xat_owner")}
	var historical,peer statistics.Writer;historical.Add(&e.Statistics,key,statistics.Fact{Inserts:11});historical.Flush(&e.Statistics,time.Now(),true);peer.Add(&e.Statistics,key,statistics.Fact{Inserts:17})
	op:=enginetest.TransactionOperation(t,sess);defer op.Done();scope,err:=e.StartStatement(op);if err!=nil{t.Fatal(err)};defer scope.Finish(nil)
	scope.Owner.Stats.Record(key,statistics.Fact{Inserts:2},0);scope.Stage.Stats.Record(key,statistics.Fact{Inserts:1},0)
	e.Mu.Lock();defer e.Mu.Unlock();auth,err:=e.AuthorizationLocked(op);if err!=nil{t.Fatal(err)}
	runtime:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:sess.Settings})
	want:=pgcatalog.XactRelationFact{Inserts:4,SeqScans:1};if got:=runtime.XactRelationStatistics(key.Object.Address);got!=want{t.Fatal("pending aborted child/access plus live prefix/suffix once, no peer/history",got,want)}
	retained:=runtime.XactRelationStatistics(key.Object.Address)
	scope.Stage.Stats.Record(key,statistics.Fact{Updates:1,Deletes:1},1)
	want.Updates,want.Deletes=1,1;if got:=runtime.XactRelationStatistics(key.Object.Address);got!=want{t.Fatal("live nested frame",got,want)}
	if retained!=(pgcatalog.XactRelationFact{Inserts:4,SeqScans:1}){t.Fatal("copied local fact mutated",retained)}
	// Same-level truncate replaces current counts, not the saved pre-truncate
	// prefix. CurrentCounts remains the one authority for this reconciliation.
	scope.Stage.Stats.Release(0)
	scope.Stage.Stats.Record(key,statistics.Fact{Truncated:true},0)
	scope.Stage.Stats.Record(key,statistics.Fact{Inserts:4},0)
	if got:=runtime.XactRelationStatistics(key.Object.Address);got.Inserts!=5{t.Fatal("truncate used saved tuple counters or lost aborted child",got)}
	missing:=key.Object.Address;missing.ObjectID=ids.OID(4294967295);if got:=runtime.XactRelationStatistics(missing);got!=(pgcatalog.XactRelationFact{}){t.Fatal("missing identity leaked events",got)}
	scope.Owner.Stats=nil;scope.Stage.Stats=nil
	e.Statistics.Reset(time.Now())
	if got:=runtime.XactRelationStatistics(key.Object.Address);got!=(pgcatalog.XactRelationFact{}){t.Fatal("provider reset leaked pending access/aborted tuple batch",got)}
}

type xactSelectionRuntime struct {
	pgcatalog.Runtime
	seen map[ids.ObjectAddress]int
}
func (r *xactSelectionRuntime) XactRelationStatistics(a ids.ObjectAddress) pgcatalog.XactRelationFact {
	r.seen[a]++
	return r.Runtime.XactRelationStatistics(a)
}

func TestPGStatXactAllTablesSourceSelection(t *testing.T) {
	for _,column:=range []int{0,1,2}{t.Run([]string{"relid","schema","name"}[column],func(t *testing.T){
		e:=engine.New();sess:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,sess,"CREATE SCHEMA xat_other","CREATE TABLE xat_x(n integer)","CREATE TABLE xat_other.xat_y(n integer)","CREATE INDEX xat_xi ON xat_x(n)","CREATE INDEX xat_yi ON xat_other.xat_y(n)")
		x,y:=enginetest.RoleTableRef(t,e,"xat_x"),enginetest.RoleTableRef(t,e,"xat_other.xat_y");yi:=enginetest.RoleTableRef(t,e,"xat_other.xat_yi")
		configureStats(sess,"snapshot");enginetest.TransactionBegin(t,sess)
		op:=enginetest.TransactionOperation(t,sess);defer op.Done();scope,err:=e.StartStatement(op);if err!=nil{t.Fatal(err)};defer scope.Finish(nil)
		timestampType:=enginetest.Usage(e,sqltype.Timestamptz).Type
		e.Mu.Lock();defer e.Mu.Unlock();auth,err:=e.AuthorizationLocked(op);if err!=nil{t.Fatal(err)};read:=auth.Catalog
		runtime:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:sess.Settings});wrapped:=&xactSelectionRuntime{Runtime:runtime,seen:make(map[ids.ObjectAddress]int)}
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatXactAllTablesOID};in:=pgcatalog.Input{Context:context.Background(),Catalog:read,Roles:auth.Roles,Owner:&e.Catalog.Owner,Ref:e.Catalog.Domain.Ref(read.Epoch(),a),Source:read.MustRelation(a),Runtime:wrapped}
		in.Keep=pgcatalog.Keep{Column:column,Test:func(v sqltype.Value)bool{if column==0{n,_:=v.OID();return ids.OID(n)==x.Address.ObjectID};text,_:=v.Text();if column==1{return text=="public"};return text=="xat_x"}}
		produce,ok:=pgcatalog.ProducerFor(pgcatalog.StatXactAllTablesOID);if !ok{t.Fatal("missing producer")}
		rows,err:=produce(in);if err!=nil||len(rows)!=1{t.Fatal("selected source",len(rows),err)}
		if wrapped.seen[x.Address]!=1||wrapped.seen[y.Address]!=0||wrapped.seen[yi.Address]!=0{t.Fatal("predicate ran after table/index inquiry",wrapped.seen)}
		retained:=append([]sqltype.Value(nil),rows[0]...)
		scope.Stage.Stats.Record(statistics.Key{Object:x},statistics.Fact{Inserts:2},0)
		fresh,err:=produce(in);if err!=nil{t.Fatal(err)};n,_:=fresh[0][7].Integer();if n!=2||!reflect.DeepEqual(rows[0],retained){t.Fatal("retained values recomputed",n)}
		ctx:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
		timestamp,err:=ctx.StatsSnapshotValue(false,timestampType);if err!=nil||!timestamp.IsNull(){t.Fatal("xact projection created cumulative snapshot",timestamp,err)}
		runtime.TableStatistics(x.Address)
		cut,err:=ctx.StatsSnapshotValue(false,timestampType);if err!=nil||cut.IsNull(){t.Fatal("missing established cumulative cut",cut,err)}
		if _,err:=produce(in);err!=nil{t.Fatal(err)}
		still,err:=ctx.StatsSnapshotValue(false,timestampType);if err!=nil||!reflect.DeepEqual(cut,still){t.Fatal("xact inquiry changed established cumulative cut",cut,still,err)}
		cancelled,cancel:=context.WithCancel(context.Background());in.Context=cancelled;in.Keep=pgcatalog.Keep{Column:2,Test:func(sqltype.Value)bool{cancel();return true}}
		partial,err:=produce(in);if err!=context.Canceled||partial!=nil{t.Fatal("canceled producer retained partial rows",len(partial),err)}
		in.Context=context.Background();in.Owner=nil;if partial,err:=produce(in);err==nil||partial!=nil{t.Fatal("invalid capture accepted")}
	})}
}
