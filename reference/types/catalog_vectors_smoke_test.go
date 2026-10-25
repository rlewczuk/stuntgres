package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCatalogVectorsFeatureSmoke(t *testing.T) {
	catalogVectorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		catalogVectorControlSetup(t,p,b)
		run:=func(s fixture.Step){catalogVectorRows(t,p,b,b.DB,s)}
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
		q:="SELECT id FROM vectors WHERE o=$1 ORDER BY id"
		stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
		func(){defer stmt.Close();held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q};for _,tc:=range []struct{arg any;want [][]any}{{"1 2 3",[][]any{{int64(1)}}},{[]byte{},[][]any{{int64(2)}}},{nil,nil},{[]byte("-1 -2147483648"),[][]any{{int64(4)}}}}{fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"id"},[]string{"INT4"},tc.want,tc.arg))}}()
		run(fixture.Select("SELECT s[0] AS s,o[0] AS o,array_lower(s,1) AS l,array_length(o,1) AS n FROM vectors WHERE id=1",[]string{"s","o","l","n"},[]string{"INT2","OID","INT4","INT4"},[][]any{{int64(1),int64(1),int64(0),int64(3)}}))
		run(fixture.Select("SELECT array_dims(s) AS d,cardinality(o) AS n FROM vectors WHERE id=2",[]string{"d","n"},[]string{"TEXT","INT4"},[][]any{{"[0:-1]",int64(0)}}))
		run(fixture.Select("SELECT '99'::int2vector > '1 2'::int2vector AS s,'99'::oidvector < '1 2'::oidvector AS o",[]string{"s","o"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
		for _,tc:=range []struct{col,input,scalar,code,suffix string}{{"s","1 32768 2","smallint","22003","32768 2"},{"o","1x","oid","22P02","x"}}{
			q:="INSERT INTO vectors(id,"+tc.col+") VALUES(10,'1'),(11,"+catalogVectorQuote(tc.input)+")"
			d:=catalogVectorDiagnostic(tc.code,tc.scalar,tc.suffix);d.Position=fixture.Position(strings.LastIndex(q,catalogVectorQuote(tc.input))+1)
			run(fixture.ErrorDiagnostic(q,d))
			catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
		}
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT proargtypes FROM pg_proc WHERE oid=41",[]string{"proargtypes"},[]string{"OIDVECTOR"},[][]any{{"22"}}))
		run(fixture.Exec("CREATE INDEX vector_smoke_idx ON vectors(id DESC NULLS FIRST)",0))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT indkey,indclass,indoption FROM pg_index WHERE indexrelid='vector_smoke_idx'::regclass",[]string{"indkey","indclass","indoption"},[]string{"INT2VECTOR","OIDVECTOR","INT2VECTOR"},[][]any{{"1","1978","3"}}))
		run(fixture.Exec("DROP INDEX vector_smoke_idx",0))
		if !b.PG {catalogVectorNativeControls(t,p,b)}
	})
}
