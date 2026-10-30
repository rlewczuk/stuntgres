package sqltype

import (
 "context"
 "errors"
 "stuntgres/internal/sqlstate"
 "testing"
)

func TestXMLTableTypeInput(t *testing.T) {
 ctx:=context.Background();types:=testTypes()
 required,err:=Domain(99051,"xt_required","xt_required",types[Int4].Usage(),true);if err!=nil {t.Fatal(err)}
 xmlDomain,err:=Domain(99052,"xt_xml","xt_xml",types[XML].Usage(),false);if err!=nil {t.Fatal(err)}
 start:=func(doc,path string)*XMLTableInvocation {
  t.Helper();value,err:=types[XML].Input(ctx,doc,-1);if err!=nil {t.Fatal(err)}
  table,err:=StartXMLTable(ctx,value,InputEnvironment{});if err!=nil {t.Fatal(err)}
  if err:=table.RowFilter(`/r`,InputEnvironment{});err!=nil {t.Fatal(err)}
  if err:=table.ColumnFilter(path,InputEnvironment{});err!=nil {t.Fatal(err)}
  if ok,err:=table.Next(InputEnvironment{});err!=nil || !ok {t.Fatal(ok,err)}
  return table
 }
 for _,tc:=range []struct{doc,path string;usage Usage;want,code string}{
  {`<r/>`,`true()`,types[Int4].Usage(),"1",""},
  {`<r/>`,`false()`,types[Int4].Usage(),"0",""},
  {`<r/>`,`true()`,types[Text].Usage(),"true",""},
  {`<r><v>{1,2}</v></r>`,`v`,types[1007].Usage(),"{1,2}",""},
  {`<r><v>bad</v></r>`,`v`,types[Int4].Usage(),"","22P02"},
  {`<r><v>abc</v></r>`,`v`,Usage{Type:types[VarChar],Typmod:6},"","22001"},
  {`<r><v>A</v><v>B</v></r>`,`v`,types[XML].Usage(),"<v>A</v><v>B</v>",""},
  {`<r><v>A</v><v>B</v></r>`,`v`,xmlDomain.Usage(),"","21000"},
 } {
  table:=start(tc.doc,tc.path)
  value,missing,err:=table.Column(ctx,0,tc.usage,InputEnvironment{})
  if tc.code!="" {var diagnostic *sqlstate.Error;if !errors.As(err,&diagnostic) || diagnostic.Code!=tc.code {t.Fatal(tc,err)};continue}
  if err!=nil || missing {t.Fatal(tc,missing,err)}
  text,err:=value.Type().Output(ctx,value);if err!=nil || text!=tc.want {t.Fatal(tc,text,err)}
 }
 table:=start(`<r/>`,`missing`)
 value,missing,err:=table.Column(ctx,0,required.Usage(),InputEnvironment{})
 if err!=nil || !missing || !value.IsNull() || value.Type()!=required {t.Fatal("missing node must bypass domain input",value,missing,err)}
 for _,path:=range []string{`v/text()`,`'scalar'`} {
  table:=start(`<r><v>A<b/>B</v></r>`,path)
  _,_,err:=table.Column(ctx,0,types[XML].Usage(),InputEnvironment{XMLMode:XMLDocument})
  var diagnostic *sqlstate.Error
  if !errors.As(err,&diagnostic) || diagnostic.Code!="2200M" {t.Fatal("present XML must use session input mode",path,err)}
 }
}
