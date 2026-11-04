package core_test

import (
	"context"
	"reflect"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type userFunctionSelectionRuntime struct { pgcatalog.Runtime; seen map[ids.ObjectAddress]int }
func (r *userFunctionSelectionRuntime) FunctionStatistics(a ids.ObjectAddress) (pgcatalog.FunctionFact,bool) { r.seen[a]++; return r.Runtime.FunctionStatistics(a) }

func TestPGStatUserFunctionsSourceSelection(t *testing.T) {
	for _, column := range []int{0,1,2} { t.Run([]string{"oid","schema","name"}[column],func(t *testing.T) {
		e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE SCHEMA uf_select_other", "CREATE FUNCTION uf_select_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "CREATE FUNCTION uf_select_other.uf_select_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "BEGIN", "SET track_functions=pl", "SELECT uf_select_f()", "SELECT uf_select_other.uf_select_g()")
		statsFunctionRetire(t,s); s=enginetest.StartSession(t,e,nil)
		op:=enginetest.TransactionOperation(t,s); defer op.Done(); scope,err:=e.StartStatement(op); if err!=nil {t.Fatal(err)}; defer scope.Finish(nil)
		e.Mu.Lock(); defer e.Mu.Unlock(); auth,err:=e.AuthorizationLocked(op); if err!=nil {t.Fatal(err)}
		f,g:=statsFunctionKeyUnlocked(t,auth.Catalog,"uf_select_f"),statsFunctionKeyUnlocked(t,auth.Catalog,"uf_select_g")
		runtime:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:s.Settings}); wrapped:=&userFunctionSelectionRuntime{Runtime:runtime,seen:map[ids.ObjectAddress]int{}}
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatUserFunctionsOID}
		in:=pgcatalog.Input{Context:context.Background(),Owner:&e.Catalog.Owner,Catalog:auth.Catalog,Roles:auth.Roles,Ref:e.Catalog.Domain.Ref(auth.Catalog.Epoch(),a),Source:auth.Catalog.MustRelation(a),Runtime:wrapped}
		in.Keep=pgcatalog.Keep{Column:column,Test:func(v sqltype.Value)bool{if column==0 {n,_:=v.OID();return ids.OID(n)==f.ObjectID}; text,_:=v.Text();if column==1{return text=="public"};return text=="uf_select_f"}}
		produce,_:=pgcatalog.ProducerFor(pgcatalog.StatUserFunctionsOID); rows,err:=produce(in)
		if err!=nil||len(rows)!=1||wrapped.seen[f]!=1||wrapped.seen[g]!=0 {t.Fatal("identity filter precedes inquiry",rows,wrapped.seen,err)}
		retained:=append([]sqltype.Value(nil),rows[0]...); if _,err:=produce(in);err!=nil||!reflect.DeepEqual(retained,rows[0]) {t.Fatal("detached rows",err)}
		ctx,cancel:=context.WithCancel(context.Background());in.Context=ctx;in.Keep=pgcatalog.Keep{Column:2,Test:func(sqltype.Value)bool{cancel();return true}}
		if partial,err:=produce(in);err!=context.Canceled||partial!=nil {t.Fatal("partial cancellation",partial,err)}
		in.Context=context.Background();in.Owner=nil;if partial,err:=produce(in);err==nil||partial!=nil{t.Fatal("invalid authority",partial,err)}
	}) }
}