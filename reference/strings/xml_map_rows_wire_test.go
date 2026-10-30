package reference_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

func xmlMapRowsProtocol(t *testing.T){
	f:=fixture.NewServerFixture(t)
	for _,b:=range f.Backends(){t.Run(b.Name,func(t *testing.T){
		c,err:=pgconn.Connect(f.Ctx,f.DSNFor(b));if err!=nil{t.Fatal(err)};defer c.Close(f.Ctx)
		if deadline,ok:=f.Ctx.Deadline();ok{if err:=c.Conn().SetDeadline(deadline);err!=nil{t.Fatal(err)}}
		send:=func(messages ...pgproto3.FrontendMessage){t.Helper();for _,m:=range messages{c.Frontend().Send(m)};if err:=c.Frontend().Flush();err!=nil{t.Fatal(err)}}
		receive:=func()pgproto3.BackendMessage{t.Helper();for{m,err:=c.ReceiveMessage(f.Ctx);if err!=nil{t.Fatal(err)};if _,ok:=m.(*pgproto3.ParameterStatus);ok{continue};return m}}
		ready:=func(status byte){t.Helper();m:=receive();r,ok:=m.(*pgproto3.ReadyForQuery);if !ok||r.TxStatus!=status{t.Fatalf("ready %#v want %c",m,status)}}
		exec:=func(q string){t.Helper();if _,err:=c.Exec(f.Ctx,q).ReadAll();err!=nil{t.Fatal(q,err)}}
		value:=func(q,want string){t.Helper();r:=c.ExecParams(f.Ctx,q,nil,nil,nil,nil).Read();if r.Err!=nil{t.Fatal(q,r.Err)};if len(r.Rows)!=1||len(r.Rows[0])!=1||string(r.Rows[0][0])!=want{t.Fatalf("%s got %#v want %q",q,r.Rows,want)}}
		bind:=func(name,q string){t.Helper();send(&pgproto3.Parse{Name:name,Query:q},&pgproto3.Bind{DestinationPortal:name,PreparedStatement:name},&pgproto3.Sync{});if m:=receive();m==nil{t.Fatal("missing parse")}else if _,ok:=m.(*pgproto3.ParseComplete);!ok{t.Fatalf("parse %#v",m)};if m:=receive();m==nil{t.Fatal("missing bind")}else if _,ok:=m.(*pgproto3.BindComplete);!ok{t.Fatalf("bind %#v",m)};ready('T')}
		// Generated spelling must survive genuine XML result format 0 and 1.
		for _,rf:=range []int16{0,1}{for _,tc:=range []struct{query,want string}{
			{`SELECT query_to_xml('SELECT 1 AS a',false,false,'a"b') AS v`,xmlMapDocument("table",`a"b`,xmlMapRow("  <a>1</a>\n"))},
			{`SELECT query_to_xml('SELECT chr(1) AS x',false,false,'') AS v`,xmlMapDocument("table","",xmlMapRow("  <x>\x01</x>\n"))},
		}{r:=c.ExecParams(f.Ctx,tc.query,nil,nil,nil,[]int16{rf}).Read();if r.Err!=nil{t.Fatal(r.Err)};xmlWireField(t,r.FieldDescriptions,142,-1,-1,rf);if len(r.Rows)!=1||len(r.Rows[0])!=1||string(r.Rows[0][0])!=tc.want{t.Fatalf("generated format%d %#v want %q",rf,r.Rows,tc.want)}}}
		exec(`BEGIN`)
		bind("xr_portal",`SELECT g AS x FROM generate_series(1,3) g`)
		value(`SELECT cursor_to_xml('xr_portal',1,false,true,'')::text`,xmlMapForest("row","","  <x>1</x>\n"))
		value(`SELECT cursor_to_xmlschema('xr_portal',false,true,'')::text`,xmlMapIntSchema("x","row","","",false,true))
		send(&pgproto3.Execute{Portal:"xr_portal",MaxRows:1},&pgproto3.Sync{})
		m:=receive();row,ok:=m.(*pgproto3.DataRow);if !ok||len(row.Values)!=1||string(row.Values[0])!="2"{t.Fatalf("shared protocol position %#v",m)}
		if m:=receive();m==nil{t.Fatal("missing suspension")}else if _,ok:=m.(*pgproto3.PortalSuspended);!ok{t.Fatalf("suspended %#v",m)};ready('T')
		value(`SELECT cursor_to_xml('xr_portal',1,false,true,'')::text`,xmlMapForest("row","","  <x>3</x>\n"))
		exec(`ROLLBACK`)
		d:=xmlError("34000",`cursor "xr_portal" does not exist`,"",0)
		r:=c.ExecParams(f.Ctx,`SELECT cursor_to_xml('xr_portal',1,false,true,'')`,nil,nil,nil,nil).Read();fixture.AssertDiagnostic(t,r.Err,&d)
		exec(`BEGIN`)
		bind("xr_utility",`SET application_name='xml_rows'`)
		r=c.ExecParams(f.Ctx,`SELECT cursor_to_xmlschema('xr_utility',false,true,'')`,nil,nil,nil,nil).Read();d=xmlError("24000",`portal "xr_utility" does not return tuples`,"",0);fixture.AssertDiagnostic(t,r.Err,&d)
		exec(`ROLLBACK`)
		exec(`BEGIN`)
		bind("xr_failed_protocol",`SELECT g/(g-2) AS x FROM generate_series(1,3) g`)
		exec(`SAVEPOINT xr_protocol_before`)
		send(&pgproto3.Execute{Portal:"xr_failed_protocol",MaxRows:1},&pgproto3.Sync{})
		m=receive();row,ok=m.(*pgproto3.DataRow);if !ok||len(row.Values)!=1||string(row.Values[0])!="-1"{t.Fatalf("first protocol row %#v",m)}
		if m:=receive();m==nil{t.Fatal("missing suspension")}else if _,ok:=m.(*pgproto3.PortalSuspended);!ok{t.Fatalf("suspended %#v",m)};ready('T')
		send(&pgproto3.Execute{Portal:"xr_failed_protocol",MaxRows:1},&pgproto3.Sync{})
		m=receive();failure,ok:=m.(*pgproto3.ErrorResponse);if !ok{t.Fatalf("failed producer %#v",m)};d=xmlError("22012","division by zero","",0);fixture.AssertDiagnostic(t,pgconn.ErrorResponseToPgError(failure),&d);ready('E')
		exec(`ROLLBACK TO xr_protocol_before`)
		value(`SELECT cursor_to_xmlschema('xr_failed_protocol',false,true,'')::text`,xmlMapIntSchema("x","row","","",false,true))
		r=c.ExecParams(f.Ctx,`SELECT cursor_to_xml('xr_failed_protocol',1,false,true,'')`,nil,nil,nil,nil).Read();d=xmlError("55000",`portal "xr_failed_protocol" cannot be run`,"",0);fixture.AssertDiagnostic(t,r.Err,&d)
		exec(`ROLLBACK`)
		for _,count:=range []int{0,-1}{
			exec(`BEGIN`)
			name:="xr_motion_zero";if count<0{name="xr_motion_back"}
			bind(name,`SELECT g AS x FROM generate_series(1,3) g`)
			if count==0{value(`SELECT cursor_to_xml('xr_motion_zero',0,false,true,'')::text`,"");value(`SELECT cursor_to_xml('xr_motion_zero',1,false,true,'')::text`,xmlMapForest("row","","  <x>1</x>\n"))}
			exec(`SAVEPOINT xr_motion_before`)
			q:=`SELECT cursor_to_xml('xr_motion_zero',0,false,true,'')`;if count<0{q=`SELECT cursor_to_xml('xr_motion_back',-1,false,true,'')`}
			r=c.ExecParams(f.Ctx,q,nil,nil,nil,nil).Read();d=xmlError("55000","cursor can only scan forward","",0);d.Hint="Declare it with SCROLL option to enable backward scan.";fixture.AssertDiagnostic(t,r.Err,&d)
			exec(`ROLLBACK TO xr_motion_before`)
			value(`SELECT cursor_to_xmlschema(`+xmlMapLiteral(name)+`,false,true,'')::text`,xmlMapIntSchema("x","row","","",false,true))
			exec(`ROLLBACK`)
		}
		exec(`BEGIN`)
		bind("xr_self",`SELECT cursor_to_xml('xr_self',1,false,true,'') AS x`)
		send(&pgproto3.Execute{Portal:"xr_self",MaxRows:1},&pgproto3.Sync{})
		m=receive();failure,ok=m.(*pgproto3.ErrorResponse);if !ok{t.Fatalf("active data portal %#v",m)};d=xmlError("55000",`portal "xr_self" cannot be run`,"",0);fixture.AssertDiagnostic(t,pgconn.ErrorResponseToPgError(failure),&d);ready('E')
		exec(`ROLLBACK`)
		exec(`BEGIN`)
		bind("xr_self_schema",`SELECT cursor_to_xmlschema('xr_self_schema',false,true,'') AS x`)
		send(&pgproto3.Execute{Portal:"xr_self_schema",MaxRows:1},&pgproto3.Sync{})
		xmlType:="<xsd:complexType mixed=\"true\">\n  <xsd:sequence>\n    <xsd:any name=\"element\" minOccurs=\"0\" maxOccurs=\"unbounded\" processContents=\"skip\"/>\n  </xsd:sequence>\n</xsd:complexType>\n\n"
		want:=xmlMapSchema(xmlType,xmlMapColumn("x","XML",false),"row","","",true)
		m=receive();row,ok=m.(*pgproto3.DataRow);if !ok||len(row.Values)!=1||string(row.Values[0])!=want{t.Fatalf("active schema portal %#v want %q",m,want)}
		if m:=receive();m==nil{t.Fatal("missing suspension")}else if _,ok:=m.(*pgproto3.PortalSuspended);!ok{t.Fatalf("active schema suspension %#v",m)};ready('T')
		exec(`ROLLBACK`)
	})}
}
