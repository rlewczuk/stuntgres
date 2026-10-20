package engine

import (
 "context"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
 "testing"
 "time"
)

func TestTimestampRangeTemporalCapture(t *testing.T) {
 ctx := context.Background(); e := New()
 d,ok := e.CatalogRead().LookupType(sqltype.TimestampRange)
 if !ok || !d.Concrete() { t.Fatal("missing tsrange") }
 first := sqltype.InputEnvironment{TransactionStart:time.Date(2000,2,29,12,34,56,123456000,time.UTC)}
 later := sqltype.InputEnvironment{TransactionStart:time.Date(2001,1,1,12,0,0,0,time.UTC)}
 position := parser.Position{Offset:7,Character:8}
 b := newTestExpressionBinder(t,ctx,e,first,nil)
 v,err := b.InputConstant("(now,tomorrow]",d.Usage(),position); if err != nil { t.Fatal(err) }
 out,err := d.Output(ctx,v)
 if err != nil || out != `("2000-02-29 12:34:56.123456","2000-03-01 00:00:00"]` || len(b.Prepared.Temporals) != 1 || !sqltype.SameDatum(v,b.Prepared.Temporals[0].Value) { t.Fatal("whole capture",out,b.Prepared.Temporals,err) }
 description := &StatementDescription{temporals:append([]temporalConstant(nil),b.Prepared.Temporals...)}
 retained := newTestExpressionBinder(t,ctx,e,later,description)
 frozen,err := retained.InputConstant("must not reparse",d.Usage(),position)
 if err != nil || !sqltype.SameDatum(v,frozen) { t.Fatal("frozen compound moved",err) }
 fresh,err := d.InputWithEnvironment(ctx,"(now,tomorrow]",-1,later); if err != nil { t.Fatal(err) }
 out,err = d.Output(ctx,fresh)
 if err != nil || out != `("2001-01-01 12:00:00","2001-01-02 00:00:00"]` || sqltype.SameDatum(v,fresh) { t.Fatal("fresh compound",out,err) }
 if !analyze.RefreshableTemporal(b.Prepared.Temporals[0]) { t.Fatal("builtin not refreshable") }
 description.refreshTemporals = true
 refreshed := newTestExpressionBinder(t,ctx,e,later,description)
 candidate,err := refreshed.InputConstant("(now,tomorrow]",d.Usage(),position)
 if err != nil || !sqltype.SameDatum(fresh,candidate) || len(refreshed.Prepared.Temporals) != 1 { t.Fatal("refresh",err) }
 if !sqltype.SameDatum(description.temporals[0].Value,v) { t.Fatal("retained capture mutated") }
 domain,err := sqltype.Domain(20000,"span","span",d.Usage()); if err != nil { t.Fatal(err) }
 logical,err := domain.Wrap(v); if err != nil { t.Fatal(err) }
 if analyze.RefreshableTemporal(temporalConstant{TypeID:domain.ID(),Typmod:-1,Value:logical}) { t.Fatal("domain ownership fence lost") }
 for _, tc := range []struct{order sqltype.DateOrder; want string}{
  {sqltype.DateMDY,`("2003-01-02 00:00:00","2003-01-03 00:00:00"]`},
  {sqltype.DateDMY,`("2003-02-01 00:00:00","2003-03-01 00:00:00"]`},
 } {
  input := sqltype.InputEnvironment{DateStyle:sqltype.DateStyle{Order:tc.order}}
  binder := newTestExpressionBinder(t,ctx,e,input,nil)
  value,err := binder.InputConstant("(01/02/2003,01/03/2003]",d.Usage(),position)
  if err != nil { t.Fatal(err) }
  if text,err := d.Output(ctx,value); err != nil || text != tc.want { t.Fatal(text,err) }
 }
}
