package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func xmlMapRowsNotices(t *testing.T){
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,_ *sql.Conn){
		var notices []fixture.Diagnostic
		_,c,rec:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
		warning:=xmlError("XX001","could not parse XML declaration in stored value","Parsing XML declaration: '?>' expected.",0)
		warning.Severity,warning.SeverityUnlocalized="WARNING","WARNING"
		// The inner XML datum's output warns; mapper text itself starts at <table>,
		// so its outer typed output must not issue a duplicate declaration notice.
		inner:=`SELECT xmlroot('<a/>',VERSION 'x" y') AS x`
		q:=`SELECT query_to_xml(`+xmlMapLiteral(inner)+`,false,false,'') AS v`
		want:=xmlMapDocument("table","",xmlMapRow("  <x><?xml version=\"x\" y\"?><a/></x>\n"))
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
		if len(notices)!=0{t.Fatalf("prepare notices %#v",notices)}
		for range 2{fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},rec,xmlSelect(q,"XML",want));if !reflect.DeepEqual(notices,[]fixture.Diagnostic{warning}){t.Fatalf("typed mapping notices %#v",notices)};notices=nil}
		fixture.RunStep(t,p.Ctx,c,rec,xmlSelect(`SELECT query_to_xml(`+xmlMapLiteral(inner)+`,false,false,'') AS v WHERE false`,"XML"))
		if len(notices)!=0{t.Fatalf("undemanded mapping notices %#v",notices)}
	})
}
