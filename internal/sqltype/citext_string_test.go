package sqltype

import (
 "context"
 "errors"
 "testing"
)

func TestCitextStringPlan(t *testing.T) {
 types:=mathTypes();scalar:=citextUnitType(t,45000);text:=types[Text].Usage()
 for _,name:=range []string{"strpos","replace","split_part","translate"} {
  args:=[]Usage{scalar.Usage(),scalar.Usage()};result:=text
  switch name {case "strpos":result=types[Int4].Usage();case "replace":args=append(args,scalar.Usage());case "split_part":args=append(args,types[Int4].Usage());case "translate":args=append(args,text)}
  p,err:=BindCitextString(name,result,text,args);if err!=nil {t.Fatal(name,err)}
  if !p.Citext()||!p.Strict()||p.Result()!=result||p.TextUsage()!=text||p.CollationIndeterminate() {t.Fatal("lost selected mode",name,p)}
  for i,u:=range args {if p.ArgumentUsage(i)!=u {t.Fatal("lost actual formal",name,i)}}
  foreign:=citextUnitType(t,scalar.ID())
  bad:=append([]Usage(nil),args...);bad[1]=foreign.Usage()
  if _,err:=BindCitextString(name,result,text,bad);err==nil {t.Fatal("foreign scalar generation accepted",name)}
  bad=append([]Usage(nil),args...);bad[0]=text
  if _,err:=BindCitextString(name,result,text,bad);err==nil {t.Fatal("ordinary text selected as citext",name)}
  if _,err:=BindCitextString(name,scalar.Usage(),text,args);err==nil {t.Fatal("citext result accepted",name)}
  if name!="strpos" {
   bad=append([]Usage(nil),args...);bad[2]=types[Bool].Usage()
   if _,err:=BindCitextString(name,result,text,bad);err==nil {t.Fatal("wrong third formal accepted",name)}
  }
 }
 if _,err:=BindCitextString("lower",text,text,[]Usage{scalar.Usage(),scalar.Usage(),text});err==nil {t.Fatal("extra overload admitted")}
}
func TestCitextStringKernel(t *testing.T) {
 ctx:=context.Background();types:=mathTypes();scalar:=citextUnitType(t,45000);text:=types[Text]
 cases:=[]struct{name,source,search,replacement string;index int64;want any}{
  {"replace","aAa","A","X",0,"XXX"}, {"replace","ab^is$Ab","^IS$","X",0,"abXAb"},
  {"replace","ab","","X",0,"XaXbX"},{"replace","","","X",0,"X"},
  {"replace","AbA","a",`<\&>`,0,"<A>b<A>"},{"replace","AbA","a",`\1`,0,"b"},
  {"replace","A","a",`\\`,0,`\`},{"replace","A","a",`\q`,0,`\q`},{"replace","A","a",`\`,0,`\`},
  {"replace","A","a","aa",0,"aa"},{"replace","AbA","!","X",0,"AbA"},
  {"split_part","aTbT","t","",1,"a"},{"split_part","aTbT","t","",2,"b"},
  {"split_part","aTbT","t","",3,""},{"split_part","aTbT","t","",4,nil},
  {"split_part","aTbT","t","",0,nil},{"split_part","aTbT","t","",-1,nil},
  {"split_part","é🙂x","","",2,"🙂"},{"split_part","","","",1,""},{"split_part","","","",2,nil},
  {"split_part","a.b.",".","",2,"b"},{"split_part","AbC","!","",1,"AbC"},
  {"strpos","é🙂AbA","a","",0,int64(3)},{"strpos","AbA","","",0,int64(1)},
  {"strpos","AbA","!","",0,int64(0)},{"strpos","Éé","é","",0,int64(2)},
  {"translate","aAbB","ab","BA",0,"ABBA"},{"translate","AaBbCc","abc","x",0,"xx"},
  {"translate","aA","aa","XY",0,"XX"},{"translate","AbC","","x",0,"AbC"},
  {"translate","AaBb","ab","",0,""},
 }
 state:=new(PGRegexExecution)
 for _,tc:=range cases {
  args:=[]Usage{scalar.Usage(),scalar.Usage()};values:=[4]Value{String(scalar,tc.source),String(scalar,tc.search)};result:=text
  switch tc.name {
  case "strpos":result=types[Int4]
  case "replace":args=append(args,scalar.Usage());values[2]=String(scalar,tc.replacement)
  case "split_part":args=append(args,types[Int4].Usage());values[2],_=Integer(types[Int4],tc.index)
  case "translate":args=append(args,text.Usage());values[2]=String(text,tc.replacement)
  }
  p,err:=BindCitextString(tc.name,result.Usage(),text.Usage(),args);if err!=nil {t.Fatal(tc,err)}
  got,err:=p.EvaluateCitext(ctx,values,InputEnvironment{},OutputEnvironment{},state);if err!=nil {t.Fatal(tc,err)}
  if got.Type()!=result {t.Fatal("wrong result descriptor",tc,got.Type())}
  switch want:=tc.want.(type) {case nil:if !got.IsNull(){t.Fatal(tc,got)};case string:s,e:=got.Text();if e!=nil||s!=want {t.Fatal(tc,s,e)};case int64:n,e:=got.Integer();if e!=nil||n!=want {t.Fatal(tc,n,e)}}
  for i,u:=range args {
   nulls:=values;nulls[i]=Null(u.Type)
   v,e:=p.EvaluateCitext(ctx,nulls,InputEnvironment{},OutputEnvironment{},state);if e!=nil||v!=Null(result) {t.Fatal("strict NULL",tc,i,v,e)}
  }
  bad:=values;bad[0]=String(citextUnitType(t,scalar.ID()),tc.source)
  if _,err:=p.EvaluateCitext(ctx,bad,InputEnvironment{},OutputEnvironment{},state);err==nil {t.Fatal("foreign value accepted",tc)}
  canceled,cancel:=context.WithCancel(ctx);cancel()
  if _,err:=p.EvaluateCitext(canceled,values,InputEnvironment{},OutputEnvironment{},state);!errors.Is(err,context.Canceled){t.Fatal("cancellation",tc,err)}
  if _,err:=p.EvaluateCitext(ctx,values,InputEnvironment{},OutputEnvironment{},state);err!=nil {t.Fatal("recovery",tc,err)}
 }
}
