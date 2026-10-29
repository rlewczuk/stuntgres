package enginetests

import (
 "context"
 "encoding/base64"
 "strings"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/typedef"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func TestModdatetimeExactTimestampReplay(t *testing.T) {
 t.Run("destination_cells",moddatetimeExactTimestampReplayCells)
 t.Run("immediate_pair",moddatetimeExactTimestampReplayPair)
 ctx:=context.Background();e:=engine.New();view:=e.CatalogRead()
 for _,id:=range []sqltype.ID{sqltype.Timestamp,sqltype.Timestamptz} {
  typ,ok:=view.LookupType(id);if !ok {t.Fatal(id)}
  exact:="2001-02-03 04:05:06.123456";if id==sqltype.Timestamptz {exact+="+00"}
  for _,precision:=range []int32{0,3} {
   u:=typ.Usage();u.Typmod=precision
   env:=typedef.DomainReplayEnv{Context:ctx,Catalog:view,Scalar:func(raw parser.Expr,usage sqltype.Usage)(sqltype.Value,error){
    literal,ok:=raw.(parser.LiteralExpr);if !ok||literal.Value.Kind!=parser.StringValue {t.Fatal("payload was parsed as expression",raw)}
    if usage.Typmod!=-1 {t.Fatal("replay reapplied precision",usage)}
    return usage.Type.Input(ctx,literal.Value.Text,usage.Typmod)
   }}
   node:=&typedef.DomainReplayNode{Kind:"timestamp_exact",Scalar:&exact}
   got,provenance,err:=typedef.RestoreDomainReplayNode(&env,u,node)
   if err!=nil||provenance==0||provenance==typedef.DomainReplayDomainNull||provenance==typedef.DomainReplaySeg {t.Fatal("distinct exact temporal provenance missing",id,precision,err)}
   text,err:=typ.Output(ctx,got);if err!=nil||text!=exact {t.Fatal(text,exact,err)}
   ordinary,err:=typ.Input(ctx,exact,precision);if err!=nil {t.Fatal(err)}
   rounded,err:=typ.Output(ctx,ordinary);want:="2001-02-03 04:05:06";if precision==3 {want+=".123"};if id==sqltype.Timestamptz {want+="+00"};if err!=nil||rounded!=want {t.Fatal("independent ordinary assignment",rounded,want,err)}
   for _,bad:=range []string{"infinity","-infinity","2001-02-03 04:05:06","2001-02-03T04:05:06.123456",exact+"; SELECT 1","now",""} {
    if id==sqltype.Timestamptz && bad=="2001-02-03 04:05:06" {bad+="+00"}
    if _,_,err:=typedef.RestoreDomainReplayNode(&env,u,&typedef.DomainReplayNode{Kind:"timestamp_exact",Scalar:&bad});err==nil {t.Fatal("invalid exact timestamp admitted",bad)}
   }
   for _,badPrecision:=range []int32{-1,6} {bad:=u;bad.Typmod=badPrecision;if _,_,err:=typedef.RestoreDomainReplayNode(&env,bad,node);err==nil {t.Fatal("unlimited precision bypass",badPrecision)}}
   canceled,cancel:=context.WithCancel(ctx);cancel();env.Context=canceled
   if _,_,err:=typedef.RestoreDomainReplayNode(&env,u,node);err==nil {t.Fatal("cancellation ignored")}
  }
 }
 raw:=`{"schema":"public","table":"mdt","cells":[{"column":"stamp","value":{"kind":"timestamp_exact","scalar":"2001-02-03 04:05:06.123456"}}]}`
 decode:=func(s string)error{_,_,err:=typedef.DecodeDomainReplay("stuntgres-domain-null-v1 "+base64.StdEncoding.EncodeToString([]byte(s)));return err}
 if err:=decode(raw);err!=nil {t.Fatal("strict temporal codec",err)}
 for _,bad:=range []string{
  strings.Replace(raw,`"kind":"timestamp_exact"`,`"kind":"timestamp_exact","kind":"timestamp_exact"`,1),
  strings.Replace(raw,`"scalar":"2001-02-03 04:05:06.123456"`,`"scalar":null`,1),
  strings.Replace(raw,`"kind":"timestamp_exact"`,`"kind":"timestamp_exact","extra":1`,1),
  strings.Replace(raw,`"kind":"timestamp_exact"`,`"kind":"timestamp_exact","type":["pg_catalog","timestamp"]`,1),
  strings.Replace(raw,`"kind":"timestamp_exact"`,`"kind":"timestamp_exact","base":{"kind":"null"}`,1),raw+raw,
 } {if err:=decode(bad);err==nil {t.Fatal("malformed wire admitted",bad)}}
}
