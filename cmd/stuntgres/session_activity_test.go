package main

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestSessionActivityWireTransport(t *testing.T) {
	logs:=&serverLogs{ops:logHandler(io.Discard,false),sql:logHandler(io.Discard,false)}
	s,err:=newServer(context.Background(),nil,nil,logs);if err!=nil { t.Fatal(err) };defer s.cancel()
	socket:=framingInput(nil);c,err:=s.newClient(socket);if err!=nil { t.Fatal(err) };defer c.finish(session.CleanClose)
	e:=engine.New();principal,err:=e.ResolveLogin("postgres");if err!=nil { t.Fatal(err) }
	c.db=&database{engine:e};c.session,err=e.StartSession(c.ctx,session.SessionStart{ID:ids.SessionID(c.id),Authenticated:principal});if err!=nil { t.Fatal(err) }
	c.freeBuffers=make(chan []byte,16)
	readRows:=func(want string,count int) {
		t.Helper();if err:=c.wire.flush();err!=nil { t.Fatal(err) }
		data:=socket.output.Bytes();seen:=0
		for len(data)>0 {
			if len(data)<5 { t.Fatal("short frame") };tag:=data[0];size:=int(binary.BigEndian.Uint32(data[1:5]));if size<4 || size+1>len(data) { t.Fatal("bad frame") };body:=data[5:1+size];data=data[1+size:]
			if tag=='E' { t.Fatalf("transport SQL failure %q",body) }
			if tag!='D' { continue };seen++
			input:=protocolInput{body:body};n,err:=input.Uint16();if err!=nil || n!=2 { t.Fatal("field count",n,err) }
			length,err:=input.Int32();if err!=nil { t.Fatal(err) };source,err:=input.Bytes(int(length));if err!=nil || string(source)!=want { t.Fatalf("source %q want %q: %v",source,want,err) }
			length,err=input.Int32();if err!=nil { t.Fatal(err) };pidBytes,err:=input.Bytes(int(length));if err!=nil { t.Fatal(err) };pid,err:=strconv.ParseUint(string(pidBytes),10,32);if err!=nil || uint32(pid)!=c.id || uint64(c.session.Token().ID())!=pid { t.Fatal("backend/client/session identity",pid,c.id,err) }
			if err:=input.End();err!=nil { t.Fatal(err) }
		}
		if seen!=count { t.Fatalf("rows %d want %d",seen,count) };socket.output.Reset()
	}
	query:=" /*λ*/ SELECT current_query(), pg_backend_pid(); SELECT current_query(), pg_backend_pid(); -- tail\n"
	if err:=c.executeSimple(c.ctx,query);err!=nil { t.Fatal(err) };readRows(query,2)
	if err:=c.executeSimple(c.ctx,"BEGIN");err!=nil { t.Fatal(err) };readRows("",0)
	c.cycle=c.session.NewProtocolCycle()
	query="/* extended λ */ SELECT current_query(), pg_backend_pid() FROM generate_series(1,2)"
	parse:=appendString(appendString(nil,"activity"),query);parse=append(parse,0,0)
	bind:=appendString(appendString(nil,"portal"),"activity");bind=append(bind,0,0,0,0,0,0)
	for _,message:=range []frontendMessage{{tag:'P',body:parse},{tag:'B',body:bind}} { if err:=c.extendedCommand(c.ctx,message);err!=nil { t.Fatal(err) };readRows("",0) }
	for range 2 { execute:=appendInt32(appendString(nil,"portal"),1);if err:=c.extendedCommand(c.ctx,frontendMessage{tag:'E',body:execute});err!=nil { t.Fatal(err) };readRows(query,1) }
	if err:=c.executeSimple(c.ctx,"ROLLBACK");err!=nil { t.Fatal(err) };readRows("",0)
	query="SELECT current_query(), pg_backend_pid()"
	if err:=c.executeSimple(c.ctx,query);err!=nil { t.Fatal(err) };readRows(query,1)
}

func TestSessionActivityWireIdentityCapacity(t *testing.T) {
	logs:=&serverLogs{ops:logHandler(io.Discard,false),sql:logHandler(io.Discard,false)}
	s,err:=newServer(context.Background(),nil,nil,logs);if err!=nil { t.Fatal(err) };defer s.cancel()
	s.nextID=math.MaxInt32-1
	c,err:=s.newClient(framingInput(nil));if err!=nil || c.id!=math.MaxInt32 { t.Fatal("last backend identity",c,err) };c.finish(session.CleanClose)
	for _,exhausted:=range []uint32{math.MaxInt32,math.MaxInt32+1,math.MaxUint32} {
		s.nextID=exhausted;before:=len(s.clients)
		c,err:=s.newClient(framingInput(nil));if c!=nil || err==nil || err.Error()!="backend identity exhausted" || s.nextID!=exhausted || len(s.clients)!=before { t.Fatal("exhausted backend allocation",c,err,s.nextID) }
	}
}
