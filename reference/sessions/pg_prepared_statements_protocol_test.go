//go:build !noservertests

package reference_test

import (
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePGPreparedStatementsProtocol(t *testing.T) {
 f:=fixture.NewServerFixture(t)
 for _,b:=range f.Backends() {t.Run(b.Name,func(t *testing.T) {
  dsn:=f.DSNFor(b);w:=serverAuthenticated(t,dsn);peer:=serverAuthenticated(t,dsn)
  parse:=func(name,source string,oids ...uint32) {
   body:=append([]byte(name),0);body=append(body,[]byte(source)...);body=append(body,0,byte(len(oids)>>8),byte(len(oids)))
   for _,oid:=range oids {body=serverPut32(body,oid)}
   w.send(t,serverPacket('P',body));w.send(t,serverPacket('S',nil));serverWantCode(t,w.ready(t),"")
  }
  source:="SELECT $1::integer AS i"
  parse("pps_wire",source,23)
  q:="SELECT name,statement,parameter_types::text,result_types::text,from_sql,prepare_time::text FROM pg_prepared_statements WHERE name='pps_wire'"
  want:=[][]string{{"pps_wire",source,"{integer}","{integer}","f"}}
  values:=serverValues(t,w.query(t,q));if len(values)!=1||len(values[0])!=6||values[0][5]==""||!reflect.DeepEqual(values[0][:5],want[0]) {t.Fatal("named Parse metadata",values)}
  original:=values[0][5]
  serverScalar(t,peer,"SELECT count(*) FROM pg_prepared_statements WHERE name='pps_wire'","0")
  parse("","SELECT 99");parse("","SELECT 100")
  serverScalar(t,w,"SELECT count(*) FROM pg_prepared_statements WHERE name=''","0")
  if got:=serverValues(t,w.query(t,q));!reflect.DeepEqual(got,values) {t.Fatal("unnamed Parse changed named definition",got,values)}
  bind:=append([]byte("pps_portal\x00pps_wire\x00"),0,0,0,1)
  bind=serverPut32(bind,1);bind=append(bind,'7',0,0)
  w.send(t,serverPacket('B',bind));w.send(t,serverPacket('E',serverPut32([]byte("pps_portal\x00"),0)));w.send(t,serverPacket('S',nil))
  frames:=w.ready(t);serverWantCode(t,frames,"");if got:=serverValues(t,frames);!reflect.DeepEqual(got,[][]string{{"7"}}) {t.Fatal(got)}
  if got:=serverValues(t,w.query(t,q));!reflect.DeepEqual(got,values)||got[0][5]!=original {t.Fatal("Bind/Execute replaced provenance",got)}
  w.send(t,serverPacket('C',append([]byte{'S'},[]byte("pps_wire\x00")...)));w.send(t,serverPacket('S',nil));serverWantCode(t,w.ready(t),"")
  serverScalar(t,w,"SELECT count(*) FROM pg_prepared_statements WHERE name='pps_wire'","0")
  parse("pps_wire",source,23);serverWantCode(t,w.query(t,"DEALLOCATE pps_wire"),"")
  serverScalar(t,w,"SELECT count(*) FROM pg_prepared_statements WHERE name='pps_wire'","0")
  parse("pps_empty","",23,25)
  got:=serverValues(t,w.query(t,"SELECT name,statement,parameter_types::text,result_types IS NULL,from_sql FROM pg_prepared_statements WHERE name='pps_empty'"))
  if !reflect.DeepEqual(got,[][]string{{"pps_empty","","{integer,text}","t","f"}}) {t.Fatal("empty named Parse metadata",got)}
 })}
}
