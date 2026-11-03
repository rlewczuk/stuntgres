package pgcatalog_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatioUserTablesSourceSelection(t *testing.T) {
	for _,mode:=range []string{"none","cache","snapshot"} {
		for _,slot:=range []int{-1,0,1,2} {t.Run(fmt.Sprintf("%s/key_%d",mode,slot),func(t *testing.T) {
			e:=engine.New();s:=enginetest.StartSession(t,e,nil)
			s.Settings.Mu.Lock();before:=s.Settings.Value("stats_fetch_consistency");s.Settings.Values["stats_fetch_consistency"]=mode;s.Settings.Typed["stats_fetch_consistency"]=settings.Value{Text:mode};s.Settings.StatsChanged(before);s.Settings.Mu.Unlock()
			enginetest.TransactionBegin(t,s);op:=enginetest.TransactionOperation(t,s);defer op.Done()
			scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)}
			defer func(){if err:=scope.Finish(nil);err!=nil {t.Error(err)}}()
			e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);e.Mu.Unlock();if err!=nil {t.Fatal(err)}
			state:=catalog.CloneState(auth.Catalog.State());ns:=ids.PublicNamespaceOID;schema:="public";if slot<0 {ns=ids.PGCatalogNamespaceOID;schema="pg_catalog"}
			d:=&catalog.Relation{ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:33500},ObjectNamespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ns},ObjectName:"user_table_reader_candidate",ObjectRevision:1,Owner:auth.State.CurrentUser,RelationKind:catalog.TableRelation}
			state.SetDescriptor(d.Address(),d)
			index:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:33501};state.SetDescriptor(index,&catalog.Relation{ObjectAddress:index,ObjectNamespace:d.Namespace(),ObjectName:"user_table_reader_index",ObjectRevision:1,Owner:d.Owner,RelationKind:catalog.IndexRelation,SQLIndex:&catalog.IndexDefinition{Parent:d.Address(),Keys:[]catalog.IndexKey{{Attribute:1}}}})
			auth.Recapture(e.Catalog.Read(state))
			input:=statioSysTableInput(t,e,auth,pgcatalog.StatioUserTablesOID)
			keepCandidate:=pgcatalog.Keep{Column:0,Test:func(v sqltype.Value)bool{id,_:=v.OID();return id==uint32(d.Address().ObjectID)}}
			input.Keep=keepCandidate
			if slot>=0 {input.Keep=pgcatalog.Keep{Column:slot,Test:func(sqltype.Value)bool{return false}}}
			ctx:=&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
			inspect:=func(){t.Helper();stamp,err:=ctx.StatsSnapshotValue(false,enginetest.Usage(e,sqltype.Timestamptz).Type);if err!=nil || !stamp.IsNull() {t.Fatal("rejected source initialized reader",stamp,err)}}
			// Input/description construction and all stock user descriptors
			// must not initialize the transaction-owned Reader.
			inspect()
			if rows:=statioSysUnitRows(t,input);len(rows)!=0 {t.Fatal("rejected candidate",rows)}
			inspect()
			var writer statistics.Writer
			publish:=func(heap,indexFact statistics.Fact){writer.Add(&e.Statistics,statistics.Key{Object:auth.Ref(d.Address()),Category:statistics.Relation},heap);writer.Add(&e.Statistics,statistics.Key{Object:auth.Ref(index),Category:statistics.Relation},indexFact);writer.Flush(&e.Statistics,time.Now(),true)}
			publish(statistics.Fact{BlocksFetched:7,BlocksHit:5},statistics.Fact{IndexBlocksRead:6,IndexBlocksHit:8})
			if slot<0 {input=statioSysTableInput(t,e,auth,pgcatalog.StatioAllTablesOID)}
			input.Keep=keepCandidate
			want:=[][]sqltype.Value{statioSysTableUnitRow(t,input,d,schema,2,5,6,8)}
			retained:=statioSysUnitRows(t,input);if !reflect.DeepEqual(retained,want) {t.Fatal("rejection poisoned missing heap/index cache",retained,want)}
			publish(statistics.Fact{BlocksFetched:4,BlocksHit:3},statistics.Fact{IndexBlocksRead:2,IndexBlocksHit:1})
			next:=want;if mode=="none" {next=[][]sqltype.Value{statioSysTableUnitRow(t,input,d,schema,3,8,8,9)}}
			if fresh:=statioSysUnitRows(t,input);!reflect.DeepEqual(fresh,next) || !reflect.DeepEqual(retained,want) {t.Fatal("shared Reader modes/retained scalars",fresh,next,retained)}
			if _,err:=ctx.StatsSnapshotValue(true,enginetest.Usage(e,sqltype.Void).Type);err!=nil {t.Fatal(err)}
			if fresh:=statioSysUnitRows(t,input);!reflect.DeepEqual(fresh,[][]sqltype.Value{statioSysTableUnitRow(t,input,d,schema,3,8,8,9)}) {t.Fatal("clear refreshes table and index together",fresh)}
			// A fresh runtime recaptures changed effective settings; it must not
			// retain evaluated authority or the previous mode's cached facts.
			s.Settings.Mu.Lock();before=s.Settings.Value("stats_fetch_consistency");s.Settings.Values["stats_fetch_consistency"]="none";s.Settings.Typed["stats_fetch_consistency"]=settings.Value{Text:"none"};s.Settings.StatsChanged(before);s.Settings.Mu.Unlock()
			publish(statistics.Fact{BlocksFetched:2,BlocksHit:1},statistics.Fact{IndexBlocksRead:1,IndexBlocksHit:2})
			freshInput:=statioSysTableInput(t,e,auth,input.Source.Address().ObjectID);freshInput.Keep=keepCandidate
			if fresh:=statioSysUnitRows(t,freshInput);!reflect.DeepEqual(fresh,[][]sqltype.Value{statioSysTableUnitRow(t,input,d,schema,4,9,9,11)}) {t.Fatal("live settings capture",fresh)}
		})}
	}
}
