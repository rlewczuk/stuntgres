package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func intarraySliceDependencyOwnership(t *testing.T) {
	e:=engine.New()
	s:=intarraySliceOwnershipSetup(t,e)
	view:=e.CatalogRead()
	relation:=view.MustRelation(view.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},Name:"ia_owner"}))
	column:=func(index int)ids.ObjectAddress {address:=relation.Address();address.SubID=relation.Attributes[index];return address}
	for _,tc:=range []struct{operator,function string; binary bool}{
		{"OPERATOR(public.#) a","public.icount(a)",false},
		{"a OPERATOR(public.#) n","public.idx(a,n)",true},
	} {
		binding:=selectedDependencyBinding(t,e,s,"SELECT "+tc.operator+","+tc.function+" FROM ia_owner")
		project:=binding.Query.Root.(*plan.ProjectNode)
		selected:=project.Expressions[0].(plan.SelectedOperatorExpr)
		backer:=selectedDependencyProcedure(t,selected.Provider)
		if selected.RoutineReference!=nil || backer!=selectedDependencyProcedure(t,project.Expressions[1]) || selected.Operator.Function!=backer.Address().ObjectID {t.Fatal("operator/backer ownership split")}
		selectedDependencyManifest(t,binding,backer)
		found:=false
		for _,dep:=range binding.Dependencies {if dep.Ref==selected.Ref && dep.Revision==selected.Operator.Revision(){found=true}}
		if !found {t.Fatal("selected operator revision missing")}
		before:=append([]catalog.ExpressionDependency(nil),binding.Dependencies...)
		columns:=[]ids.ObjectAddress{column(0)};if tc.binary {columns=append(columns,column(2))}
		got,err:=stored.ExpressionDependRefs(t.Context(),view,selected)
		selectedDependencySet(t,got,err,append(append([]ids.ObjectAddress(nil),columns...),selected.Ref.Address)...)
		got,err=stored.ExpressionDependRefs(t.Context(),view,project.Expressions[1])
		selectedDependencySet(t,got,err,append(append([]ids.ObjectAddress(nil),columns...),backer.Address())...)
		got,err=stored.QueryDependRefs(t.Context(),view,nil,binding.Query)
		selectedDependencySet(t,got,err,append(columns,selected.Ref.Address,backer.Address())...)
		if !reflect.DeepEqual(before,binding.Dependencies){t.Fatal("edge traversal mutated executable manifest")}
	}
	for _,tc:=range []struct{call string; columns []ids.ObjectAddress}{
		{"public.subarray(a,n)",[]ids.ObjectAddress{column(0),column(2)}},
		{"public.subarray(a,n,n)",[]ids.ObjectAddress{column(0),column(2)}},
		{"public.intset(n)",[]ids.ObjectAddress{column(2)}},
	} {
		binding:=selectedDependencyBinding(t,e,s,"SELECT "+tc.call+" FROM ia_owner")
		node:=binding.Query.Root.(*plan.ProjectNode).Expressions[0]
		call:=intarraySliceOwnershipCall(t,node)
		selectedDependencyManifest(t,binding,selectedDependencyProcedure(t,node))
		capture,ok,err:=call.Capture(expr.CaptureFunction)
		if err!=nil || !ok || capture.Count!=call.Operation.Arity() || !reflect.DeepEqual(capture.Args,call.Children()) {t.Fatal("capture deduplicated or reordered written operands",err)}
		got,err:=stored.ExpressionDependRefs(t.Context(),view,node)
		selectedDependencySet(t,got,err,append(tc.columns,call.Ref.Address)...)
	}
}
