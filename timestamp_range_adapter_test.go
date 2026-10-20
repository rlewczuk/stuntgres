package stuntgres

import (
 "context"
 "database/sql/driver"
 "errors"
 "reflect"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/sqltype"
 "testing"
)

func TestTimestampRangeAdapterEnvironmentDiagnostics(t *testing.T) {
 ctx := context.Background(); e := engine.New()
 d,_ := e.CatalogRead().LookupType(sqltype.TimestampRange)
 v,err := importParameter(ctx,"[01/02/2003,01/03/2003)",d.Usage(),sqltype.InputEnvironment{DateStyle:sqltype.DateStyle{Order:sqltype.DateDMY}})
 if err != nil { t.Fatal(err) }
 out,err := exportValue(ctx,v,sqltype.OutputEnvironment{DateStyle:sqltype.DateStyle{Style:sqltype.DateGerman,Order:sqltype.DateDMY}})
 if err != nil || out != `["01.02.2003 00:00:00","01.03.2003 00:00:00")` { t.Fatal(out,err) }
 if scanType(d) != reflect.TypeFor[string]() { t.Fatal("TSRANGE ScanType",scanType(d)) }
 for _, input := range []any{nil,[]byte(nil)} {
  value,err := importParameter(ctx,input,d.Usage(),sqltype.InputEnvironment{})
  if err != nil || !value.IsNull() { t.Fatal("NULL import",value,err) }
  raw,err := exportValue(ctx,value,sqltype.OutputEnvironment{}); if err != nil || raw != nil { t.Fatal("NULL export",raw,err) }
 }
 for _, tc := range []struct{ input,code,message,detail,portal string }{
  {"[bad,epoch)","22007",`invalid input syntax for type timestamp: "bad"`,"","..."},
  {"[294277-01-01,infinity)","22008",`timestamp out of range: "294277-01-01"`,"","..."},
  {"[infinity,-infinity]","22000","range lower bound must be less than or equal to range upper bound","","..."},
  {"","22P02",`malformed range literal: ""`,"Missing left parenthesis or bracket.",""},
 } {
  importer := frameImporter([]driver.Value{tc.input})
  err := importer(ctx,analyze.ParameterInfo{Types:[]sqltype.Usage{d.Usage()}},make([]sqltype.Value,1),sqltype.InputEnvironment{})
  want := Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:tc.code,Message:tc.message,Detail:tc.detail,Context:`unnamed portal parameter $1 = '`+tc.portal+`'`}
  var got *Error
  if !errors.As(err,&got) || *got != want { t.Fatalf("got=%#v want=%#v err=%v",got,want,err) }
 }
}
