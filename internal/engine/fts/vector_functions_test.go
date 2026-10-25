package fts

import (
 "context"
 "errors"
 "fmt"
 "reflect"
 "testing"
)
func TestFTSVectorFunctionsTransforms(t *testing.T){
 ctx:=context.Background();v,err:=ParseVector(ctx,"cat:3B,7C fat:2,4 rat:5A bare");if err!=nil{t.Fatal(err)}
 before:=v.String();entries:=append([]lexeme(nil),v.lexemes...)
 for i:=range entries{entries[i].positions=append([]position(nil),entries[i].positions...)}
 selectors:=[]string{"cat","rat","cat","missing"};at:=func(i int)(string,bool){return selectors[i],true}
 for _,tc:=range []struct{name,want string;run func()(*Vector,error)}{
  {"weight","'bare' 'cat':3A,7A 'fat':2A,4A 'rat':5A",func()(*Vector,error){return v.SetWeight(ctx,3)}},
  {"selected","'bare' 'cat':3,7 'fat':2,4 'rat':5",func()(*Vector,error){return v.SetSelectedWeight(ctx,0,len(selectors),at)}},
  {"strip","'bare' 'cat' 'fat' 'rat'",func()(*Vector,error){return v.Strip(ctx)}},
  {"delete","'bare' 'fat':2,4",func()(*Vector,error){return v.Delete(ctx,len(selectors),at)}},
  {"filter","'cat':3B 'rat':5A",func()(*Vector,error){return v.Filter(ctx,12)}},
 }{out,err:=tc.run();if err!=nil||out.String()!=tc.want{t.Fatal(tc.name,out,err)};if v.String()!=before||!reflect.DeepEqual(entries,v.lexemes){t.Fatal("mutated source")}}
 if v.LexemeCount()!=4||v.LexemeAt(0)!="bare"{t.Fatal("lexeme inspection")}
 unchanged,err:=v.SetSelectedWeight(ctx,3,1000,func(int)(string,bool){return "missing",true});if err!=nil||unchanged!=v{t.Fatal("no-op ownership",err)}
 for _,b:=range []byte{'A','a','B','b','C','c','D','d'}{if _,err:=ParseWeight(b);err!=nil{t.Fatal(err)}}
}
func TestFTSVectorFunctionsCancellation(t *testing.T){
 entries:=make([]lexeme,2048);for i:=range entries{entries[i]=lexeme{word:fmt.Sprintf("w%04d",i),positions:[]position{{number:1,weight:2}}}}
 v,err:=renderVector(context.Background(),entries);if err!=nil{t.Fatal(err)}
 for _,operation:=range []string{"weight","selected","strip","delete","filter"}{
  depth:=514;if operation=="selected"||operation=="delete"{depth=4}
  c:=&queryFunctionsCancelContext{Context:context.Background(),remaining:depth};var out *Vector
  switch operation{
  case "weight":out,err=v.SetWeight(c,3)
  case "selected":out,err=v.SetSelectedWeight(c,3,4096,func(int)(string,bool){return "missing",true})
  case "strip":out,err=v.Strip(c)
  case "delete":out,err=v.Delete(c,4096,func(int)(string,bool){return "missing",true})
  case "filter":out,err=v.Filter(c,0)
  }
  if out!=nil||!errors.Is(err,context.Canceled)||c.observed!=depth{t.Fatal(operation,out,err,c.observed)}
  if v.lexemes[0].positions[0].weight!=2||v.LexemeCount()!=2048{t.Fatal("canceled mutation")}
 }
}
