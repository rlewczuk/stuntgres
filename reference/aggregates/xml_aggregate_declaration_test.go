package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLAggDeclarations(t *testing.T) {
	xmlAggScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		run(fixture.Exec("CREATE TABLE xa(k int,x xml)",0))
		const yes=`<?xml version="1.0" standalone="yes"?>`
		const no=`<?xml version="1.0" standalone="no"?>`
		const v11=`<?xml version="1.1"?>`
		const adjacent=`<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`
		var count int64
		for _,tc:=range []struct{name string; inputs []string; raw,typed string}{
			{"singleton",[]string{"<?xml version='1.0' encoding='UTF8'?><a/>"},"<?xml version='1.0' encoding='UTF8'?><a/>","<a/>"},
			{"default",[]string{`<?xml version="1.0"?><a/>`,`<?xml version="1.0"?><b/>`},"<a/><b/>","<a/><b/>"},
			{"yes",[]string{yes+"<a/>",yes+"<b/>"},yes+"<a/><b/>",yes+"<a/><b/>"},
			{"no",[]string{yes+"<a/>",no+"<b/>"},no+"<a/><b/>",no+"<a/><b/>"},
			{"absent",[]string{yes+"<a/>","<b/>"},"<a/><b/>","<a/><b/>"},
			{"version",[]string{v11+"<a/>",v11+"<b/>"},v11+"<a/><b/>",v11+"<a/><b/>"},
			{"triple-version",[]string{v11+"<a/>",v11+"<b/>",v11+"<c/>"},v11+"<a/><b/><c/>",v11+"<a/><b/><c/>"},
			{"different",[]string{v11+"<a/>",`<?xml version="1.0"?><b/>`},"<a/><b/>","<a/><b/>"},
			{"triple-default-suppression",[]string{`<?xml version="1.0"?><a/>`,`<?xml version="1.0"?><b/>`,v11+"<c/>"},"<a/><b/><c/>","<a/><b/><c/>"},
			{"triple-standalone-default",[]string{`<?xml version="1.1" standalone="yes"?><a/>`,yes+"<b/>",yes+"<c/>"},yes+"<a/><b/><c/>",yes+"<a/><b/><c/>"},
			{"linefeed",[]string{"\n<a/>","<b/>"},"\n<a/><b/>","<a/><b/>"},
			{"reader-boundary",[]string{adjacent,"<b/>"},"<b/>","<b/>"},
			{"dtd",[]string{"<!DOCTYPE a><a/>","<!DOCTYPE b><b/>"},"<!DOCTYPE a><a/><!DOCTYPE b><b/>","<!DOCTYPE a><a/><!DOCTYPE b><b/>"},
		}{t.Run(tc.name,func(t *testing.T){
			run(fixture.Exec("DELETE FROM xa",count))
			for i,input:=range tc.inputs { mode:="CONTENT"; if tc.name=="reader-boundary" {mode="DOCUMENT"}; q:="INSERT INTO xa VALUES($1,XMLPARSE("+mode+" $2))";run(fixture.Exec(q,1,i,input)) }
			run(fixture.Exec("INSERT INTO xa VALUES(-1,NULL),(100,NULL)",2))
			run(fixture.Exec("SET XML OPTION DOCUMENT",0))
			xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x ORDER BY k)::text AS v FROM xa","TEXT",tc.raw))
			xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x ORDER BY k) AS v FROM xa","XML",tc.typed))
			run(fixture.Exec("SET XML OPTION CONTENT",0))
			count = int64(len(tc.inputs)+2)
		})}
		// A singleton custom-reader-ineligible value is still byte-identical.
		run(fixture.Exec("DELETE FROM xa",count))
		run(fixture.Exec("INSERT INTO xa VALUES(1,XMLPARSE(DOCUMENT '"+strings.ReplaceAll(adjacent,"'","''")+"'))",1))
		xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x)::text AS v FROM xa","TEXT",adjacent))
	})
	t.Run("no-warning-replay",xmlAggNoWarningReplay)
}

func xmlAggNoWarningReplay(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
		var notices []fixture.Diagnostic
		_,c,rec:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
		b.Recorder=rec
		fixture.RunStep(t,p.Ctx,c,rec,fixture.Exec("CREATE TABLE xa(k int,x xml)",0))
		for i,tag:=range []string{"a","b"}{
			fixture.RunStep(t,p.Ctx,c,rec,fixture.Exec("INSERT INTO xa VALUES($1,XMLPARSE(DOCUMENT $2))",1,i,`<?xml version="1.1"?><`+tag+`/>`))
			want:=fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"01000",Message:"line 1: Unsupported version '1.1'\n<?xml version=\"1.1\"?><"+tag+"/>\n                   ^"}
			if len(notices)!=1||notices[0]!=want{t.Fatalf("input warning %#v want %#v",notices,want)}
			notices=nil
		}
		for range 2 {
			xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x ORDER BY k)::text AS v FROM xa","TEXT",`<?xml version="1.1"?><a/><b/>`))
			if len(notices)!=0{t.Fatalf("aggregate replayed input diagnostics: %#v",notices)}
		}
	})}
}
