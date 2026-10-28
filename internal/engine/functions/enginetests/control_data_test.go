package enginetests_test

import (
 "context"
 "errors"
 "reflect"
 "testing"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/sqltype"
 "stuntgres/internal/sqlstate"
)

var controlUnitNames = []string{"pg_control_checkpoint", "pg_control_system", "pg_control_init", "pg_control_recovery"}
var controlUnitLabels = [][]string{{"checkpoint_lsn","redo_lsn","redo_wal_file","timeline_id","prev_timeline_id","full_page_writes","next_xid","next_oid","next_multixact_id","next_multi_offset","oldest_xid","oldest_xid_dbid","oldest_active_xid","oldest_multi_xid","oldest_multi_dbid","oldest_commit_ts_xid","newest_commit_ts_xid","checkpoint_time"}, {"pg_control_version","catalog_version_no","system_identifier","pg_control_last_modified"}, {"max_data_alignment","database_block_size","blocks_per_segment","wal_block_size","bytes_per_wal_segment","max_identifier_length","max_index_columns","max_toast_chunk_size","large_object_chunk_size","float8_pass_by_value","data_page_checksum_version","default_char_signedness"}, {"min_recovery_end_lsn","min_recovery_end_timeline","backup_start_lsn","backup_end_lsn","end_of_backup_record_required"}}
var controlUnitTypes = [][]ids.OID{{3220,3220,25,23,23,16,25,26,28,28,28,26,28,28,26,28,28,1184},{23,23,20,1184},{23,23,23,23,23,23,23,23,23,16,23,16},{3220,23,3220,3220,16}}

