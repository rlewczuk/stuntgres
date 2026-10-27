package xml

import (
 "context"
 "errors"
 "slices"
 "strings"
 "testing"
)

func TestXMLCoreParserWarningCancellation(t *testing.T) {
 const retainedInput=`<?xmlfoo?><a xml:space="bogus"/>`
 retained:=accept(t,retainedInput,Document)
 warnings:=retained.Diagnostics()
 if len(warnings)!=2 {t.Fatal("missing retained warnings")}
 for _,tc:=range []struct{phase,input string;mode Mode}{
  {"attribute",`<a xml:space="`+strings.Repeat("x",40000)+`"/>`,Content},
  {"namespaceCopy",`<a xml:space="`+strings.Repeat("x",40000)+`&#32;"/>`,Content},
  {"joinMessage",`<a xml:space="`+strings.Repeat("x",40000)+`"/>`,Content},
  {"pi",`<?xmlfoo `+strings.Repeat("x",40000)+`?><a/>`,Document},
  {"catalogPI",`<?oasis-xml-catalog catalog="`+strings.Repeat("x",40000)+`"?><a/>`,Document},
  {"catalogPI","<?oasis-xml-catalog broken"+strings.Repeat("\n",40000)+"?><a/>",Document},
  {"namespaceCopy",`<?oasis-xml-catalog catalog="`+strings.Repeat("x",40000)+"\r\n\"?><a/>",Document},
 } {
  for _,at:=range []int{2,4} {
   c:=&phaseContext{Context:context.Background(),phase:tc.phase,after:at,cause:context.Canceled}
   result,err:=Parse(c,tc.input,Options{Mode:tc.mode})
   if !c.admitted||!errors.Is(err,context.Canceled)||result.valid||result.original!=""||result.requested!=0||result.effective!=0||result.declaration!=(Declaration{})||result.diagnostics!=nil {t.Fatalf("%s/%d admitted%t result%+v error%v",tc.phase,at,c.admitted,result,err)}
   if _,ok:=err.(interface{SQLState() string});ok {t.Fatal("cancellation acquired SQLSTATE")}
   healthy:=accept(t,"<healthy/>",tc.mode)
   if len(healthy.Diagnostics())!=0 {t.Fatal("warning state survived cancellation")}
   if retained.Original()!=retainedInput||!slices.Equal(retained.Diagnostics(),warnings) {t.Fatal("cancellation mutated retained warnings")}
   if out,w,err:=OutputText(context.Background(),retained);err!=nil||out!=retainedInput||w!=(OutputWarning{}) {t.Fatalf("retained output%q error%v",out,err)}
  }
 }
}
