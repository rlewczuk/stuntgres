package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// ReplicationSlotKind identifies a SQL operation, never a provider or slot.
type ReplicationSlotKind uint8
const (
 SlotCreatePhysical ReplicationSlotKind = iota
 SlotCreateLogical
 SlotDrop
 SlotCopyPhysical
 SlotCopyLogical
 SlotAdvance
)
var replicationSlotNames = [...]string{"pg_create_physical_replication_slot", "pg_create_logical_replication_slot", "pg_drop_replication_slot", "pg_copy_physical_replication_slot", "pg_copy_logical_replication_slot", "pg_replication_slot_advance"}
func (k ReplicationSlotKind) Name() string { if int(k) >= len(replicationSlotNames) { return "" }; return replicationSlotNames[k] }
func slotSignature(k ReplicationSlotKind, arity int) (types [5]sqltype.ID, labels [5]string, declared, required int) {
 switch k {
 case SlotCreatePhysical: return [5]sqltype.ID{sqltype.Name,sqltype.Bool,sqltype.Bool}, [5]string{"slot_name","immediately_reserve","temporary"},3,1
 case SlotCreateLogical: return [5]sqltype.ID{sqltype.Name,sqltype.Name,sqltype.Bool,sqltype.Bool,sqltype.Bool}, [5]string{"slot_name","plugin","temporary","twophase","failover"},5,2
 case SlotDrop: return [5]sqltype.ID{sqltype.Name},labels,1,1
 case SlotCopyPhysical, SlotCopyLogical:
  maxArgs:=3; if k==SlotCopyLogical { maxArgs=4 }
  if arity<2 || arity>maxArgs { return }
  return [5]sqltype.ID{sqltype.Name,sqltype.Name,sqltype.Bool,sqltype.Name},[5]string{"src_slot_name","dst_slot_name","temporary","plugin"},arity,arity
 case SlotAdvance: return [5]sqltype.ID{sqltype.Name,sqltype.PgLSN},[5]string{"slot_name","upto_lsn"},2,2
 }
 return
}

