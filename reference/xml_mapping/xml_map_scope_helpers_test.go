package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"stuntgres/reference/fixture"
)

var xmlScopeFunctions=[]string{"schema_to_xml","schema_to_xmlschema","schema_to_xml_and_xmlschema","database_to_xml","database_to_xmlschema","database_to_xml_and_xmlschema"}

// Literal release templates, independent of native checked construction. Rows
// are one/object unless the scenario intentionally compares unordered content.
func xmlScopeTable(name string,forest bool,fields ...string)string{
	var text string
	if !forest{text="<"+name+">\n\n"}
	for _,field:=range fields{if forest{text+="<"+name+">\n"+field+"</"+name+">\n\n"}else{text+=xmlMapRow(field)}}
	if !forest{text+="</"+name+">\n"};return text+"\n"
}
func xmlScopeNested(name,body string)string{return "<"+name+">\n\n"+body+"</"+name+">\n\n"}
func xmlScopeDocument(name,ns,schema,body string)string{
	attributes:=xmlMapXSI;if ns!=""{attributes+=` xmlns="`+ns+`"`}
	if schema!=""{if ns==""{attributes+=` xsi:noNamespaceSchemaLocation="#"`}else{attributes+=` xsi:schemaLocation="`+ns+` #"`}}
	if schema!=""{body=schema+"\n\n"+body}
	return "<"+name+attributes+">\n\n"+body+"</"+name+">\n"
}
// Identity arguments are independently mapped XML spellings (as is the current
// database helper's result), never raw names to remap or mapper-produced XML.
func xmlScopeXSD(db,schema,ns,types string,forest bool,members ...string)string{
	header:="<xsd:schema\n    xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\"";if ns!=""{header+="\n    targetNamespace=\""+ns+"\"\n    elementFormDefault=\"qualified\""}
	root,top,group:=db,"CatalogType."+db,"all";if schema!=""{root=schema;top="SchemaType."+db+"."+schema;if forest{group="sequence"}}
	text:=header+">\n\n"+types+`<xsd:complexType name="`+top+`">`+"\n  <xsd:"+group+">\n"
	for _,name:=range members{
		typ,extra:="SchemaType."+db+"."+name,"";if schema!=""{prefix:="TableType";if forest{prefix="RowType";extra=` minOccurs="0" maxOccurs="unbounded"`};typ=prefix+"."+db+"."+schema+"."+name}
		text+=`    <xsd:element name="`+name+`" type="`+typ+`"`+extra+"/>\n"
	}
	return text+"  </xsd:"+group+">\n</xsd:complexType>\n\n"+`<xsd:element name="`+root+`" type="`+top+`"/>`+"\n\n</xsd:schema>"
}
func xmlScopeCall(function,schema string,nulls,forest bool,ns string)string{
	args:=fmt.Sprintf("%t,%t,%s",nulls,forest,xmlMapLiteral(ns));if strings.HasPrefix(function,"schema_"){args=xmlMapLiteral(schema)+","+args};return "SELECT "+function+"("+args+") AS v"
}
func xmlScopeBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,query,want string){
	t.Helper();step:=xmlSelect(query,"XML",want);step.CheckMetadata=func(t *testing.T,rows *sql.Rows){xmlMetadata(t,rows,b.PG)};xmlBoth(t,p,b,c,step)
}
func xmlScopeHeld(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,stmt *sql.Stmt,query,want string){
	t.Helper();step:=xmlSelect(query,"XML",want);step.CheckMetadata=func(t *testing.T,rows *sql.Rows){xmlMetadata(t,rows,b.PG)}
	fixture.RunStep(t,p.Ctx,c,b.Recorder,step);fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query},b.Recorder,step)
}
func xmlScopeSetup(t *testing.T,p *fixture.Pair,c *sql.Conn,schema string){
	t.Helper();xmlMapExec(t,p,c,"CREATE SCHEMA "+schema,"CREATE TABLE "+schema+".z(a int)","INSERT INTO "+schema+".z VALUES(2)","CREATE TABLE "+schema+".a(a int,b int)","INSERT INTO "+schema+".a VALUES(1,NULL)","CREATE TABLE "+schema+".empty(a int)")
}
