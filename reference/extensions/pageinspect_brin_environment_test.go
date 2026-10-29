package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferencePageinspectBrinOutputEnvironment(t *testing.T){
 for _,tc:=range []struct{
  name,typ string
  datum []byte
  align int
  short bool
  settings []struct{sql,want string}
 }{
  {name:"bytea",typ:"bytea",datum:brinShort([]byte{0,255}),align:4,short:true,settings:[]struct{sql,want string}{
   {"SET bytea_output TO 'hex'",`{\x00ff .. \x00ff}`},
   {"SET bytea_output TO 'escape'",`{\000\377 .. \000\377}`},
   {"SET bytea_output TO 'hex'",`{\x00ff .. \x00ff}`},
  }},
  {name:"date",typ:"date",datum:brinLE32(1),align:4,settings:[]struct{sql,want string}{
   {"SET datestyle TO 'ISO, MDY'","{2000-01-02 .. 2000-01-02}"},
   {"SET datestyle TO 'SQL, DMY'","{02/01/2000 .. 02/01/2000}"},
   {"SET datestyle TO 'ISO, MDY'","{2000-01-02 .. 2000-01-02}"},
  }},
 }{
  t.Run(tc.name,func(t *testing.T){brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   for _,q:=range []string{"CREATE TABLE pi_env(n "+tc.typ+")","CREATE INDEX pi_env_idx ON pi_env USING brin(n)"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
   defer func(){if _,err:=c.ExecContext(p.Ctx,tc.settings[0].sql);err!=nil{t.Error(err)}}()
   q:="SELECT * FROM brin_part.brin_page_items($1,'pi_env_idx'::regclass)"
   statement,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer statement.Close()
   held:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
   // Reuse this exact physical image for every execution, including A/B/A
   // setting changes after preparation. Only the current output environment
   // changes; neither the descriptor nor any stored bits are replaced.
   image:=brinItems(brinTuple(0,nil,brinPair(tc.datum,tc.align,tc.short)))
   for _,setting:=range tc.settings{
    fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(setting.sql,0))
    s:=fixture.BRINMetadata(fixture.Select(q,brinItemNames,brinItemTypes,[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,setting.want}},image),b.PG,false)
    fixture.BRINBoth(t,p,b,c,s)
    fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   }
  })})
 }
}
