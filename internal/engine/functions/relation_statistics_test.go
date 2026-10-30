package functions

import (
 "testing"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/sqltype"
)

type relationStatisticsOperand struct {expr.Info}
func(relationStatisticsOperand) Eval(expr.Context)(sqltype.Value,error){return sqltype.Value{},nil}

func TestRelationStatisticsRecipeOwnership(t *testing.T){
 types:=make(map[sqltype.ID]*sqltype.Descriptor);for _,d:=range sqltype.Builtins(){types[d.ID()]=d}
 first:=relationStatisticsOperand{expr.Info{Usage:types[sqltype.Text].Usage()}}
 second:=relationStatisticsOperand{expr.Info{Usage:types[sqltype.Text].Usage(),Occurrence:1}}
 n:=RelationStatistics{Info:expr.Info{Usage:types[sqltype.Void].Usage()},Clear:true,Args:[]expr.Expr{first,second},Slots:[2]uint8{1,0},Labels:[2]string{"relname","schemaname"}}
 if err:=n.Validate();err!=nil{t.Fatal(err)}
 if n.PlanningFold()!=expr.FoldNever||n.IntrinsicVolatility()!=expr.Volatile||n.StrictNull()!=expr.StrictNone{t.Fatal("statistics mutation became foldable/strict")}
 children:=[]expr.Expr{second,first};rebuilt,err:=n.Rebuild(n.Info,children);if err!=nil{t.Fatal(err)};children[0]=first
 r:=rebuilt.(RelationStatistics)
 if r.Args[0]!=second||n.Args[0]!=first||r.Slots!=n.Slots||r.Labels!=n.Labels{t.Fatal("rebuild lost immutable operands/formal mapping")}
 capture,ok,err:=r.Capture(expr.CaptureFunction);if err!=nil||!ok||capture.Count!=2||!capture.Indexed{t.Fatal(capture,ok,err)}
 if operand,err:=r.CaptureOperand(0);err!=nil||operand!=second{t.Fatal(operand,err)}
 rendering,err:=r.Render();if err!=nil||len(rendering.Args)!=2||rendering.Args[0].Label!="relname"||rendering.Args[1].Label!="schemaname"{t.Fatal(rendering,err)}
 bad:=n;bad.Slots=[2]uint8{0,0};if bad.Validate()==nil{t.Fatal("duplicate formal slots admitted")}
 restore:=RelationStatistics{Info:expr.Info{Usage:types[sqltype.Bool].Usage()},Args:[]expr.Expr{first}}
 if err:=restore.Validate();err!=nil{t.Fatal(err)}
 restore.Labels[0]="kwargs";if restore.Validate()==nil{t.Fatal("named expanded restore admitted")}
}
