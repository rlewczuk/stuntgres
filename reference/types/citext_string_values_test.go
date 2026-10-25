package reference_test

import (
 "database/sql"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceCitextStringValues(t *testing.T) {
 for _,group:=range []string{"replace","split_part","strpos","translate","corpus","metadata"} {t.Run(group,func(t *testing.T){
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 cases:=map[string][]struct{expr,typ string;value any}{
 "replace":{
  {"replace('aAa'::citext,'A','X')","TEXT","XXX"},
  {"replace('ab^is$Ab'::citext,'^IS$','X')","TEXT","abXAb"},
  {"replace('ab'::citext,'','X')","TEXT","XaXbX"},
  {"replace(''::citext,'','X')","TEXT","X"},
  {`replace('AbA'::citext,'a',$$<\&>$$)`,"TEXT","<A>b<A>"},
  {`replace('AbA'::citext,'a',$$\1$$)`,"TEXT","b"},
  {`replace('AbA'::citext,'a',$$\9$$)`,"TEXT","b"},
  {`replace('A'::citext,'a',$$\\$$)`,"TEXT",`\`},
  {`replace('A'::citext,'a',$$\q$$)`,"TEXT",`\q`},
  {`replace('A'::citext,'a',$$\$$)`,"TEXT",`\`},
  {"replace('A'::citext,'a','aa')","TEXT","aa"},
  {"replace('AbA'::citext,'!','X')","TEXT","AbA"},
  {"replace('AbA'::citext,'a','')","TEXT","b"},
 },
 "split_part":{
  {"split_part('aTbT'::citext,'t',1)","TEXT","a"},
  {"split_part('aTbT'::citext,'t',2)","TEXT","b"},
  {"split_part('aTbT'::citext,'t',3)","TEXT",""},
  {"split_part('aTbT'::citext,'t',4)","TEXT",nil},
  {"split_part('aTbT'::citext,'t',0)","TEXT",nil},
  {"split_part('aTbT'::citext,'t',-1)","TEXT",nil},
  {"split_part('aTbT'::citext,'t',2147483647)","TEXT",nil},
  {"split_part('é🙂x'::citext,'',2)","TEXT","🙂"},
  {"split_part(''::citext,'',1)","TEXT",""},
  {"split_part(''::citext,'',2)","TEXT",nil},
  {"split_part('AbC'::citext,'!',1)","TEXT","AbC"},
  {"split_part('AbC'::citext,'!',2)","TEXT",nil},
  {"split_part('TTaTT'::citext,'t',1)","TEXT",""},
  {"split_part('TTaTT'::citext,'t',2)","TEXT",""},
  {"split_part('TTaTT'::citext,'t',3)","TEXT","a"},
  {"split_part('TTaTT'::citext,'t',5)","TEXT",""},
 },
 "strpos":{
  {"strpos('é🙂AbA'::citext,'a')","INT4",int64(3)},
  {"strpos('AbA'::citext,'')","INT4",int64(1)},
  {"strpos('AbA'::citext,'!')","INT4",int64(0)},
  {"strpos(''::citext,'')","INT4",int64(1)},
  {"strpos(''::citext,'a')","INT4",int64(0)},
  {"strpos('Éé'::citext,'é')","INT4",int64(2)},
  {"strpos('éÉ'::citext,'é')","INT4",int64(0)},
 },
 "translate":{
  {"translate('aAbB'::citext,'ab','BA')","TEXT","ABBA"},
  {"translate('AaBbCc'::citext,'abc','x')","TEXT","xx"},
  {"translate('aA'::citext,'aa','XY')","TEXT","XX"},
  {"translate('AbC'::citext,'','x')","TEXT","AbC"},
  {"translate('AaBb'::citext,'ab','')","TEXT",""},
  {"translate('aA'::citext,'a','xyz')","TEXT","xx"},
  {"translate('é🙂É'::citext,'é🙂','xy')","TEXT","xyÉ"},
  {"translate(''::citext,'a','X')","TEXT",""},
 },
 }
 for _,tc:=range cases[group] {citextStringRoutes(t,p,b,c,citextStringValue("SELECT "+tc.expr+" AS v",tc.typ,tc.value))}
 if group=="replace"||group=="split_part" {
  for _,literal:=range []string{".","^","$","[","]","(",")","{","}","*","+","?","|",`\`,"'","\n","é","é"} {
   q:="SELECT public."+group+"($1::citext,$2::citext,$3::"
   last:=any("X");want:="aXbX";typ:="citext"
   if group=="split_part" {last=int64(2);want="b";typ="integer"}
   q+=typ+") AS v"
   citextStringRoutes(t,p,b,c,citextStringValue(q,"TEXT",want,"a"+literal+"b"+literal,literal,last))
  }
 }
 if group=="corpus" {
  citextStringCorpus(t,p,b,c);citextStringCorpusChecks(t,p,b,c)
  for _,expr:=range []string{"replace('A'::citext,'a','X')","split_part('X'::citext,'!',1)","translate('A'::citext,'a','X')"} {
   citextStringRoutes(t,p,b,c,citextPatternBool("SELECT "+expr+"='x' AS v",false))
   citextStringRoutes(t,p,b,c,citextPatternBool("SELECT ("+expr+")::citext='x' AS v",true))
  }
 }
 if group=="metadata" {
  citextStringCorpus(t,p,b,c)
  q:=strings.Replace(citextStringProjection,"id,","",1)
  for _,suffix:=range []string{" ORDER BY id"," WHERE id=3"," WHERE false"} {for _,prepared:=range []bool{false,true} {
   var rows *sql.Rows;var err error
   if prepared {stmt,e:=c.PrepareContext(p.Ctx,q+suffix);if e!=nil {t.Fatal(e)};defer stmt.Close();rows,err=stmt.QueryContext(p.Ctx)} else {rows,err=c.QueryContext(p.Ctx,q+suffix)}
   if err!=nil {t.Fatal(err)};defer rows.Close();citextStringMetadata(t,b,rows)
   for rows.Next() {var r,s,pos,tr any;if err:=rows.Scan(&r,&s,&pos,&tr);err!=nil {t.Fatal(err)}}
   if err:=rows.Err();err!=nil {t.Fatal(err)};rows.Close()
  }}
 }
 })})}
}
