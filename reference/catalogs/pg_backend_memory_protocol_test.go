package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

// The existing public server fixture is required only for real protocol
// Parse/Describe ownership and origin fields unavailable through database/sql.
// Both servers execute this one body; no oracle-only copied scenario exists.
func backendMemoryProtocol(t *testing.T) {
	t.Helper()
	f:=fixture.NewServerFixture(t)
	for _,b:=range f.Backends(){t.Run(b.Name,func(t *testing.T){
		c,err:=pgconn.Connect(f.Ctx,f.DSNFor(b));if err!=nil{t.Fatal(err)}
		defer c.Close(f.Ctx)
		if deadline,ok:=f.Ctx.Deadline();ok{if err:=c.Conn().SetDeadline(deadline);err!=nil{t.Fatal(err)}}
		id,err:=c.Exec(f.Ctx,"SELECT oid::bigint FROM pg_catalog.pg_class WHERE oid='pg_catalog.pg_backend_memory_contexts'::regclass").ReadAll()
		if err!=nil{t.Fatal(err)}
		if len(id)!=1||len(id[0].Rows)!=1{t.Fatal("relation identity",id)}
		relation,err:=strconv.ParseUint(string(id[0].Rows[0][0]),10,32);if err!=nil||relation==0{t.Fatal(relation,err)}
		for _,q:=range []struct{sql string;view,record bool}{
			{backendMemoryView+" LIMIT 0",true,false},
			{backendMemoryFunction+" LIMIT 0",false,false},
			{"SELECT (pg_catalog.pg_get_backend_memory_contexts()).* LIMIT 0",false,false},
			{"SELECT pg_catalog.pg_get_backend_memory_contexts() AS context LIMIT 0",false,true},
		}{
			description,err:=c.Prepare(f.Ctx,"backend_memory_metadata",q.sql,nil);if err!=nil{t.Fatal(err)}
			backendMemoryWireFields(t,description.Fields,uint32(relation),q.view,q.record)
			fields,rows:=backendMemoryWireExecute(t,f.Ctx,c,"backend_memory_metadata")
			if len(rows)!=0{t.Fatal("empty protocol result",rows)}
			backendMemoryWireFields(t,fields,uint32(relation),q.view,q.record)
			backendMemoryWireClose(t,f.Ctx,c,"backend_memory_metadata")
		}
		if _,err:=c.Prepare(f.Ctx,"backend_memory_capture",backendMemoryView,nil);err!=nil{t.Fatal(err)}
		capture:=func() []backendMemoryRow {
			fields,values:=backendMemoryWireExecute(t,f.Ctx,c,"backend_memory_capture")
			backendMemoryWireFields(t,fields,uint32(relation),true,false)
			var rows []backendMemoryRow
			for _,fields:=range values{
				if len(fields)!=10{t.Fatal(fields)}
				values:=make([]string,10);for i,value:=range fields{values[i]=string(value)}
				level,err:=strconv.ParseInt(values[3],10,64);if err!=nil{t.Fatal(err)}
				// Reuse the same independently specified invariant checker, with
				// text-mode protocol carriers decoded through public SQL scanners.
				row:=backendMemoryWireRow(t,values,level)
				rows=append(rows,row)
			}
			backendMemoryTopology(t,rows,b.PG)
			return rows
		}
		baseline:=capture()
		if _,err:=c.Prepare(f.Ctx,"backend_memory_named","SELECT 42",nil);err!=nil{t.Fatal(err)}
		one:=capture();var delta int64
		if !b.PG{delta=backendMemoryDelta(t,baseline,one,0)}
		// Named duplicate rejects publication rather than replacing the owner.
		_,err=c.Prepare(f.Ctx,"backend_memory_named","SELECT 7",nil)
		fixture.AssertDiagnostic(t,err,&fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42P05",Message:`prepared statement "backend_memory_named" already exists`})
		if !b.PG{backendMemorySameOwn(t,capture(),one)}else{capture()}
		if _,err:=c.Prepare(f.Ctx,"","SELECT 7",nil);err!=nil{t.Fatal(err)}
		two:=capture();if !b.PG{backendMemoryDelta(t,one,two,delta)}
		if _,err:=c.Prepare(f.Ctx,"","SELECT 'replacement with a different source length'::text",nil);err!=nil{t.Fatal(err)}
		replaced:=capture();if !b.PG{backendMemorySameOwn(t,replaced,two)}
		backendMemoryWireClose(t,f.Ctx,c,"backend_memory_named")
		after:=capture();if !b.PG{backendMemoryDelta(t,baseline,after,delta)}
		backendMemoryWireClose(t,f.Ctx,c,"")
		final:=capture();if !b.PG{backendMemorySameOwn(t,final,baseline)}
	})}
}

func backendMemoryWireFields(t *testing.T,fields []pgconn.FieldDescription,relation uint32,view,record bool) {
	t.Helper()
	names,ids:=backendMemoryNames,backendMemoryTypeIDs
	if record{names=[]string{"context"};ids=[]int64{2249}}
	if len(fields)!=len(ids){t.Fatal("wire field count",fields)}
	for i,field:=range fields {
		size:=int16(-1);if ids[i]==23{size=4};if ids[i]==20{size=8}
		table,attribute:=uint32(0),uint16(0);if view{table=relation;attribute=uint16(i+1)}
		if field.Name!=names[i]||field.DataTypeOID!=uint32(ids[i])||field.DataTypeSize!=size||field.TypeModifier!=-1||field.Format!=0||field.TableOID!=table||field.TableAttributeNumber!=attribute{t.Fatal("exact wire descriptor",i,field,"origin",table,attribute,"type",ids[i],size)}
	}
}

func backendMemoryWireRow(t *testing.T,fields []string,level int64) backendMemoryRow {
	t.Helper()
	// A record's CSV quoting is not reused for the wire's separate fields.
	r:=backendMemoryRow{Name:fields[0],Type:fields[2],Level:level}
	if fields[1]!=""{r.Ident.String=fields[1];r.Ident.Valid=true}
	var path pq.Int64Array
	if err:=path.Scan(fields[4]);err!=nil{t.Fatal(err)}
	r.Path=append([]int64(nil),path...)
	for i,dst:=range []*sql.NullInt64{&r.Total,&r.Blocks,&r.Free,&r.Chunks,&r.Used}{if fields[i+5]!=""{n,err:=strconv.ParseInt(fields[i+5],10,64);if err!=nil{t.Fatal(err)};dst.Int64=n;dst.Valid=true}}
	return r
}

func backendMemoryWireClose(t *testing.T,ctx context.Context,c *pgconn.PgConn,name string) {
	t.Helper()
	c.Frontend().Send(&pgproto3.Close{ObjectType:'S',Name:name})
	c.Frontend().Send(&pgproto3.Sync{})
	if err:=c.Frontend().Flush();err!=nil{t.Fatal(err)}
	message,err:=c.ReceiveMessage(ctx)
	if err!=nil{t.Fatal(err)}
	if _,ok:=message.(*pgproto3.CloseComplete);!ok{t.Fatalf("statement Close: %#v",message)}
	message,err=c.ReceiveMessage(ctx)
	if err!=nil{t.Fatal(err)}
	if ready,ok:=message.(*pgproto3.ReadyForQuery);!ok||ready.TxStatus!='I'{t.Fatalf("statement Close ready: %#v",message)}
}

// Execute alone does not send RowDescription. Request the bound portal's
// descriptor explicitly, so both empty and populated executions independently
// exercise actual result origins/types rather than reusing statement Describe.
func backendMemoryWireExecute(t *testing.T,ctx context.Context,c *pgconn.PgConn,name string) ([]pgconn.FieldDescription,[][][]byte) {
	t.Helper()
	c.Frontend().Send(&pgproto3.Bind{PreparedStatement:name})
	c.Frontend().Send(&pgproto3.Describe{ObjectType:'P'})
	c.Frontend().Send(&pgproto3.Execute{})
	c.Frontend().Send(&pgproto3.Sync{})
	if err:=c.Frontend().Flush();err!=nil{t.Fatal(err)}
	var fields []pgconn.FieldDescription
	var rows [][][]byte
	bound,described,completed:=false,false,false
	for {
		message,err:=c.ReceiveMessage(ctx)
		if err!=nil{t.Fatal(err)}
		switch m:=message.(type) {
		case *pgproto3.BindComplete:
			if bound||described||completed{t.Fatalf("unexpected BindComplete: %#v",m)}
			bound=true
		case *pgproto3.RowDescription:
			if !bound||described||completed{t.Fatalf("unexpected RowDescription: %#v",m)}
			described=true
			for _,f:=range m.Fields{
				fields=append(fields,pgconn.FieldDescription{Name:string(f.Name),TableOID:f.TableOID,TableAttributeNumber:f.TableAttributeNumber,DataTypeOID:f.DataTypeOID,DataTypeSize:f.DataTypeSize,TypeModifier:f.TypeModifier,Format:f.Format})
			}
		case *pgproto3.DataRow:
			if !described||completed||len(m.Values)!=len(fields){t.Fatalf("unexpected DataRow: %#v",m)}
			values:=make([][]byte,len(m.Values))
			for i,value:=range m.Values{if value!=nil{values[i]=append([]byte{},value...)}}
			rows=append(rows,values)
		case *pgproto3.CommandComplete:
			if !described||completed||string(m.CommandTag)!="SELECT "+strconv.Itoa(len(rows)){t.Fatalf("unexpected SELECT completion: %#v rows=%d",m,len(rows))}
			completed=true
		case *pgproto3.ReadyForQuery:
			if !bound||!described||!completed||m.TxStatus!='I'{t.Fatalf("incomplete described execution: %#v bound=%t described=%t completed=%t",m,bound,described,completed)}
			return fields,rows
		default:
			t.Fatalf("unexpected described execution response: %#v",message)
		}
	}
}