// ReplicationSlot owns immutable written operands and their formal mapping.
// Missing copy options are represented by Arity, not manufactured NULL values.
type ReplicationSlot struct {
 expr.Info
 Kind ReplicationSlotKind
 Arity uint8
 Args [5]expr.Expr
 Slots [5]uint8
 Labels [5]string
}
func bindReplicationSlot(b Binder, raw parser.FunctionExpr) (expr.Expr,error) {
 var operands [5]Argument
 var inline [5]string
 names:=inline[:min(len(raw.Args),len(inline))]
 if len(raw.Args)>len(inline) && len(raw.Args)<=100 { names=make([]string,len(raw.Args)) }
 for i,child:=range raw.Args {
  a,err:=b.AnalyzeArgument(child,AnalysisRequest{Intent:ComparisonArgument}); if err!=nil { return nil,err }
  if i<5 { operands[i]=a }; typ:="unknown"; if a.Usage.Type!=nil { typ=b.DiagnosticTypeName(a.Usage.Type) }; if i<len(names) { names[i]=NamedArgumentType(raw,i,typ) }
 }
 if len(raw.Args)>100 { return nil,expr.ErrorAt("54023","cannot pass more than 100 arguments to a function",raw.Pos) }
 if err:=ValidateCallSyntax(b,raw); err!=nil { return nil,err }
 visible,err:=b.BuiltinVisible(raw.Name); if err!=nil { return nil,err }
 name:=raw.Name.Parts[len(raw.Name.Parts)-1].Text
 kind:=ReplicationSlotKind(len(replicationSlotNames)); for i,n:=range replicationSlotNames { if n==name { kind=ReplicationSlotKind(i); break } }
 types,labels,declared,required:=slotSignature(kind,len(raw.Args))
 slots,valid:=namedSlots(raw,labels[:declared],required)
 if kind==SlotDrop && len(raw.ArgNames)!=0 { for _,label:=range raw.ArgNames { if label.Text!="" { valid=false } } }
 if !visible || declared==0 || !valid || raw.Variadic || raw.ResolutionOnly { return nil,b.SignatureError(raw.Name,raw.Pos,names) }
 node:=ReplicationSlot{Kind:kind,Arity:uint8(len(raw.Args))}
 properties:=expr.Properties{Volatility:expr.Volatile}
 for i:=range raw.Args {
  target:=b.Type(types[slots[i]]).Usage()
  if operands[i].Usage.Type!=nil { if _,failure:=sqltype.PlanCoercion(operands[i].Usage,target,sqltype.Implicit); failure!=nil { return nil,b.SignatureError(raw.Name,raw.Pos,names) } }
  arg,err:=b.CoerceArgument(operands[i],CoercionRequest{Intent:ExactFormalArgument,Target:target}); if err!=nil { return nil,err }
  node.Args[i]=arg; node.Slots[i]=uint8(slots[i]); if len(raw.ArgNames)!=0 { node.Labels[i]=raw.ArgNames[i].Text }
  properties=expr.MergeProperties(properties,arg.Properties())
 }
 if raw.Star { return nil,expr.ErrorAt("42809",name+"(*) specified, but "+name+" is not an aggregate function",raw.Pos) }
 if err:=CallDecorationError(raw); err!=nil { return nil,err }
 result:=b.Type(sqltype.Void)
 if kind!=SlotDrop {
  second:="lsn"; if kind==SlotAdvance { second="end_lsn" }
  result,err=sqltype.RecordShape([]sqltype.CompositeMember{{Attribute:1,Name:"slot_name",Usage:b.Type(sqltype.Name).Usage()},{Attribute:2,Name:second,Usage:b.Type(sqltype.PgLSN).Usage()}}); if err!=nil { return nil,err }
 }
 node.Info,err=b.BuiltinRoutineInfo(b.Info(result.Usage(),raw.Pos,properties),CatalogRoutineProvider,name,types[:declared]...)
 return node,err
}
func namedReplicationSlot(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage,[]sqltype.Usage,bool) {
 name:=raw.Name.Parts[len(raw.Name.Parts)-1].Text
 for i,n:=range replicationSlotNames {
  if name!=n || ReplicationSlotKind(i)==SlotDrop { continue }
  types,labels,count,required:=slotSignature(ReplicationSlotKind(i),len(declared))
  if count!=len(declared) || !declaredTypes(declared,types[:count]) { return nil,nil,false }
  return fixedNamedInputs(b,raw,args,declared,labels[:count],required)
 }
 return nil,nil,false
}
func (n ReplicationSlot) Validate() error {
 types,labels,count,required:=slotSignature(n.Kind,int(n.Arity))
 if count==0 || int(n.Arity)<required || int(n.Arity)>count || n.Usage.Type==nil { return fmt.Errorf("stuntgres: invalid replication slot signature") }
 var seen [5]bool
 for i:=range int(n.Arity) {
  slot:=int(n.Slots[i]); a:=n.Args[i]
  if slot>=count || seen[slot] || a==nil || a.ResultUsage().Type==nil || a.ResultUsage().Type.ID()!=types[slot] || n.Labels[i]!="" && n.Labels[i]!=labels[slot] { return fmt.Errorf("stuntgres: invalid replication slot operand") }; seen[slot]=true
 }
 for i:=range required { if !seen[i] { return fmt.Errorf("stuntgres: missing replication slot operand") } }
 for i:=int(n.Arity); i<5; i++ { if n.Args[i]!=nil || n.Labels[i]!="" || n.Slots[i]!=0 { return fmt.Errorf("stuntgres: unused replication slot operand") } }
 d:=n.Usage.Type
 if n.Kind==SlotDrop { if d.ID()!=sqltype.Void { return fmt.Errorf("stuntgres: invalid slot void result") }; return nil }
 if d.ID()!=2249 || !d.IsComposite() || d.CompositeLen()!=2 { return fmt.Errorf("stuntgres: invalid slot record") }
 for i,label:=range []string{"slot_name","lsn"} { id:=sqltype.Name; if i==1 { id=sqltype.PgLSN; if n.Kind==SlotAdvance { label="end_lsn" } }; m:=d.CompositeMember(i); if m.Attribute!=int32(i+1) || m.Name!=label || m.Usage.Type==nil || m.Usage.Type.ID()!=id { return fmt.Errorf("stuntgres: invalid slot member") } }
 return nil
}
type ReplicationSlotRuntime interface { ReplicationSlot(ReplicationSlotKind,uint8,[5]sqltype.Value) error }
func (n ReplicationSlot) Eval(ctx expr.Context) (sqltype.Value,error) {
 if _,err:=evaluationContext(ctx); err!=nil { return sqltype.Value{},err }
 var values [5]sqltype.Value
 null:=false
 for i:=range int(n.Arity) { v,err:=n.Args[i].Eval(ctx); if err!=nil { return sqltype.Value{},err }; values[n.Slots[i]]=v; null=null||v.IsNull() }
 if null { return sqltype.Null(n.Usage.Type),nil }
 runtime,ok:=ctx.(ReplicationSlotRuntime); if !ok { return sqltype.Value{},fmt.Errorf("stuntgres: missing replication slot runtime") }
 return sqltype.Value{},runtime.ReplicationSlot(n.Kind,n.Arity,values)
}
