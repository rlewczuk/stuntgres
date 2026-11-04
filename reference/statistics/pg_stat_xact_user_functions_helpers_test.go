package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var xactFunctionNames = strings.Fields("funcid schemaname funcname calls total_time self_time")
var xactFunctionTypes = strings.Fields("OID NAME NAME INT8 FLOAT8 FLOAT8")
const xactFunctionSQL = "SELECT * FROM pg_catalog.pg_stat_xact_user_functions WHERE funcname=$1"

type xactFunctionRow struct { ID int64; Schema, Name string; Calls int64; Total, Self float64 }
func (r *xactFunctionRow) destinations() []any { return []any{&r.ID,&r.Schema,&r.Name,&r.Calls,&r.Total,&r.Self} }
func xactFunctionGet(t *testing.T,p *fixture.Pair,c fixture.Executor,name string) xactFunctionRow {
	t.Helper(); rows,err:=c.QueryContext(p.Ctx,xactFunctionSQL,name);if err!=nil{t.Fatal(name,err)};defer rows.Close()
	if !rows.Next(){t.Fatal("missing function pending row",name,rows.Err())};var r xactFunctionRow;if err:=rows.Scan(r.destinations()...);err!=nil{t.Fatal(err)}
	if rows.Next()||rows.Err()!=nil{t.Fatal("extra function identity",name,rows.Err())};return r
}
func xactFunctionAbsent(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,name string) {
	t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_catalog.pg_stat_xact_user_functions WHERE funcname=$1",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}},name))
}
func xactFunctionCounts(t *testing.T,b fixture.Backend,r xactFunctionRow,calls int64,total,self float64) {
	t.Helper();if r.ID<=0||r.Schema==""||r.Name==""||r.Calls!=calls||math.IsNaN(r.Total)||math.IsNaN(r.Self)||math.IsInf(r.Total,0)||math.IsInf(r.Self,0)||r.Self<0||r.Total<r.Self{t.Fatal("exact identity/calls and valid timing",r,calls)}
	if !b.PG&&(r.Total!=total||r.Self!=self){t.Fatal("logical invocation milliseconds",r,total,self)}
}
func xactFunctionCreate(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,name,body string) {
	t.Helper();t.Cleanup(func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();if _,err:=b.DB.ExecContext(ctx,"DROP FUNCTION IF EXISTS public."+name+"() CASCADE");err!=nil{t.Error(err)}})
	fixture.StatisticsExec(t,p,b,c,"CREATE FUNCTION public."+name+"() RETURNS integer LANGUAGE plpgsql AS $$BEGIN "+body+" END$$",0)
}
func xactFunctionCall(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,name string,want int64) {
	t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT "+name+"()",[]string{name},[]string{"INT4"},[][]any{{want}}))
}
func xactFunctionBegin(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {t.Helper();fixture.StatisticsExec(t,p,b,c,"BEGIN",0);fixture.StatisticsExec(t,p,b,c,"SET track_functions=pl",0)}
func xactFunctionMetadata(t *testing.T,columns []*sql.ColumnType,pg bool) {
	t.Helper();if len(columns)!=6{t.Fatal("six columns",len(columns))}
	for i,c:=range columns {
		if c.Name()!=xactFunctionNames[i]||c.DatabaseTypeName()!=xactFunctionTypes[i]{t.Fatal("typed metadata",i,c.Name(),c.DatabaseTypeName())}
		n,k:=c.Nullable();if pg {if n||k{t.Fatal("pq nullable",i)}}else if !n||!k{t.Fatal("native nullable",i)}
		if _,ok:=c.Length();ok{t.Fatal("unexpected length",i)};if _,_,ok:=c.DecimalSize();ok{t.Fatal("unexpected precision",i)}
		want:=reflect.TypeFor[int64]();if i==1||i==2{want=reflect.TypeFor[string]()};if i>=4{want=reflect.TypeFor[float64]()};if pg&&i<3{want=reflect.TypeFor[any]()};if c.ScanType()!=want{t.Fatal("scan type",i,c.ScanType(),want)}
	}
}
func xactFunctionDiagnostic(code,message string) fixture.Diagnostic { return fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:code,Message:message} }
func xactFunctionDenied(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,q,code,message string,prepared bool) {
	t.Helper();step:=fixture.ErrorDiagnostic(q,xactFunctionDiagnostic(code,message));step.SelectRows=true;step.ErrorStage="execute";step.Prepared=prepared;fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
}
