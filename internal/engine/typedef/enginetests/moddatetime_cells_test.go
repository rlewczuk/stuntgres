package enginetests

import (
 "context"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/typedef"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func moddatetimeExactTimestampReplayCells(t *testing.T) {
 e:=engine.New();v:=e.CatalogRead();d,_:=v.LookupType(sqltype.Timestamp);u:=d.Usage();u.Typmod=0
 text:="2001-02-03 04:05:06.123456"
 relation:=catalog.Relation{Index:map[string]int{"stamp":0},Columns:[]catalog.Column{{Name:"stamp",Usage:u}},Attributes:[]int32{1}}
 env:=typedef.DomainReplayEnv{Context:context.Background(),Catalog:v,Scalar:func(raw parser.Expr,u sqltype.Usage)(sqltype.Value,error){lit,ok:=raw.(parser.LiteralExpr);if !ok||lit.Value.Kind!=parser.StringValue {t.Fatal("nonliteral reconstruction")};return u.Type.Input(context.Background(),lit.Value.Text,u.Typmod)}}
 cell:=typedef.DomainReplayCell{Column:"stamp",Value:&typedef.DomainReplayNode{Kind:"timestamp_exact",Scalar:&text}}
 for _,tc:=range []struct{name,sql string;slots []int;cells []typedef.DomainReplayCell;valid bool}{
  {"valid","INSERT INTO public.mdt(stamp) VALUES('2001-02-03 04:05:06.123456')",[]int{0},[]typedef.DomainReplayCell{cell},true},
  {"null","INSERT INTO public.mdt(stamp) VALUES(NULL)",[]int{0},[]typedef.DomainReplayCell{cell},false},
  {"cast","INSERT INTO public.mdt(stamp) VALUES('2001-02-03 04:05:06.123456'::timestamp)",[]int{0},[]typedef.DomainReplayCell{cell},false},
  {"mismatch","INSERT INTO public.mdt(stamp) VALUES('2001-02-03 04:05:06.123457')",[]int{0},[]typedef.DomainReplayCell{cell},false},
  {"duplicate_cell","INSERT INTO public.mdt(stamp) VALUES('2001-02-03 04:05:06.123456')",[]int{0},[]typedef.DomainReplayCell{cell,cell},false},
  {"duplicate_target","INSERT INTO public.mdt(stamp,stamp) VALUES('2001-02-03 04:05:06.123456','2001-02-03 04:05:06.123456')",[]int{0,0},[]typedef.DomainReplayCell{cell},false},
 } {t.Run(tc.name,func(t *testing.T){statements,err:=parser.Parse(tc.sql);if err!=nil {t.Fatal(err)};cells,err:=typedef.DomainReplayCells(&env,statements[0].(*parser.Insert),tc.cells,&relation,tc.slots);if tc.valid {if err!=nil||len(cells)!=1 {t.Fatal(cells,err)}} else if err==nil {t.Fatal("invalid temporal cell admitted",tc.name)}})}
 statements,err:=parser.Parse("INSERT INTO public.mdt(stamp) VALUES('2001-02-03 04:05:06.123456')");if err!=nil {t.Fatal(err)}
 relation.ColumnMeta.Generations=map[int32]catalog.ColumnGeneration{1:{}}
 if _,err:=typedef.DomainReplayCells(&env,statements[0].(*parser.Insert),[]typedef.DomainReplayCell{cell},&relation,[]int{0});err==nil {t.Fatal("generated destination admitted")}
 relation.ColumnMeta.Generations=nil
 domain,err:=sqltype.Domain(9500900,"md_domain","public.md_domain",d.Usage(),true);if err!=nil {t.Fatal(err)}
 if _,_,err:=typedef.RestoreDomainReplayNode(&env,domain.Usage(),cell.Value);err==nil {t.Fatal("domain acquired temporal bypass")}
 // Node shape alone cannot authorize a different destination type.
 integer,_:=v.LookupType(sqltype.Int4);u.Type=integer
 if _,_,err:=typedef.RestoreDomainReplayNode(&env,u,cell.Value);err==nil {t.Fatal("non timestamp destination admitted")}
}
