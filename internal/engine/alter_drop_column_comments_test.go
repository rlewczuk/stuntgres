package engine

import (
 "context"
 "reflect"
 "testing"
)

func TestAlterDropColumnCommentsStableAttributes(t *testing.T){
 e:=New();for _,q:=range []string{"CREATE TABLE t(a INT,b TEXT,c BIGINT)","COMMENT ON TABLE t IS 'table keep'","COMMENT ON COLUMN t.b IS 'removed comment'","COMMENT ON COLUMN t.c IS 'survivor comment'"}{run(t,e,q)}
 ref:=roleTableRef(t,e,"t");oldView:=e.CatalogRead();old:=e.state.descriptors[ref.Address].(*relationDescriptor)
 run(t,e,"ALTER TABLE t DROP b");next:=e.state.descriptors[ref.Address].(*relationDescriptor)
 if next.comment!="table keep"||!reflect.DeepEqual(next.attributes,[]int32{1,3})||!reflect.DeepEqual(next.columnComments,map[int32]string{3:"survivor comment"})||!reflect.DeepEqual(old.columnComments,map[int32]string{2:"removed comment",3:"survivor comment"}){t.Fatal(old,next)}
 for _,sub:=range []int32{2,3}{column:=ref;column.Address.SubID=sub;text,present,err:=oldView.Comment(column);want:=map[int32]string{2:"removed comment",3:"survivor comment"}[sub];if err!=nil||!present||text!=want{t.Fatal(text,present,err)}}
 live:=ref;live.Address.SubID=3;if text,present,err:=e.CatalogRead().Comment(live);err!=nil||!present||text!="survivor comment"{t.Fatal(text,present,err)}
 dead:=ref;dead.Address.SubID=2;if _,_,err:=e.CatalogRead().Comment(dead);err==nil{t.Fatal("dead attribute readable")}
 invalid:=*next;invalid.revision++;invalid.columnComments=map[int32]string{2:"invalid dropped payload",3:"survivor comment"};stage:=e.BeginCatalogStage(e.CatalogRead());defer e.DiscardCatalogStage(stage);if err:=stage.Replace(&invalid);err!=nil{t.Fatal(err)};if _,err:=e.ValidateCatalogStage(context.Background(),stage);err==nil||err.Error()!="stuntgres: invalid relation column comment"{t.Fatal(err)};if e.state.descriptors[ref.Address]!=next{t.Fatal("invalid stage changed metadata")}
 run(t,e,"DROP TABLE t");run(t,e,"CREATE TABLE t(a INT,b TEXT,c BIGINT)");replacement:=e.state.descriptors[roleTableRef(t,e,"t").Address].(*relationDescriptor);if replacement.comment!=""||len(replacement.columnComments)!=0{t.Fatal("name recreation inherited comments",replacement)}
}
