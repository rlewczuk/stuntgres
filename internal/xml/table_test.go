package xml

import (
 "context"
 "errors"
 "reflect"
 "testing"
)

func TestXMLTableProvider(t *testing.T) {
 input:=accept(t,`<r xmlns="urn:r" xmlns:a="urn:a"><v a:id="7">A<!--c--><b>B</b>C</v><v a:id="8"/></r>`,Content)
 table,_,err:=StartTable(context.Background(),input);if err!=nil {t.Fatal(err)}
 if err:=table.SetNamespace("p","urn:r");err!=nil {t.Fatal(err)}
 if err:=table.SetNamespace("q","urn:a");err!=nil {t.Fatal(err)}
 if err:=table.SetRowFilter(`/p:r/p:*`);err!=nil {t.Fatal(err)}
 for _,path:=range []string{`@q:*`,`.`,`/p:r/p:v[1]/@q:id`,`missing`,`text()`,`true()`,`namespace::a`} {if err:=table.SetColumnFilter(path);err!=nil {t.Fatal(err)}}
 for row,want:=range []string{"ABC",""} {
  present,err:=table.Next();if err!=nil || !present {t.Fatal(row,present,err)}
  id,err:=table.Column(0,false);if err!=nil || id.Text!=[]string{"7","8"}[row] {t.Fatal(id,err)}
  current,err:=table.Column(1,false);if err!=nil || current.Text!=want || current.Missing {t.Fatal(current,err)}
  absolute,err:=table.Column(2,false);if err!=nil || absolute.Text!="7" {t.Fatal(absolute,err)}
  missing,err:=table.Column(3,false);if err!=nil || !missing.Missing {t.Fatal(missing,err)}
  boolean,err:=table.Column(5,false);if err!=nil || !boolean.Boolean || boolean.Text!="true" {t.Fatal(boolean,err)}
  namespace,err:=table.Column(6,true);if err!=nil || namespace.Text!="urn:a" {t.Fatal(namespace,err)}
  if row==0 {
   _,err:=table.Column(4,false);var cardinality *TableCardinalityError;if !errors.As(err,&cardinality) {t.Fatal(err)}
   text,err:=table.Column(4,true);if err!=nil || text.Text!="AC" {t.Fatal(text,err)}
  }
 }
 if more,err:=table.Next();err!=nil || more {t.Fatal(more,err)}
 for _,path:=range []string{`false()`,`true()`,`1`,`'x'`,`/missing`} {
  other,_,err:=StartTable(context.Background(),input);if err!=nil {t.Fatal(err)}
  if err:=other.SetRowFilter(path);err!=nil {t.Fatal(err)}
  if more,err:=other.Next();err!=nil || more {t.Fatal(path,more,err)}
 }
 t.Run("serialization and operation isolation",func(t *testing.T){
  original:=accept(t,`<r xmlns:p="urn:p"><p:v p:a="7"><p:w/></p:v></r>`,Content)
  first,_,err:=StartTable(context.Background(),original);if err!=nil {t.Fatal(err)}
  second,_,err:=StartTable(context.Background(),original);if err!=nil {t.Fatal(err)}
  if first.frame==second.frame || first.frame.tree==second.frame.tree || first.frame.tree.root==second.frame.tree.root {t.Fatal("shared invocation graph")}
  first.SetNamespace("p","urn:p");second.SetNamespace("p","urn:other")
  if first.frame.bindings["p"]==second.frame.bindings["p"] {t.Fatal("shared namespace resolver")}
  for _,table:=range []*Table{first,second} {
   if err:=table.SetRowFilter(`/r`);err!=nil {t.Fatal(err)}
   if err:=table.SetColumnFilter(`*`);err!=nil {t.Fatal(err)}
   if ok,err:=table.Next();err!=nil || !ok {t.Fatal(ok,err)}
  }
  snapshot:=*first.columns[0]
  result,err:=first.Column(0,true);if err!=nil || result.Text!=`<p:v xmlns:p="urn:p" p:a="7"><p:w/></p:v>` {t.Fatal(result,err)}
  if !reflect.DeepEqual(*first.columns[0],snapshot) || original.Original()!=`<r xmlns:p="urn:p"><p:v p:a="7"><p:w/></p:v></r>` {t.Fatal("serialization mutated accepted syntax/value")}
  doc,_,err:=StartTable(context.Background(),original);if err!=nil {t.Fatal(err)}
  doc.SetRowFilter(`/`);doc.SetColumnFilter(`.`);doc.Next()
  value,err:=doc.Column(0,true);if err!=nil || value.Text!=original.Original()+"\n" {t.Fatal(value,err)}
 })
 t.Run("CDATA projection and serialization",func(t *testing.T){
  for _,tc:=range []struct{name,doc,xml,text,count string}{
   {"ordinary text",`<r>A&amp;B</r>`,`A&amp;B`,`A&B`,"1"},
   {"single CDATA",`<r><![CDATA[A&B]]></r>`,`<![CDATA[A&B]]>`,`A&B`,"1"},
   {"adjacent CDATA",`<r><![CDATA[A&]]><![CDATA[B<C]]></r>`,`<![CDATA[A&B<C]]>`,`A&B<C`,"1"},
   {"adjacent delimiter",`<r><![CDATA[A]]]]><![CDATA[>B]]></r>`,`<![CDATA[A]]]]><![CDATA[>B]]>`,`A]]>B`,"1"},
   {"mixed text and CDATA",`<r>T&amp;<![CDATA[A<B]]>U&amp;</r>`,`T&amp;<![CDATA[A<B]]>U&amp;`,`T&A<BU&`,"3"},
  } {t.Run(tc.name,func(t *testing.T){
   original:=accept(t,tc.doc,Content)
   var tables [2]*Table
   for i:=range tables {
    table,_,err:=StartTable(context.Background(),original);if err!=nil {t.Fatal(err)}
    if err:=table.SetRowFilter(`/r`);err!=nil {t.Fatal(err)}
    for _,path:=range []string{`text()`,`string(.)`,`count(text())`} {if err:=table.SetColumnFilter(path);err!=nil {t.Fatal(err)}}
    if ok,err:=table.Next();err!=nil || !ok {t.Fatal(ok,err)}
    tables[i]=table
   }
   sources:=tables[0].frame.tree.document.nodes[0].children
   siblingSources:=tables[1].frame.tree.document.nodes[0].children
   if sources[0]==siblingSources[0] {t.Fatal("shared invocation source")}
   before:=make([]node,len(sources))
   for i,source:=range sources {before[i]=*source}
   first:=tables[0].frame.node.children[0]
   if first.source!=sources[0] {t.Fatal("projection lost original first source backing")}
   if tc.count=="1" && first.value!=tc.text {t.Fatal("projection lost coalesced XPath value",first.value)}
   for _,table:=range tables {
    for repeat:=0;repeat<2;repeat++ {
     value,err:=table.Column(0,true);if err!=nil || value.Text!=tc.xml || value.Missing {t.Fatal(value,err)}
     scalar,err:=table.Column(0,false)
     if tc.count=="1" {
      if err!=nil || scalar.Text!=tc.text || scalar.Missing {t.Fatal(scalar,err)}
     } else {var cardinality *TableCardinalityError;if !errors.As(err,&cardinality) {t.Fatal("mixed source lost scalar cardinality",err)}}
     text,err:=table.Column(1,false);if err!=nil || text.Text!=tc.text {t.Fatal(text,err)}
     count,err:=table.Column(2,false);if err!=nil || count.Text!=tc.count {t.Fatal(count,err)}
    }
   }
   for i,source:=range sources {
    if !reflect.DeepEqual(*source,before[i]) || !reflect.DeepEqual(*siblingSources[i],before[i]) {t.Fatal("serialization mutated source nodes")}
   }
   if original.Original()!=tc.doc {t.Fatal("serialization mutated retained XML")}
  })}
 })
 t.Run("initialization stages",func(t *testing.T){
  if _,_,err:=StartTable(context.Background(),accept(t,`<a/><b/>`,Content));err==nil {t.Fatal("fragment context admitted")}
  for _,tc:=range []struct{path,stage string}{{"","row-empty"},{"[","compile"}} {
   table,_,err:=StartTable(context.Background(),input);if err!=nil {t.Fatal(err)}
   var xpath *XPathError
   if err:=table.SetRowFilter(tc.path);!errors.As(err,&xpath) || xpath.Stage!=tc.stage {t.Fatal(err)}
  }
  table,_,err:=StartTable(context.Background(),input);if err!=nil {t.Fatal(err)}
  if err:=table.SetRowFilter(`/missing`);err!=nil {t.Fatal(err)}
  if err:=table.SetColumnFilter(`[`);err==nil {t.Fatal("invalid inactive column not compiled")}
 })
 t.Run("cancellation",func(t *testing.T){
  ctx,cancel:=context.WithCancel(context.Background());cancel()
  if _,_,err:=StartTable(ctx,input);!errors.Is(err,context.Canceled) {t.Fatal(err)}
  for _,operation:=range []string{"namespace","row","column","next","value"} {
   ctx,cancel:=context.WithCancel(context.Background())
   table,_,err:=StartTable(ctx,input);if err!=nil {cancel();t.Fatal(err)}
   table.SetRowFilter(`/`);table.SetColumnFilter(`.`);table.Next();cancel()
   switch operation {
   case "namespace":err=table.SetNamespace("p","urn:r")
   case "row":err=table.SetRowFilter(`/r`)
   case "column":err=table.SetColumnFilter(`.`)
   case "next":_,err=table.Next()
   case "value":_,err=table.Column(0,true)
   }
   if !errors.Is(err,context.Canceled) {t.Fatal(operation,err)}
  }
 })
}
