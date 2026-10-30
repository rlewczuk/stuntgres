package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreRowsCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, schema string, names []string) {
	t.Helper()
	for i, name := range names {
		sig := schema+"."+name+"(public.hstore)"
		result := "text"; if i==2 { result="record" }
		q := `SELECT p.prosrc,p.probin,p.proisstrict,p.proretset,p.provolatile::text,p.proparallel::text,p.prorettype=$2::regtype AS result,p.prorows::double precision AS rows,p.pronargs::integer AS nargs,p.proargtypes[0]='public.hstore'::regtype AS input,l.lanname::text AS language FROM (SELECT * FROM pg_proc WHERE oid=$1::regprocedure) p JOIN pg_language l ON l.oid=p.prolang`
		hstoreRun(t,p,b,db,fixture.Select(q,[]string{"prosrc","probin","proisstrict","proretset","provolatile","proparallel","result","rows","nargs","input","language"},[]string{"TEXT","TEXT","BOOL","BOOL","TEXT","TEXT","BOOL","FLOAT8","INT4","BOOL","TEXT"},[][]any{{[]string{"hstore_skeys","hstore_svals","hstore_each"}[i],"$libdir/hstore",true,true,"i","s",true,float64(1000),int64(1),true,"c"}},sig,result))
		hstoreRun(t,p,b,db,fixture.Select(`SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'`,[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}},sig))
		if i==2 {
			hstoreRun(t,p,b,db,fixture.Select(`SELECT proallargtypes[1]='public.hstore'::regtype AS input,proallargtypes[2]=25 AS key,proallargtypes[3]=25 AS value,array_length(proallargtypes,1) AS n,proargmodes::text AS modes,proargnames::text AS names FROM pg_proc WHERE oid=$1::regprocedure`,[]string{"input","key","value","n","modes","names"},[]string{"BOOL","BOOL","BOOL","INT4","TEXT","TEXT"},[][]any{{true,true,true,int64(3),"{i,o,o}","{hs,key,value}"}},sig))
		} else {
			hstoreRun(t,p,b,db,fixture.Select(`SELECT proallargtypes IS NULL AS no_out,proargmodes IS NULL AS no_modes,proargnames IS NULL AS unnamed FROM pg_proc WHERE oid=$1::regprocedure`,[]string{"no_out","no_modes","unnamed"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,true}},sig))
		}
	}
}

func TestReferenceHstoreRowsCatalog(t *testing.T) {
	hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		hstoreInstall(t,p,b,c)
		hstoreRowsCatalog(t,p,b,c,"public",[]string{"skeys","svals","each"})
		for _,name:=range []string{"skeys","svals","each"} {
			for _,arg:=range []any{"",nil} {
				q:="SELECT * FROM "+name+"($1::hstore)"
				hstoreRowsBoth(t,p,b,c,hstoreRowsSelect(q,hstoreRowsNames(name),[][]any{},arg))
				stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
				hstoreRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},hstoreRowsSelect(q,hstoreRowsNames(name),[][]any{},arg))
				if err:=stmt.Close();err!=nil{t.Fatal(err)}
			}
			rows,err:=c.QueryContext(p.Ctx,"SELECT * FROM "+name+"('a=>NULL'::hstore)");if err!=nil{t.Fatal(err)}
			names,err:=rows.Columns();if err!=nil{t.Fatal(err)}
			columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
			saved:=append([]string(nil),names...)
			if len(names)>0 { names[0]="caller mutation" }
			if !rows.Next(){t.Fatalf("missing NULL-valued pair: %v",rows.Err())}
			if rows.Next() || rows.Err()!=nil {t.Fatalf("extra rows: %v",rows.Err())}
			if err:=rows.Close();err!=nil{t.Fatal(err)}
			if !reflect.DeepEqual(saved,hstoreRowsNames(name)){t.Fatalf("labels %#v",saved)}
			hstoreRowsTextMetadata(t,columns,hstoreRowsNames(name))
		}
	})
}

func hstoreRowsPrepared(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
	t.Helper()
	for _,name:=range []string{"skeys","svals","each","each_named"} {
		call:=name+"($1::hstore)"; base:=name
		if name=="each_named" {call="each(hs => $1::hstore)";base="each"}
		q:="SELECT * FROM "+call
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
		func(){
			defer stmt.Close()
			held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			for _,input:=range []struct{value any;rows [][]any}{
				{hstoreRowsInput,hstoreRowsValues(base)},{"",[][]any{}},{nil,[][]any{}},
			} {
				s:=hstoreRowsSelect(q,hstoreRowsNames(base),input.rows,input.value)
				hstoreRowsBoth(t,p,b,c,s);hstoreRun(t,p,b,held,s)
			}
			want:=hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,1)
			for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,false,want,"a=b")}
			hstoreError(t,p,b,held,q,false,true,false,want,"a=b")
			one:=[][]any{{"a",nil}};if base=="skeys"{one=[][]any{{"a"}}};if base=="svals"{one=[][]any{{nil}}}
			for _,input:=range []struct{value any;rows [][]any}{{"a=>NULL",one},{hstoreRowsInput,hstoreRowsValues(base)}} {
				s:=hstoreRowsSelect(q,hstoreRowsNames(base),input.rows,input.value)
				hstoreRowsBoth(t,p,b,c,s);hstoreRun(t,p,b,held,s)
			}
		}()
	}
}

func TestReferenceHstoreRowsPrepared(t *testing.T) {
	hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		hstoreRowsSeed(t,p,b,c)
		hstoreRowsPrepared(t,p,b,c)
		for _,name:=range []string{"skeys","svals","each"} {
			hstoreRowsBoth(t,p,b,c,hstoreRowsSelect("SELECT * FROM "+name+"("+hstoreLiteral(hstoreRowsInput)+")",hstoreRowsNames(name),hstoreRowsValues(name)))
		}
		hstoreRun(t,p,b,c,fixture.Select(`SELECT id FROM hs_rows ORDER BY id`,[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)},{int64(5)}}))
		hstoreRowsBoth(t,p,b,c,hstoreRowsSelect(`SELECT key,value FROM hs_rows h CROSS JOIN LATERAL each(h.v) e WHERE h.id=1`,[]string{"key","value"},hstoreRowsPairs()))
		hstoreRowsBoth(t,p,b,c,hstoreRowsSelect(`SELECT key,value FROM hs_rows h CROSS JOIN LATERAL each(h.v) e WHERE h.id=4`,[]string{"key","value"},[][]any{{"a",nil}}))
		for _,id:=range []string{"2","3"} {hstoreRowsBoth(t,p,b,c,hstoreRowsSelect(`SELECT key,value FROM hs_rows h CROSS JOIN LATERAL each(h.v) e WHERE h.id=`+id,[]string{"key","value"},[][]any{}))}
	})
}
