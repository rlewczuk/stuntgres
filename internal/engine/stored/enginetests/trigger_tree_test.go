package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTriggerTreeCarrier(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{"CREATE TABLE tree_owner(a integer)","CREATE FUNCTION tree_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$","CREATE TRIGGER actual BEFORE UPDATE ON tree_owner FOR EACH ROW WHEN(COALESCE(OLD.a::bigint,0)>0 AND NOT(OLD.a IS NULL)) EXECUTE FUNCTION tree_fn()","CREATE TRIGGER new_side BEFORE UPDATE ON tree_owner FOR EACH ROW WHEN(NEW.a>0) EXECUTE FUNCTION tree_fn()","CREATE TRIGGER absent BEFORE UPDATE ON tree_owner FOR EACH ROW EXECUTE FUNCTION tree_fn()"} { enginetest.SessionSQL(t,s,q) }
	read:=e.CatalogRead()
	address:=enginetest.CatalogRef(t,e,read,"tree_owner").Address
	owner:=read.MustRelation(address)
	list:=owner.Triggers.(stored.TriggerList)
	typ,_:=read.LookupType(sqltype.PgNodeTree)
	index:=func(name string) int { for i:=range list { if list[i].Name()==name { return i } }; t.Fatal("fixture trigger missing",name);return -1 }
	i:=index("actual")
	value,err:=stored.TriggerTree(t.Context(),read,owner,typ,&list[i])
	if err!=nil { t.Fatal(err) }
	tree,err:=value.Tree();if err!=nil { t.Fatal(err) }
	carrier:=tree.(stored.TriggerCarrier)
	if reflect.ValueOf(carrier).FieldByName("definition").Pointer()!=reflect.ValueOf(&list[i]).Pointer() { t.Fatal("carrier detached actual backing") }
	original:=list[i].Parts()
	if original.When==nil || original.Presentation==nil || original.Binding==nil { t.Fatal("missing genuine checked backing") }
	ctx,cancel:=context.WithCancel(t.Context());cancel()
	if _,err:=value.Type().Output(ctx,value);!errors.Is(err,context.Canceled) { t.Fatal("canceled lazy output",err) }
	raw,err:=value.Type().Output(t.Context(),value)
	if err!=nil || !strings.Contains(raw,"Var attnum 1 scope") || !strings.Contains(raw,"frame 1") { t.Fatal("lost scalar namespace",raw,err) }
	var diagnostic *sqlstate.Error
	if err:=carrier.Preflight(t.Context(),0);!errors.As(err,&diagnostic) || diagnostic.Code!="22023" || diagnostic.Message!="expression contains variables" { t.Fatal("zero context",err) }
	if err:=carrier.Preflight(t.Context(),4294967295);err!=nil { t.Fatal("OLD preflight must precede lookup",err) }
	newValue,err:=stored.TriggerTree(t.Context(),read,owner,typ,&list[index("new_side")]);if err!=nil { t.Fatal(err) }
	newTree,_:=newValue.Tree()
	column := plan.ColumnExpr{Info:expr.Info{Usage:owner.Columns[0].Usage},Origin:original.Relation,Scope:analyze.RulePseudoScope,Frame:1}
	column.Origin.Address.SubID=owner.Attributes[0]
	oldScalar,err:=(plan.BoundTree{Expression:column}).StructuralOutput(t.Context());if err!=nil { t.Fatal(err) }
	column.Frame=2
	newScalar,err:=(plan.BoundTree{Expression:column}).StructuralOutput(t.Context());if err!=nil || oldScalar==newScalar { t.Fatal("scalar OLD/NEW collapsed at identical attnum",oldScalar,newScalar,err) }
	column.Scope,column.Frame=1,1
	ordinary,err:=(plan.BoundTree{Expression:column}).StructuralOutput(t.Context())
	if err!=nil || !strings.Contains(ordinary,"Var attnum 1") || strings.Contains(ordinary," scope ") || strings.Contains(ordinary," frame ") { t.Fatal("ordinary CHECK-like column format changed",ordinary,err) }
	record,err:=sqltype.RecordShape(nil);if err!=nil { t.Fatal(err) }
	ordinaryRow,err:=(plan.BoundTree{Expression:plan.RowExpr{Info:expr.Info{Usage:record.Usage()}}}).StructuralOutput(t.Context())
	if err!=nil || strings.Contains(ordinaryRow," side ") { t.Fatal("ordinary row format changed",ordinaryRow,err) }
	diagnostic=nil
	if err:=newTree.(stored.TriggerCarrier).Preflight(t.Context(),4294967295);!errors.As(err,&diagnostic) || diagnostic.Code!="22023" || diagnostic.Message!="expression contains variables of more than one relation" { t.Fatal("NEW preflight",err) }
	absent,err:=stored.TriggerTree(t.Context(),read,owner,typ,&list[index("absent")]);if err!=nil || !absent.IsNull() || absent.Type()!=typ { t.Fatal("typed absent WHEN",absent,err) }
	foreign:=engine.New().CatalogRead();foreignType,_:=foreign.LookupType(sqltype.PgNodeTree)
	copy:=list[i]
	for _,tc:=range []struct{read catalog.Read;owner *catalog.Relation;typ *sqltype.Descriptor;definition *stored.Trigger}{{read,owner,typ,&copy},{foreign,owner,typ,&list[i]},{read,owner,foreignType,&list[i]}} {
		if _,err:=stored.TriggerTree(t.Context(),tc.read,tc.owner,tc.typ,tc.definition);err==nil { t.Fatal("foreign/detached carrier accepted") }
	}
	boolean,_:=read.LookupType(sqltype.Bool)
	invalid:=original
	invalid.When=plan.ParameterExpr{Info:expr.Info{Usage:boolean.Usage()},Number:1}
	borrowed,err:=stored.NewTrigger(invalid);if err!=nil { t.Fatal(err) }
	badOwner:=*owner;badOwner.Triggers=stored.TriggerList{borrowed}
	state:=catalog.CloneState(read.State());state.SetDescriptor(owner.Address(),&badOwner)
	badRead:=read.WithState(state);badList:=badOwner.Triggers.(stored.TriggerList)
	if _,err:=stored.TriggerTree(t.Context(),badRead,&badOwner,typ,&badList[0]);err==nil { t.Fatal("invocation parameter admitted to immutable carrier") }
	integer,_:=read.LookupType(sqltype.Int4);number,err:=sqltype.Integer(integer,1);if err!=nil { t.Fatal(err) }
	invalid.When=plan.LiteralExpr{Info:expr.Info{Usage:integer.Usage()},Value:number}
	if _,err:=stored.NewTrigger(invalid);err==nil { t.Fatal("nonboolean WHEN admitted") }
	if _,err:=stored.TriggerTree(ctx,read,owner,typ,&list[i]);!errors.Is(err,context.Canceled) { t.Fatal("constructor cancellation",err) }
	if err:=carrier.Preflight(ctx,1);!errors.Is(err,context.Canceled) { t.Fatal("preflight cancellation",err) }
	if _,err:=stored.TriggerDisplay(ctx,carrier,stored.Deparser{View:read,Relation:owner});!errors.Is(err,context.Canceled) { t.Fatal("display cancellation",err) }
	for _,q:=range []string{"CREATE OR REPLACE TRIGGER actual BEFORE UPDATE ON tree_owner FOR EACH ROW WHEN(false) EXECUTE FUNCTION tree_fn()","DROP TRIGGER actual ON tree_owner"} { enginetest.SessionSQL(t,s,q) }
	if now:=list[i].Parts();!reflect.DeepEqual(now.When,original.When) || now.Presentation!=original.Presentation || now.Binding!=original.Binding { t.Fatal("replacement mutated original definition backing") }
	if retained,err:=carrier.StructuralOutput(t.Context());err!=nil || retained!=raw { t.Fatal("retained output",retained,err) }

	// Exercise actual checked payloads built through the stored definition API.
	// Whole-row SQL declaration capture is a separate preexisting DDL boundary;
	// these carriers still preserve and consume the executable RuleSide node.
	for _,empty:=range []bool{false,true} {
		t.Run(map[bool]string{false:"WholeRow",true:"EmptyWholeRow"}[empty],func(t *testing.T) {
			name:="tree_whole";if empty { name="tree_empty" }
			ddl:="CREATE TABLE "+name+"(a integer)";if empty { ddl="CREATE TABLE "+name+"()" }
			enginetest.SessionSQL(t,s,ddl)
			enginetest.SessionSQL(t,s,"CREATE TRIGGER base BEFORE UPDATE ON "+name+" FOR EACH ROW WHEN(true) EXECUTE FUNCTION tree_fn()")
			base:=e.CatalogRead();owner:=base.MustRelation(enginetest.CatalogRef(t,e,base,name).Address)
			parts:=owner.Triggers.(stored.TriggerList)[0].Parts()
			boolType,_:=base.LookupType(sqltype.Bool);rowType,_:=base.LookupType(owner.RowType)
			var outputs [2]string
			for side:=uint32(1);side<=2;side++ {
				fields:=make([]expr.Expr,len(owner.Columns))
				for i,col:=range owner.Columns { ref:=parts.Relation;ref.Address.SubID=owner.Attributes[i];fields[i]=plan.ColumnExpr{Info:expr.Info{Usage:col.Usage,Property:expr.Properties{RowDependent:true}},Origin:ref,Scope:analyze.RulePseudoScope,Frame:side,Slot:i};parts.References=append(parts.References,ref.Address) }
				row:=plan.RowExpr{Info:expr.Info{Usage:rowType.Usage(),Property:expr.Properties{RowDependent:true}},Fields:fields,RuleSide:side}
				parts.When=plan.NullTestExpr{Info:expr.Info{Usage:boolType.Usage(),Property:expr.Properties{RowDependent:true}},Child:row,Not:true,Whole:true}
				parts.References=append(parts.References,ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(owner.RowType)})
				definition,err:=stored.NewTrigger(parts);if err!=nil { t.Fatal(err) }
				clone:=*owner;clone.Triggers=stored.TriggerList{definition}
				state:=catalog.CloneState(base.State());state.SetDescriptor(owner.Address(),&clone);captured:=base.WithState(state)
				list:=clone.Triggers.(stored.TriggerList);typ,_:=captured.LookupType(sqltype.PgNodeTree)
				value,err:=stored.TriggerTree(t.Context(),captured,&clone,typ,&list[0]);if err!=nil { t.Fatal("checked whole row",err) }
				tree,_:=value.Tree();carrier:=tree.(stored.TriggerCarrier)
				outputs[side-1],err=carrier.StructuralOutput(t.Context());if err!=nil { t.Fatal(err) }
				if err:=carrier.Preflight(t.Context(),0);err==nil { t.Fatal("whole-row lost Var with zero context") }
				if side==2 { if err:=carrier.Preflight(t.Context(),4294967295);err==nil { t.Fatal("NEW whole-row lost namespace") };continue }
				display,err:=stored.TriggerDisplay(t.Context(),carrier,stored.Deparser{View:captured,Relation:&clone})
				if err!=nil || !strings.Contains(display,name+".*") || strings.Contains(display,"ROW(") { t.Fatal("whole-row display",display,err) }
			}
			if outputs[0]==outputs[1] { t.Fatal("whole-row OLD/NEW structural identity collapsed",outputs) }
		})
	}
}
