package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const userFunctionDefinition = ` SELECT p.oid AS funcid,
    n.nspname AS schemaname,
    p.proname AS funcname,
    pg_stat_get_function_calls(p.oid) AS calls,
    pg_stat_get_function_total_time(p.oid) AS total_time,
    pg_stat_get_function_self_time(p.oid) AS self_time
   FROM (pg_proc p
     LEFT JOIN pg_namespace n ON ((n.oid = p.pronamespace)))
  WHERE ((p.prolang <> (12)::oid) AND (pg_stat_get_function_calls(p.oid) IS NOT NULL));`

func TestReferencePGStatUserFunctionsIdentityAndMetadata(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		var id,namespace,row,array,element,owner int64;var kind string
		err:=c.QueryRowContext(p.Ctx,"SELECT c.oid,c.relnamespace,c.relkind,c.reltype,t.typarray,a.typelem,t.typrelid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_user_functions'::regclass").Scan(&id,&namespace,&kind,&row,&array,&element,&owner)
		if err!=nil||id<=0||namespace!=11||kind!="v"||row<=0||array<=0||element!=row||owner!=id{t.Fatal("reciprocal identities",id,namespace,kind,row,array,element,owner,err)}
		if !b.PG&&(id!=12279||row!=12281||array!=12280){t.Fatal("native fixed IDs",id,row,array)}
		var regtype int64;if err:=c.QueryRowContext(p.Ctx,"SELECT 'pg_stat_user_functions'::regtype::oid").Scan(&regtype);err!=nil||regtype!=row{t.Fatal("regtype",regtype,err)}
		attrs,err:=c.QueryContext(p.Ctx,"SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation FROM pg_attribute WHERE attrelid='pg_stat_user_functions'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum");if err!=nil{t.Fatal(err)}
		n:=0;for attrs.Next(){var number,typ,mod,coll int64;var name string;var notnull bool;if err:=attrs.Scan(&number,&name,&typ,&mod,&notnull,&coll);err!=nil{t.Fatal(err)};types:=[]int64{26,19,19,20,701,701};wantColl:=int64(0);if n==1||n==2{wantColl=950};if n>=6||number!=int64(n+1)||name!=xactFunctionNames[n]||typ!=types[n]||mod!=-1||notnull||coll!=wantColl{t.Fatal("attribute",n,number,name,typ,mod,notnull,coll)};n++};if attrs.Err()!=nil{t.Fatal(attrs.Err())};attrs.Close();if n!=6{t.Fatal("attribute count",n)}
		for _,pretty:=range []bool{false,true}{
			want:=userFunctionDefinition
			if pretty {
				want=strings.Replace(want,"   FROM (pg_proc p\n     LEFT JOIN pg_namespace n ON ((n.oid = p.pronamespace)))","   FROM pg_proc p\n     LEFT JOIN pg_namespace n ON n.oid = p.pronamespace",1)
				want=strings.Replace(want,"  WHERE ((p.prolang <> (12)::oid) AND (pg_stat_get_function_calls(p.oid) IS NOT NULL));","  WHERE p.prolang <> 12::oid AND pg_stat_get_function_calls(p.oid) IS NOT NULL;",1)
			}
			var text string;if err:=c.QueryRowContext(p.Ctx,"SELECT pg_get_viewdef('pg_stat_user_functions'::regclass,$1::boolean)",pretty).Scan(&text);err!=nil||text!=want{t.Fatal("exact released definition",pretty,text,want,err)}
		}
		fixture.StatisticsExec(t,p,b,c,"SET quote_all_identifiers=on",0);var quoted string;if err:=c.QueryRowContext(p.Ctx,"SELECT pg_get_viewdef('pg_stat_user_functions'::regclass,true::boolean)").Scan(&quoted);err!=nil||!strings.Contains(quoted,`"pg_proc" "p"`)||!strings.Contains(quoted,`"pg_stat_get_function_calls"`){t.Fatal("quoted display",quoted,err)};fixture.StatisticsExec(t,p,b,c,"SET quote_all_identifiers=off",0)
		fixture.StatisticsExec(t,p,b,c,"CREATE TABLE public.pg_proc(n integer)",0);fixture.StatisticsExec(t,p,b,c,"SET search_path=public,pg_catalog",0)
		var shadow string;if err:=c.QueryRowContext(p.Ctx,"SELECT pg_catalog.pg_get_viewdef('pg_catalog.pg_stat_user_functions'::regclass)").Scan(&shadow);err!=nil||!strings.Contains(shadow,"FROM (pg_catalog.pg_proc p"){t.Fatal("identity-aware display",shadow,err)}
		fixture.StatisticsExec(t,p,b,c,"DROP TABLE public.pg_proc",0);fixture.StatisticsExec(t,p,b,c,"RESET search_path",0)
		xactFunctionCreate(t,p,b,c,"uf_metadata","RETURN 7;")
		retained:=fixture.IdentityHeld(t,p.Ctx,c,userFunctionSQL);defer retained.Statement.Close()
		emptyRows,err:=retained.QueryContext(p.Ctx,userFunctionSQL,"uf_metadata");if err!=nil{t.Fatal(err)};emptyTypes,err:=emptyRows.ColumnTypes();if err!=nil{t.Fatal(err)};xactFunctionMetadata(t,emptyTypes,b.PG);if emptyRows.Next()||emptyRows.Err()!=nil{t.Fatal("prepare/empty read fabricated activity",emptyRows.Err())};emptyRows.Close()
		w,retire:=fixture.StatisticsReportingConn(t,p,b);xactFunctionBegin(t,p,b,w);xactFunctionCall(t,p,b,w,"uf_metadata",7);fixture.StatisticsExec(t,p,b,w,"COMMIT",0);retire();userFunctionAwait(t,p,b,"uf_metadata",1)
		heldRows,err:=retained.QueryContext(p.Ctx,userFunctionSQL,"uf_metadata");if err!=nil{t.Fatal(err)};heldTypes,err:=heldRows.ColumnTypes();if err!=nil{t.Fatal(err)};xactFunctionMetadata(t,heldTypes,b.PG);if !heldRows.Next(){t.Fatal("held populated metadata",heldRows.Err())};var heldRow xactFunctionRow;if err:=heldRows.Scan(heldRow.destinations()...);err!=nil{t.Fatal(err)};xactFunctionCounts(t,b,heldRow,1,1,1);if heldRows.Next()||heldRows.Err()!=nil{t.Fatal("held cardinality",heldRows.Err())};heldRows.Close();xactFunctionMetadata(t,emptyTypes,b.PG);xactFunctionMetadata(t,heldTypes,b.PG)
		for _,qualified:=range []bool{false,true}{for _,empty:=range []bool{false,true}{for _,prepared:=range []bool{false,true}{
			q:="SELECT * FROM pg_stat_user_functions WHERE funcname=$1";if qualified{q=userFunctionSQL};if empty{q+=" AND FALSE"}
			var rows *sql.Rows;var held *sql.Stmt;if prepared{held,err=c.PrepareContext(p.Ctx,q);if err==nil{rows,err=held.QueryContext(p.Ctx,"uf_metadata")}}else{rows,err=c.QueryContext(p.Ctx,q,"uf_metadata")};if err!=nil{t.Fatal(err)}
			columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};xactFunctionMetadata(t,columns,b.PG);count:=0
			for rows.Next(){var raw [6]any;dest:=make([]any,6);for i:=range raw{dest[i]=&raw[i]};if err:=rows.Scan(dest...);err!=nil{t.Fatal(err)};for i,v:=range raw{want:=reflect.TypeFor[int64]();if i==1||i==2{want=reflect.TypeFor[string]()};if i>=4{want=reflect.TypeFor[float64]()};if b.PG&&i<3{want=reflect.TypeFor[[]byte]()};if reflect.TypeOf(v)!=want{t.Fatalf("raw cell %d=%T want %v",i,v,want)}};count++};if rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close();if held!=nil{held.Close()};xactFunctionMetadata(t,columns,b.PG);want:=1;if empty{want=0};if count!=want{t.Fatal("cardinality",count,want)}
		}}}
		q:="SELECT f.* FROM (VALUES (1)) AS v(n) LEFT JOIN pg_stat_user_functions f ON f.funcname='xf_never_exists'"
		rows,err:=c.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)};columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};xactFunctionMetadata(t,columns,b.PG);if !rows.Next(){t.Fatal(rows.Err())};var cells [6]any;dest:=make([]any,6);for i:=range cells{dest[i]=&cells[i]};if err:=rows.Scan(dest...);err!=nil{t.Fatal(err)};for _,v:=range cells{if v!=nil{t.Fatal("outer NULL fabricated",cells)}};if rows.Next()||rows.Err()!=nil{t.Fatal("outer cardinality",rows.Err())};rows.Close()
		
	})
}