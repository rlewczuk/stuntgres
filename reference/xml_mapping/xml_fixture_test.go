package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"stuntgres/reference/fixture"
)

// Only fixture's public execution/diagnostic APIs participate in the dual-mode
// body. These small XML assertions own this package's raw result contract.
func xmlScenario(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)){
	t.Helper();p:=fixture.NewScenario(t)
	for _,backend:=range p.Backends(){t.Run(backend.Name,func(t *testing.T){
		c,err:=backend.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
		// Native bootstrap grants PUBLIC USAGE; the oracle fixture rebuilds
		// public without it. Establish the same deliberate membership policy,
		// retaining each backend's real owner and ordinary ACL implementation.
		xmlMapExec(t,p,c,`REVOKE USAGE ON SCHEMA public FROM PUBLIC`)
		body(t,p,backend,c)
	})}
}
func xmlBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,step fixture.Step){
	t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
	stmt,err:=c.PrepareContext(p.Ctx,step.Query);if err!=nil{t.Fatal(err)};defer stmt.Close()
	fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:step.Query},b.Recorder,step)
}
func xmlSelect(query,typ string,values ...any)fixture.Step{
	rows:=make([][]any,len(values));for i,value:=range values{rows[i]=[]any{value}}
	return fixture.Select(query,[]string{"v"},[]string{typ},rows)
}
func xmlMetadata(t *testing.T,rows *sql.Rows,pg bool){
	t.Helper();columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};if len(columns)!=1{t.Fatal("XML result width",len(columns))}
	column:=columns[0];scan:=reflect.TypeFor[string]();if pg{scan=reflect.TypeFor[any]()}
	if column.Name()!="v"||column.DatabaseTypeName()!="XML"||column.ScanType()!=scan{t.Fatal("XML result descriptor",column.Name(),column.DatabaseTypeName(),column.ScanType())}
	if length,known:=column.Length();length!=0||known{t.Fatal("XML result length",length,known)}
	if precision,scale,known:=column.DecimalSize();precision!=0||scale!=0||known{t.Fatal("XML result precision",precision,scale,known)}
	if nullable,known:=column.Nullable();pg&&(nullable||known){t.Fatal("XML result nullability",nullable,known)}
}
func xmlError(code,message,detail string,position int)fixture.Diagnostic{
	diagnostic:=fixture.ControlsReferenceDiagnostic(code,message);diagnostic.Detail=detail;diagnostic.Position=fixture.Position(position);return diagnostic
}
func xmlFailure(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,query string,diagnostic fixture.Diagnostic,analysis bool){
	t.Helper();for _,prepared:=range []bool{false,true}{step:=fixture.ErrorDiagnostic(query,diagnostic);step.SelectRows=true;step.ErrorStage="execute";if prepared{step=fixture.Prepared(step);if analysis{step.ErrorStage="prepare"}};fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
}
func xmlMapExec(t *testing.T,p *fixture.Pair,c *sql.Conn,queries ...string){
	t.Helper();for _,query:=range queries{if _,err:=c.ExecContext(p.Ctx,query);err!=nil{t.Fatal(query,err)}}
}
func xmlMapLiteral(text string)string{return "'"+strings.ReplaceAll(text,"'","''")+"'"}
func xmlMapRow(fields string)string{return "<row>\n"+fields+"</row>\n\n"}

const xmlMapXSI=` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`

// REL_18_6 xml.c's INTEGER restriction; not mapper-derived output.
const xmlMapInteger="<xsd:simpleType name=\"INTEGER\">\n  <xsd:restriction base=\"xsd:int\">\n    <xsd:maxInclusive value=\"2147483647\"/>\n    <xsd:minInclusive value=\"-2147483648\"/>\n  </xsd:restriction>\n</xsd:simpleType>\n\n"

func xmlMapDatabase(t *testing.T,p *fixture.Pair,c *sql.Conn)string{
	t.Helper();var name string;if err:=c.QueryRowContext(p.Ctx,`SELECT current_database()`).Scan(&name);err!=nil{t.Fatal(err)}
	// The pinned fixture names are ASCII. Independently apply the SQL/XML
	// full-escape/multipart spelling, never ask a mapper to supply its golden.
	var mapped strings.Builder
	for i,r:=range name{
		if r>127{t.Fatalf("fixture database needs independent non-ASCII expectation: %q",name)}
		letter:=r>='a'&&r<='z'||r>='A'&&r<='Z'
		escape:=r==':'||r=='.'||r=='_'&&i+1<len(name)&&name[i+1]=='x'||i==0&&len(name)>=3&&strings.EqualFold(name[:3],"xml")
		if !escape{escape=!(letter||r=='_'||i>0&&(r>='0'&&r<='9'||r=='-'))}
		if escape{fmt.Fprintf(&mapped,"_x%04X_",r)}else{mapped.WriteRune(r)}
	}
	return mapped.String()
}