func TestControlDataDeclarations(t *testing.T) {
 e, op, scope := strictNullUnitFixture(t)
 oids := []ids.OID{3442,3441,3444,3443}
 for i, name := range controlUnitNames {
  node, _, _ := strictNullUnitBind(t,e,op,scope,name+"()")
  n := node.(functions.ControlData)
  d, err := exec.ExpressionRoutineDescriptor(n)
  if err != nil || d == nil || d.Address() != (ids.ObjectAddress{ClassID:1255,ObjectID:oids[i]}) || d.Result != 2249 || d.ReturnsSet() || d.RowEstimate()!=0 || d.Cost!=1 || !d.Strict || d.Volatility!=expr.Volatile || d.Builtin==nil || d.Builtin.Parallel!='s' { t.Fatal("declaration", d,err) }
  out := d.OutputDeclaration()
  if out==nil || out.Inputs()!=0 || out.Len()!=len(controlUnitLabels[i]) { t.Fatal(out) }
  for j,label := range controlUnitLabels[i] {
   m:=n.Usage.Type.CompositeMember(j)
   if m.Name!=label || m.Attribute!=int32(j+1) || m.Usage.Type.ID()!=sqltype.ID(controlUnitTypes[i][j]) || m.Usage!=m.Usage.Type.Usage() || out.Name(j)!=label || out.Type(j)!=controlUnitTypes[i][j] { t.Fatal("member",m) }
  }
  if admitted,set:=n.SourceAdmission(); !admitted || set { t.Fatal("admission") }
  if n.PlanningFold()!=expr.FoldNever || n.PreflightFold()!=expr.PreflightKeep || n.IntrinsicVolatility()!=expr.Volatile || n.ClosedDefault(func(expr.Expr)bool{return true}) || n.Children()!=nil || n.CallCost()!=1 { t.Fatal("planning") }
 }
}
func TestControlDataShapeValidation(t *testing.T) {
 e,op,scope:=strictNullUnitFixture(t)
 for _,name:=range controlUnitNames {
  node,_,_:=strictNullUnitBind(t,e,op,scope,name+"()")
  n:=node.(functions.ControlData)
  for _,fault:=range []string{"nil","scalar","kind","recordid","count","order","name","type","attribute","typmod","usage"} {
   bad:=n
   if fault=="typmod" && name!="pg_control_checkpoint" && name!="pg_control_system" {continue}
   switch fault {
   case "nil": bad.Usage.Type=nil
   case "scalar": bad.Usage.Type,_=e.CatalogRead().LookupType(sqltype.Text)
   case "kind": bad.Kind=255
   case "usage": bad.Usage.Typmod=7
   default:
    members:=make([]sqltype.CompositeMember,n.Usage.Type.CompositeLen())
    for i:=range members { members[i]=n.Usage.Type.CompositeMember(i) }
    switch fault {
    case "count": members=members[:len(members)-1]
    case "order": members[0],members[1]=members[1],members[0]
    case "name": members[0].Name="wrong"
    case "type": d,_:=e.CatalogRead().LookupType(sqltype.Text); members[0].Usage=d.Usage()
    case "attribute": members[0].Attribute=99
    case "typmod": members[len(members)-1].Usage.Typmod=0
    }
    shape,err:=sqltype.RecordShape(members)
    if fault=="recordid" {shape,err=sqltype.Composite(90001,"wrong","wrong",0,members)}
    if err!=nil { t.Fatal(err) }; bad.Usage=shape.Usage()
   }
   if bad.Validate()==nil || bad.ValidateStoredProvider()==nil { t.Fatal("accepted",fault) }
   if _,err:=bad.Render();err==nil { t.Fatal("rendered",fault) }
   if _,_,err:=bad.Capture(expr.CaptureFunction);err==nil { t.Fatal("captured",fault) }
  }
  if _,err:=n.Rebuild(n.Info,[]expr.Expr{n});err==nil {t.Fatal("children accepted")}
 }
}
func TestControlDataEvaluation(t *testing.T) {
 e,op,scope:=strictNullUnitFixture(t)
 for _,name:=range controlUnitNames {
  node,_,evaluation:=strictNullUnitBind(t,e,op,scope,name+"()")
  n:=node.(functions.ControlData); before:=n
  for range 2 {
   value,err:=n.Eval(&evaluation)
   if value.Type()!=nil || err==nil {t.Fatal(value,err)}
   state,ok:=err.(interface{SQLState()string}); if !ok || state.SQLState()!="0A000" || err.Error()!="stuntgres: "+name+" physical control-file access is unsupported in embedded mode" {t.Fatal(err)}
   var diagnostic *sqlstate.Error
   want:=&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"0A000",Message:name+" physical control-file access is unsupported in embedded mode"}
   if !errors.As(err,&diagnostic) || !reflect.DeepEqual(diagnostic,want) {t.Fatal("complete diagnostic",err)}
   source,err:=n.OpenSource(&evaluation)
   if err==nil || source.Type!=nil || source.Values!=nil || source.State!=nil || source.Advance!=nil {t.Fatal(source,err)}
  }
  if !reflect.DeepEqual(before,n) {t.Fatal("mutated")}
  malformed:=n;malformed.Kind=255
  if _,err:=malformed.Eval(&evaluation);err==nil {t.Fatal("malformed evaluation succeeded")}
  if _,err:=malformed.OpenSource(&evaluation);err==nil {t.Fatal("malformed source succeeded")}
  canceled,cancel:=context.WithCancel(context.Background());cancel();evaluation.Context=canceled
  if _,err:=n.Eval(&evaluation);err!=context.Canceled {t.Fatal(err)}
  if _,err:=n.OpenSource(&evaluation);err!=context.Canceled {t.Fatal(err)}
  evaluation.Context=nil
  if _,err:=n.Eval(&evaluation);err==nil {t.Fatal("invalid context")}
 }
}
func TestControlDataProtocols(t *testing.T) {
 e,op,scope:=strictNullUnitFixture(t)
 for _,name:=range controlUnitNames {
  node,_,_:=strictNullUnitBind(t,e,op,scope,name+"()")
  n:=node.(functions.ControlData); before:=n; info:=n.Info;info.Occurrence++
  rebuilt,err:=n.Rebuild(info,nil);if err!=nil {t.Fatal(err)}
  next:=rebuilt.(functions.ControlData)
  if !reflect.DeepEqual(next.Info,info) || next.RoutineReference!=n.RoutineReference || next.Usage.Type!=n.Usage.Type || !reflect.DeepEqual(n,before) {t.Fatal("ownership")}
  if !n.SameGroup(next,nil,false) || !n.SameSourceOccurrence(next) {t.Fatal("same recipe identity")}
  independent,_,_:=strictNullUnitBind(t,e,op,scope,name+"()")
  if !n.SameGroup(independent,nil,false) {t.Fatal("independent equivalent group")}
  different:=next;different.Kind=(n.Kind+1)%4
  if n.SameGroup(different,nil,false) || n.SameSourceOccurrence(different) {t.Fatal("distinct inquiry identity")}
  rendering,err:=n.Render();if err!=nil || rendering.Form!=expr.FormCall || rendering.Name!=name || len(rendering.Args)!=0 || len(rendering.Inputs)!=0 {t.Fatal(rendering,err)}
  capture,ok,err:=n.Capture(expr.CaptureFunction);if err!=nil || !ok || capture.Count!=0 {t.Fatal(capture,err)}
  if _,ok,err:=n.Capture(expr.CaptureKind(255));err!=nil || ok {t.Fatal(ok,err)}
  bad:=info;bad.Usage.Type=nil
  if _,err:=n.Substitute(bad,nil);err==nil || !reflect.DeepEqual(before,n) {t.Fatal("rejected publication")}
  identity:=func(d *sqltype.Descriptor)*sqltype.Descriptor{return d}
  retagged,changed,err:=stored.RetagTypes(e.CatalogRead(),n,identity)
  if err!=nil || changed || !reflect.DeepEqual(retagged,n) {t.Fatal("identity retag",changed,err)}
  remapped,err:=stored.RemapExpression(n,func(ref ids.ObjectRef)ids.ObjectRef{return ref},nil)
  if err!=nil || !reflect.DeepEqual(remapped,n) {t.Fatal("identity remap",err)}
 }
}
