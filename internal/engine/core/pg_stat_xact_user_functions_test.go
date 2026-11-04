package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGStatXactUserFunctionsCatalogIdentity(t *testing.T) {
	e := engine.New(); read := e.CatalogRead()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatXactUserFunctionsOID}
	v := read.MustRelation(a)
	if a.ObjectID != 12284 || v.RowType != 12286 || !v.VirtualView || len(v.Columns) != 6 { t.Fatal("view identity", v) }
	names := strings.Fields("funcid schemaname funcname calls total_time self_time")
	kinds := []sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Name, sqltype.Int8, sqltype.Float8, sqltype.Float8}
	for i, c := range v.Columns { if c.Name != names[i] || c.Usage.Type.ID() != kinds[i] || c.Usage.Typmod != -1 || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12284 || c.OriginAttribute != int16(i+1) { t.Fatal("typed origin", i, c) } }
	row, ok := read.LookupType(sqltype.ID(12286)); if !ok || row.AssociatedArray() != sqltype.ID(12285) { t.Fatal("row/array", row) }
	n := 0; for _, spec := range pgcatalog.Specs { if spec.Name == "pg_stat_xact_user_functions" { n++; if spec.OID != 12284 { t.Fatal(spec) } } }; if n != 1 { t.Fatal("duplicate source", n) }
	def := pgcatalog.SummaryViewDefinition(pgcatalog.StatXactUserFunctionsOID)
	for _, term := range []string{"LEFT JOIN pg_namespace", "pg_stat_get_xact_function_calls(p.oid)", "IS NOT NULL", "prolang"} { if !strings.Contains(def, term) { t.Fatal("released definition", term, def) } }
}

func TestPGStatXactUserFunctionsLogicalTiming(t *testing.T) {
	for _, tc := range []struct { name string; defs []string; call string; want map[string][3]int64 }{
		{"distinct", []string{"CREATE FUNCTION xf_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.005); RETURN 7; END$$", "CREATE FUNCTION xf_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM xf_g(); RETURN 9; END$$"}, "SELECT xf_f()", map[string][3]int64{"xf_f": {1,2,1}, "xf_g": {1,1,1}}},
		{"same", []string{"CREATE FUNCTION xf_f(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n=0 THEN PERFORM xf_f(1); END IF; RETURN 7; END$$"}, "SELECT xf_f(0)", map[string][3]int64{"xf_f": {2,2,2}}},
		{"mutual", []string{"CREATE FUNCTION xf_f(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n=0 THEN PERFORM xf_g(); END IF; RETURN 7; END$$", "CREATE FUNCTION xf_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM xf_f(1); RETURN 7; END$$"}, "SELECT xf_f(0)", map[string][3]int64{"xf_f": {2,3,2}, "xf_g": {1,2,1}}},
		{"untracked", []string{"CREATE FUNCTION xf_h() RETURNS integer LANGUAGE plpgsql SET track_functions=pl AS $$BEGIN RETURN 7; END$$", "CREATE FUNCTION xf_g() RETURNS integer LANGUAGE plpgsql SET track_functions=none AS $$BEGIN PERFORM xf_h(); RETURN 7; END$$", "CREATE FUNCTION xf_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM xf_g(); RETURN 7; END$$"}, "SELECT xf_f()", map[string][3]int64{"xf_f": {1,2,1}, "xf_h": {1,1,1}}},
		{"caught", []string{"CREATE FUNCTION xf_h() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "CREATE FUNCTION xf_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM xf_h(); RAISE EXCEPTION 'child'; END$$", "CREATE FUNCTION xf_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM xf_g(); EXCEPTION WHEN OTHERS THEN PERFORM 1; END; RETURN 7; END$$"}, "SELECT xf_f()", map[string][3]int64{"xf_f": {1,2,1}, "xf_g": {0,0,0}, "xf_h": {1,1,1}}},
		{"failed_outer", []string{"CREATE FUNCTION xf_f(n integer) RETURNS integer LANGUAGE plpgsql SET track_functions=pl AS $$BEGIN IF n=0 THEN PERFORM xf_f(1); RAISE EXCEPTION 'outer'; END IF; RETURN 7; END$$", "CREATE FUNCTION xf_catch() RETURNS integer LANGUAGE plpgsql SET track_functions=none AS $$BEGIN BEGIN PERFORM xf_f(0); EXCEPTION WHEN OTHERS THEN PERFORM 1; END; RETURN 7; END$$"}, "SELECT xf_catch()", map[string][3]int64{"xf_f": {1,1,1}}},
		{"trigger", []string{"CREATE TABLE xf_t(n integer)", "CREATE FUNCTION xf_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER xf_tr BEFORE INSERT ON xf_t FOR EACH ROW EXECUTE FUNCTION xf_trigger()", "CREATE FUNCTION xf_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO xf_t VALUES(1); RETURN 7; END$$"}, "SELECT xf_f()", map[string][3]int64{"xf_f": {1,2,1}, "xf_trigger": {1,1,1}}},
	} {
		t.Run(tc.name, func(t *testing.T) { synctest.Test(t, func(t *testing.T) {
			e := engine.New(); s := enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,s,tc.defs...); statsFunctionExec(t,s,"BEGIN", "SET track_functions=pl",tc.call)
			xactFunctionLogicalRows(t,s,tc.want)
			op := enginetest.TransactionOperation(t,s); defer op.Done()
			scope, err := e.StartStatement(op); if err != nil { t.Fatal(err) }; defer scope.Finish(nil)
			e.Mu.Lock(); defer e.Mu.Unlock(); auth,err := e.AuthorizationLocked(op); if err != nil { t.Fatal(err) }
			runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:s.Settings})
			for name, want := range tc.want { key := statsFunctionKeyUnlocked(t,auth.Catalog,name); got,ok := runtime.XactFunctionStatistics(key); if !ok || [3]int64{got.Calls,got.LogicalTotal,got.LogicalSelf} != want { t.Fatal(name,got,ok,want) } }
		}) })
	}
}

