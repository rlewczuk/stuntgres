package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func xmlMapCursorStep(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,prepared bool,q,typ string,want any){
	t.Helper()
	s:=xmlSelect(q,typ,want);if prepared{s=fixture.Prepared(s)};fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
}

func TestReferenceXMLMapRowsCursors(t *testing.T){
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		defer c.ExecContext(p.Ctx,`ROLLBACK`)
		for _,prepared:=range []bool{false,true}{
			xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_cursor SCROLL CURSOR FOR SELECT g AS x FROM generate_series(1,3) g`)
			fetch:=func(count int,values ...int){t.Helper();want:="";for _,v:=range values{want+=xmlMapForest("row","",fmt.Sprintf("  <x>%d</x>\n",v))};xmlMapCursorStep(t,p,b,c,prepared,fmt.Sprintf(`SELECT cursor_to_xml('xr_cursor',%d,false,true,'') AS v`,count),"XML",want)}
			fetch(0)
			fetch(1,1)
			xmlMapCursorStep(t,p,b,c,prepared,`SELECT cursor_to_xmlschema('xr_cursor',false,false,'') AS v`,"XML",xmlMapIntSchema("x","table","","",false,false))
			fetch(1,2)
			fetch(0,2)
			fetch(-1,1)
			fetch(1,2)
			fetch(10,3)
			fetch(0)
			fetch(1)
			xmlMapCursorStep(t,p,b,c,prepared,`SELECT cursor_to_xml('xr_cursor',1,false,false,'') AS v`,"XML",xmlMapDocument("table","",""))
			xmlMapExec(t,p,c,`CLOSE xr_cursor`,`ROLLBACK`)
			// Negative batches are real backwards traversal, not reverse of a new query.
			xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_cursor SCROLL CURSOR FOR SELECT g AS x FROM generate_series(1,3) g`)
			fetch(3,1,2,3)
			fetch(-2,2,1)
			fetch(-1)
			fetch(1,1)
			xmlMapExec(t,p,c,`ROLLBACK`)
		}
		for _,position:=range []string{"before","current","after"}{for _,count:=range []int{0,-1}{
			xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_forward NO SCROLL CURSOR FOR SELECT g AS x FROM generate_series(1,3) g`)
			if position=="current"{fixture.RunStep(t,p.Ctx,c,b.Recorder,xmlSelect(`SELECT cursor_to_xml('xr_forward',1,false,true,'') AS v`,"XML",xmlMapForest("row","","  <x>1</x>\n")))}
			if position=="after"{fixture.RunStep(t,p.Ctx,c,b.Recorder,xmlSelect(`SELECT cursor_to_xml('xr_forward',4,false,true,'') AS v`,"XML",xmlMapForest("row","","  <x>1</x>\n")+xmlMapForest("row","","  <x>2</x>\n")+xmlMapForest("row","","  <x>3</x>\n")))}
			d:=xmlError("55000","cursor can only scan forward","",0);d.Hint="Declare it with SCROLL option to enable backward scan."
			// At the initial boundary zero fetch has no tuple to rescan.
			q:=fmt.Sprintf(`SELECT cursor_to_xml('xr_forward',%d,false,true,'') AS v`,count)
			if count==0&&position!="current"{fixture.RunStep(t,p.Ctx,c,b.Recorder,xmlSelect(q,"XML",""))}else{step:=fixture.ErrorDiagnostic(q,d);step.SelectRows,step.ErrorStage=true,"execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,step)}
			xmlMapExec(t,p,c,`ROLLBACK`)
		}}
	})
}

func TestReferenceXMLMapRowsCursorLifetime(t *testing.T){
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		defer c.ExecContext(p.Ctx,`ROLLBACK`)
		missing:=func(name string){t.Helper();for _,q:=range []string{`SELECT cursor_to_xml(`+xmlMapLiteral(name)+`,1,false,false,'')`,`SELECT cursor_to_xmlschema(`+xmlMapLiteral(name)+`,false,false,'')`}{xmlFailure(t,p,b,c,q,xmlError("34000",fmt.Sprintf("cursor %q does not exist",name),"",0),false)}}
		missing("missing")
		for _,finish:=range []string{"COMMIT","ROLLBACK"}{xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_life SCROLL CURSOR FOR SELECT 1 AS x`,finish);missing("xr_life")}
		xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_hold SCROLL CURSOR WITH HOLD FOR SELECT g AS x FROM generate_series(1,3) g`,`COMMIT`)
		for _,n:=range []int{1,2}{xmlMapCursorStep(t,p,b,c,n==2,`SELECT cursor_to_xml('xr_hold',1,false,true,'') AS v`,"XML",xmlMapForest("row","",fmt.Sprintf("  <x>%d</x>\n",n)))}
		xmlBoth(t,p,b,c,xmlSelect(`SELECT cursor_to_xmlschema('xr_hold',false,false,'') AS v`,"XML",xmlMapIntSchema("x","table","","",false,false)))
		// An independently borrowed physical session cannot find another session's portal.
		other,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)}
		q:=`SELECT cursor_to_xml('xr_hold',1,false,false,'')`
		xmlFailure(t,p,b,other,q,xmlError("34000",`cursor "xr_hold" does not exist`,"",0),false)
		other.Close()
		xmlMapExec(t,p,c,`CLOSE xr_hold`,`BEGIN`,`DECLARE xr_hold SCROLL CURSOR FOR SELECT 9 AS x`)
		xmlMapCursorStep(t,p,b,c,true,`SELECT cursor_to_xml('xr_hold',1,false,true,'') AS v`,"XML",xmlMapForest("row","","  <x>9</x>\n"))
		xmlMapExec(t,p,c,`CLOSE xr_hold`,`ROLLBACK`)
		missing("xr_hold")
		// Hold promotion does not resurrect a cursor from a rolled-back transaction.
		xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_aborted CURSOR WITH HOLD FOR SELECT 1 AS x`,`ROLLBACK`)
		missing("xr_aborted")
		// Cursor values keep their original tuple authority but mapping settings are live.
		xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_binary SCROLL CURSOR FOR SELECT decode('00ff','hex') AS x FROM generate_series(1,3)`)
		for i,mode:=range []string{"base64","hex","base64"}{xmlMapExec(t,p,c,"SET xmlbinary="+xmlMapLiteral(mode));value:="AP8=";if i==1{value="00FF"};xmlMapCursorStep(t,p,b,c,true,`SELECT cursor_to_xml('xr_binary',1,false,true,'') AS v`,"XML",xmlMapForest("row","","  <x>"+value+"</x>\n"))}
		xmlMapExec(t,p,c,`ROLLBACK`)
		// A failed portal retains its descriptor, but may never run again.
		for _,prepared:=range []bool{false,true}{
			xmlMapExec(t,p,c,`BEGIN`,`DECLARE xr_failed SCROLL CURSOR FOR SELECT 1/(g-2) AS x FROM generate_series(1,3) g`,`SAVEPOINT xr_before_failure`)
			s:=fixture.ErrorDiagnostic(`SELECT cursor_to_xml('xr_failed',2,false,true,'')`,xmlError("22012","division by zero","",0));s.SelectRows,s.ErrorStage=true,"execute";if prepared{s=fixture.Prepared(s)}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
			xmlMapExec(t,p,c,`ROLLBACK TO xr_before_failure`)
			xmlMapCursorStep(t,p,b,c,prepared,`SELECT cursor_to_xmlschema('xr_failed',false,true,'') AS v`,"XML",xmlMapIntSchema("x","row","","",false,true))
			s=fixture.ErrorDiagnostic(`SELECT cursor_to_xml('xr_failed',1,false,true,'')`,xmlError("55000",`portal "xr_failed" cannot be run`,"",0));s.SelectRows,s.ErrorStage=true,"execute";if prepared{s=fixture.Prepared(s)}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
			xmlMapExec(t,p,c,`ROLLBACK`)
		}
	})
	t.Run("protocol",xmlMapRowsProtocol)
}
