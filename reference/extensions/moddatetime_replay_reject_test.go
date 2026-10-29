package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "encoding/base64"
 "errors"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func mdRejectTemporalReplay(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,check func(*sql.Conn,fixture.Backend)) {
 t.Helper()
 node:=`{"kind":"timestamp_exact","scalar":"2001-02-03 04:05:06.123456"}`
 record:=`{"schema":"public","table":"md_attack","cells":[{"column":"stamp","value":`+node+`}]}`
 marker:=func(raw string)string{return "-- stuntgres-domain-null-v1 "+base64.StdEncoding.EncodeToString([]byte(raw))+"\n"}
 prefix:="CREATE TABLE public.md_staged(n integer);CREATE TABLE public.md_attack(stamp timestamp(0));\n"
 insert:="INSERT INTO public.md_attack(stamp) VALUES('2001-02-03 04:05:06.123456');"
 cases:=[]struct{name,sql string}{
  {"unknown_node_field",prefix+marker(strings.Replace(record,`"kind":"timestamp_exact"`,`"kind":"timestamp_exact","extra":1`,1))+insert},
  {"duplicate_json_field",prefix+marker(strings.Replace(record,`"scalar":`,`"kind":"timestamp_exact","scalar":`,1))+insert},
  {"null_payload",prefix+marker(strings.Replace(record,`"2001-02-03 04:05:06.123456"`,`null`,1))+insert},
  {"noncanonical",prefix+marker(strings.Replace(record,"04:05:06.123456","04:05:06.1234560",1))+strings.Replace(insert,"04:05:06.123456","04:05:06.1234560",1)},
  {"fitting",prefix+marker(strings.Replace(record,"04:05:06.123456","04:05:06",1))+strings.Replace(insert,"04:05:06.123456","04:05:06",1)},
  {"mismatched_placeholder",prefix+marker(record)+strings.Replace(insert,".123456",".123457",1)},
  {"typed_placeholder",prefix+marker(record)+strings.Replace(insert,".123456'",".123456'::timestamp",1)},
  {"wrong_destination",strings.Replace(prefix,"stamp timestamp(0)","stamp text",1)+marker(record)+insert},
  {"duplicate_target",prefix+marker(record)+"INSERT INTO public.md_attack(stamp,stamp) VALUES('2001-02-03 04:05:06.123456','2001-02-03 04:05:06.123456');"},
  {"generated_destination",strings.Replace(prefix,"stamp timestamp(0)","stamp timestamp(0) GENERATED ALWAYS AS (timestamp '2000-01-01') STORED",1)+marker(record)+insert},
  {"not_immediate",prefix+marker(record)+"SELECT 1;"+insert},
 }
 for _,tc:=range cases {t.Run(tc.name,func(t *testing.T){
  if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(tc.sql));err==nil {t.Fatal("malformed temporal replay accepted")}
  check(c,b)
  mdRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='md_staged' OR relname='md_attack'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
 })}
 ctx,cancel:=context.WithCancel(p.Ctx)
 err:=stuntgres.Restore(ctx,p.Native,&fixture.EnumCancelReader{Reader:bytes.NewReader([]byte(prefix+marker(record)+insert)),Cancel:cancel});cancel()
 if !errors.Is(err,context.Canceled) {t.Fatal("midstream temporal replay cancellation",err)}
 check(c,b)
}