func statsFunctionKeyUnlocked(t *testing.T, read catalog.Read, name string) ids.ObjectAddress {
	t.Helper()
	for _, d := range read.Descriptors() { if p,ok := d.(*catalog.Procedure); ok && p.Name() == name { return p.Address() } }
	t.Fatal("missing procedure",name); return ids.ObjectAddress{}
}

func xactFunctionLogicalRows(t *testing.T,s *session.Session,want map[string][3]int64) {
	t.Helper();op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)};defer op.Done()
	result,err:=core.EngineOf(s).Execute(op,enginetest.Statement(t,"SELECT funcname,calls,total_time,self_time FROM pg_stat_xact_user_functions"),0,nil)
	if err!=nil{t.Fatal("actual view query",err)};defer result.Rows.Close()
	var row [4]sqltype.Value
	seen:=make(map[string]bool,len(want))
	for {
		present,err:=result.Rows.Next(t.Context(),row[:]);if err!=nil{t.Fatal("actual view row",err)};if !present{break}
		name,err:=row[0].Text();if err!=nil{t.Fatal(err)}
		calls,err:=row[1].Integer();if err!=nil{t.Fatal(err)}
		total,err:=row[2].Float();if err!=nil{t.Fatal(err)}
		self,err:=row[3].Float();if err!=nil{t.Fatal(err)}
		expected,ok:=want[name];if !ok||seen[name]||calls!=expected[0]||total!=float64(expected[1])||self!=float64(expected[2]){t.Fatal("actual logical SQL row",name,calls,total,self,expected)}
		seen[name]=true
	}
	if len(seen)!=len(want){t.Fatal("view membership",seen,want)}
}

type xactFunctionSelectionRuntime struct { pgcatalog.Runtime; seen map[ids.ObjectAddress]int }
func (r *xactFunctionSelectionRuntime) XactFunctionStatistics(a ids.ObjectAddress) (pgcatalog.FunctionFact,bool) { r.seen[a]++; return r.Runtime.XactFunctionStatistics(a) }

func TestPGStatXactUserFunctionsSourceSelection(t *testing.T) {
	for _, column := range []int{0,1,2} { t.Run([]string{"oid","schema","name"}[column],func(t *testing.T) {
		e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE SCHEMA xf_other", "CREATE FUNCTION xf_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "CREATE FUNCTION xf_other.xf_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "BEGIN", "SET track_functions=pl", "SELECT xf_f()", "SELECT xf_other.xf_g()")
		op:=enginetest.TransactionOperation(t,s); defer op.Done(); scope,err:=e.StartStatement(op); if err!=nil {t.Fatal(err)}; defer scope.Finish(nil)
		e.Mu.Lock(); defer e.Mu.Unlock(); auth,err:=e.AuthorizationLocked(op); if err!=nil {t.Fatal(err)}
		f,g:=statsFunctionKeyUnlocked(t,auth.Catalog,"xf_f"),statsFunctionKeyUnlocked(t,auth.Catalog,"xf_g")
		runtime:=core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e,Auth:auth,Settings:s.Settings}); wrapped:=&xactFunctionSelectionRuntime{Runtime:runtime,seen:map[ids.ObjectAddress]int{}}
		a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatXactUserFunctionsOID}
		in:=pgcatalog.Input{Context:context.Background(),Owner:&e.Catalog.Owner,Catalog:auth.Catalog,Roles:auth.Roles,Ref:e.Catalog.Domain.Ref(auth.Catalog.Epoch(),a),Source:auth.Catalog.MustRelation(a),Runtime:wrapped}
		in.Keep=pgcatalog.Keep{Column:column,Test:func(v sqltype.Value)bool{if column==0 {n,_:=v.OID();return ids.OID(n)==f.ObjectID}; text,_:=v.Text();if column==1{return text=="public"};return text=="xf_f"}}
		produce,_:=pgcatalog.ProducerFor(pgcatalog.StatXactUserFunctionsOID); rows,err:=produce(in)
		if err!=nil||len(rows)!=1||wrapped.seen[f]!=1||wrapped.seen[g]!=0 {t.Fatal("identity filter precedes inquiry",rows,wrapped.seen,err)}
		retained:=append([]sqltype.Value(nil),rows[0]...); if _,err:=produce(in);err!=nil||!reflect.DeepEqual(retained,rows[0]) {t.Fatal("detached rows",err)}
		ctx,cancel:=context.WithCancel(context.Background());in.Context=ctx;in.Keep=pgcatalog.Keep{Column:2,Test:func(sqltype.Value)bool{cancel();return true}}
		if partial,err:=produce(in);err!=context.Canceled||partial!=nil {t.Fatal("partial cancellation",partial,err)}
		in.Context=context.Background();in.Owner=nil;if partial,err:=produce(in);err==nil||partial!=nil{t.Fatal("invalid authority",partial,err)}
	}) }
}
