package enginetests

import (
 "testing"
 "stuntgres/internal/engine/typedef"
 "stuntgres/internal/parser"
)

func moddatetimeExactTimestampReplayPair(t *testing.T) {
 text:="2001-02-03 04:05:06.123456"
 directive,err:=typedef.EncodeDomainReplay(typedef.DomainReplayRecord{Schema:"public",Table:"mdt",Cells:[]typedef.DomainReplayCell{{Column:"stamp",Value:&typedef.DomainReplayNode{Kind:"timestamp_exact",Scalar:&text}}}});if err!=nil {t.Fatal(err)}
 for _,tc:=range []struct{name,sql string;valid bool}{
  {"valid",directive+"INSERT INTO public.mdt(stamp) VALUES ('"+text+"');",true},
  {"other_table",directive+"INSERT INTO public.other(stamp) VALUES ('"+text+"');",false},
  {"other_schema",directive+"INSERT INTO other.mdt(stamp) VALUES ('"+text+"');",false},
  {"intervening_statement",directive+"SELECT 1;INSERT INTO public.mdt(stamp) VALUES ('"+text+"');",false},
  {"intervening_comment",directive+"-- moved\nINSERT INTO public.mdt(stamp) VALUES ('"+text+"');",false},
  {"duplicate",directive+directive+"INSERT INTO public.mdt(stamp) VALUES ('"+text+"');",false},
  {"multiple_rows",directive+"INSERT INTO public.mdt(stamp) VALUES ('"+text+"'),('"+text+"');",false},
  {"missing_insert",directive,false},
 } {t.Run(tc.name,func(t *testing.T){
  var records []typedef.PositionedDomainReplay
  statements,err:=parser.ParseWithOptions(tc.sql,parser.Options{StandardConformingStrings:true,LineComment:func(comment string,pos parser.Position)error{record,found,err:=typedef.DecodeDomainReplay(comment);if found&&err==nil {records=append(records,typedef.PositionedDomainReplay{Record:record,End:pos.Offset+len(comment)})};return err}});if err!=nil {t.Fatal(err)}
  pairs,err:=typedef.PairDomainReplay(tc.sql,statements,records);if tc.valid {if err!=nil||len(pairs)!=1 {t.Fatal(pairs,err)}} else if err==nil {t.Fatal("unpaired exact temporal provenance accepted")}
 })}
}
