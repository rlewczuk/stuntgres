package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const ltreeLquerySchema="lq_provider"
const ltreeLqueryProjection=`path ~ 'Top.*{1,2}.Child'::lquery AS forward,'Top.*{1,2}.Child'::lquery ~ path AS reverse,path ? '{Other.*,"Top.*{1,2}.Child"}'::lquery[] AS alternative`
var ltreeLqueryStoredRows=[][]any{{int64(1),true,true,true},{int64(2),false,false,false},{int64(3),nil,nil,nil}}

func ltreeLqueryControlSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
	t.Helper()
	for _,q:=range []string{
		"CREATE SCHEMA "+ltreeLquerySchema,"CREATE EXTENSION ltree SCHEMA "+ltreeLquerySchema,"SET search_path=public,"+ltreeLquerySchema,
		"CREATE TABLE lq_paths(id integer NOT NULL,path ltree)",
		"CREATE VIEW lq_view AS SELECT id,"+ltreeLqueryProjection+" FROM lq_paths",
		"CREATE TABLE lq_defaults(id integer NOT NULL,v boolean DEFAULT ('a'::ltree ~ 'a'::lquery))",
		"CREATE TABLE lq_guard(id integer NOT NULL,path ltree,CONSTRAINT lq_guard_match CHECK(path ~ 'Top.*{1,2}.Child'::lquery))",
	}{ltreeLqueryRun(t,p,b,c,fixture.Exec(q,0))}
	ltreeLqueryRun(t,p,b,c,fixture.Exec("INSERT INTO lq_paths VALUES (1,'Top.A.Child'),(2,'Top.Child'),(3,NULL)",3))
	ltreeLqueryRun(t,p,b,c,fixture.Exec("INSERT INTO lq_defaults(id) VALUES (1)",1))
	ltreeLqueryRun(t,p,b,c,fixture.Exec("INSERT INTO lq_guard VALUES (1,'Top.A.Child'),(3,NULL)",2))
	// Rename the installed namespace after capturing the stored expressions.
	// This exercises provider presentation without moving extension membership.
	ltreeLqueryRun(t,p,b,c,fixture.Exec("ALTER SCHEMA "+ltreeLquerySchema+" RENAME TO lq_provider_renamed",0))
	ltreeLqueryRun(t,p,b,c,fixture.Exec("SET search_path=public,lq_provider_renamed",0))
	s:=fixture.Select("SELECT id,forward,reverse,alternative FROM lq_view ORDER BY id",[]string{"id","forward","reverse","alternative"},[]string{"INT4","BOOL","BOOL","BOOL"},ltreeLqueryStoredRows)
	ltreeLqueryRun(t,p,b,c,s);ltreeLqueryRun(t,p,b,c,fixture.Prepared(s))
	ltreeLqueryRun(t,p,b,c,fixture.Exec("ALTER SCHEMA lq_provider_renamed RENAME TO "+ltreeLquerySchema,0))
}

func ltreeLqueryControlChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor){
	t.Helper()
	ltreeLqueryRun(t,p,b,e,fixture.Exec("SET search_path=public,"+ltreeLquerySchema,0))
	for _,s:=range []fixture.Step{
		fixture.Select("SELECT id,forward,reverse,alternative FROM lq_view ORDER BY id",[]string{"id","forward","reverse","alternative"},[]string{"INT4","BOOL","BOOL","BOOL"},ltreeLqueryStoredRows),
		fixture.Select("SELECT id,v FROM lq_defaults ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(1),true}}),
		fixture.Select("SELECT id FROM lq_guard ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(3)}}),
		ltreeArrayResult(b,"SELECT 'a'::lq_provider.ltree OPERATOR(lq_provider.~) 'a'::lq_provider.lquery AS v",true),
		ltreeArrayResult(b,"SELECT lq_provider.ltq_regex('a'::lq_provider.ltree,'a'::lq_provider.lquery) AS v",true),
		ltreeArrayResult(b,"SELECT lq_provider.ltq_rregex('a'::lq_provider.lquery,'a'::lq_provider.ltree) AS v",true),
	}{ltreeLqueryRun(t,p,b,e,s);ltreeLqueryRun(t,p,b,e,fixture.Prepared(s))}
	want:=fixture.ControlsReferenceDiagnostic("23514",`new row for relation "lq_guard" violates check constraint "lq_guard_match"`)
	want.Detail="Failing row contains (2, Top.Child).";want.Schema="public";want.Table="lq_guard";want.Constraint="lq_guard_match"
	s:=fixture.ErrorDiagnostic("INSERT INTO lq_guard VALUES (2,'Top.Child')",want);ltreeLqueryRun(t,p,b,e,s)
	// Exercise newly restored expressions, rather than inspecting only old values.
	ltreeLqueryRun(t,p,b,e,fixture.Exec("INSERT INTO lq_defaults(id) VALUES (2)",1))
	ltreeLqueryRun(t,p,b,e,fixture.Select("SELECT v FROM lq_defaults WHERE id=2",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
	ltreeLqueryRun(t,p,b,e,fixture.Exec("DELETE FROM lq_defaults WHERE id=2",1))
	for _,s:=range ltreeLqueryCatalogChecks(ltreeLquerySchema){ltreeLqueryRun(t,p,b,e,s)}
}

func ltreeLqueryDrop(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
	t.Helper()
	for _,q:=range []string{"DROP VIEW lq_view","DROP TABLE lq_paths","DROP TABLE lq_defaults","DROP TABLE lq_guard","DROP EXTENSION ltree","DROP SCHEMA "+ltreeLquerySchema}{ltreeLqueryRun(t,p,b,c,fixture.Exec(q,0))}
}

func ltreeLqueryRetain(t *testing.T,p *fixture.Pair,b fixture.Backend)(*sql.Rows,[]*sql.ColumnType){
	t.Helper()
	rows,err:=b.DB.QueryContext(p.Ctx,"SELECT forward,reverse,alternative FROM public.lq_view ORDER BY id");if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){rows.Close()})
	return rows,ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL","BOOL"},[]bool{true,true,true})
}

func ltreeLqueryConsume(t *testing.T,rows *sql.Rows,columns []*sql.ColumnType){
	t.Helper()
	i:=0
	for rows.Next(){var a,b,c any;if err:=rows.Scan(&a,&b,&c);err!=nil{t.Fatal(err)};if i>=len(ltreeLqueryStoredRows)||a!=ltreeLqueryStoredRows[i][1]||b!=ltreeLqueryStoredRows[i][2]||c!=ltreeLqueryStoredRows[i][3]{t.Fatalf("retained scalar row %d: %v/%v/%v",i,a,b,c)};i++}
	if err:=rows.Err();err!=nil{t.Fatal(err)};rows.Close();if i!=3{t.Fatalf("retained rows %d",i)}
	ltreeArrayColumnMetadata(t,fixture.Backend{},columns,[]string{"BOOL","BOOL","BOOL"},[]bool{true,true,true})
}
