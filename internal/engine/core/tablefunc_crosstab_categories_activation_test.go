package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

// Err interrupts only after the actual nested command has published its own
// expected analysis. Result names distinguish category from source admission;
// no timing, call-count threshold, or production inspection hook is involved.
type categoriesActivationCancelContext struct {
	context.Context
	scope *core.StatementScope
	column string
	cancel context.CancelFunc
	entered bool
}
func (c *categoriesActivationCancelContext) Err() error {
	if expected:=c.scope.Expected; expected!=nil && len(expected.Results)>0 && expected.Results[0].Name==c.column {
		c.entered=true
		c.cancel()
	}
	return c.Context.Err()
}

func TestTablefuncCrosstabCategoriesActivationCleanup(t *testing.T) {
	e:=engine.New()
	crosstabUnitInstalled(t,e)
	enginetest.Run(t,e,"CREATE TABLE ct_hash_activation(k text,category text,v text)")
	enginetest.Run(t,e,"INSERT INTO ct_hash_activation VALUES('g','a','10')")
	enginetest.Run(t,e,"CREATE TABLE ct_hash_categories(category text)")
	enginetest.Run(t,e,"INSERT INTO ct_hash_categories VALUES('a')")
	n:=categoriesExpression(t,e)
	reader,_:=blockingUnitOwner(t,e)
	scope:=pgrowlocksUnitScope(t,e,reader)
	scope.RoutineDependencies=[]catalog.ExpressionDependency{{Ref:n.Ref,Revision:n.Procedure.Revision()}}
	e.Mu.Lock();auth,err:=core.ScopeWriteAuthorizationLocked(scope,e);e.Mu.Unlock()
	if err!=nil{t.Fatal(err)}
	scope.RoutineAuth=auth
	beforeView,beforeOutput,beforeInput:=scope.View,scope.Output,scope.Input
	beforeAuthority,beforeExpected:=core.ScopeAuthority(scope),scope.Expected
	beforeCatalog,beforePrincipal:=scope.RoutineAuth.Catalog.State(),scope.RoutineAuth.State.CurrentUser
	beforeDependencies:=append([]catalog.ExpressionDependency(nil),scope.RoutineDependencies...)
	diagnostic,enum:=reflect.ValueOf(beforeInput.TypeDiagnosticName).Pointer(),reflect.ValueOf(beforeInput.EnumMemberSafe).Pointer()
	beforeInput.TypeDiagnosticName,beforeInput.EnumMemberSafe=nil,nil
	unchanged:=func(){
		t.Helper()
		input:=scope.Input
		gotDiagnostic,gotEnum:=reflect.ValueOf(input.TypeDiagnosticName).Pointer(),reflect.ValueOf(input.EnumMemberSafe).Pointer()
		input.TypeDiagnosticName,input.EnumMemberSafe=nil,nil
		if scope.View!=beforeView||core.ScopeAuthority(scope)!=beforeAuthority||scope.Expected!=beforeExpected||scope.RoutineAuth.Catalog.State()!=beforeCatalog||scope.RoutineAuth.State.CurrentUser!=beforePrincipal||!reflect.DeepEqual(scope.RoutineDependencies,beforeDependencies)||diagnostic!=gotDiagnostic||enum!=gotEnum||!reflect.DeepEqual(input,beforeInput)||!reflect.DeepEqual(scope.Output,beforeOutput){t.Fatal("category/source activation leaked outer environment")}
	}
	source:="SELECT k AS source_probe,category,v FROM ct_hash_activation"
	categories:="SELECT category AS category_probe FROM ct_hash_categories"
	check:=func(source,categories string){
		t.Helper()
		values,err:=scope.WriteHost().Contrib().TablefuncCrosstab(source,categories,true,n.Usage.Type)
		if err!=nil||len(values)!=1{t.Fatal("nested hash source",values,err)}
		text,err:=values[0].Type().Output(context.Background(),values[0]);if err!=nil||text!="(g,10)"{t.Fatal(text,err)}
		unchanged()
	}
	check(source,categories)
	check("SELECT 1; "+source,"SELECT 2; "+categories)
	check(source+"; SELECT 1; "+source,categories+"; SELECT 2; "+categories)
	for _,tc:=range []struct{source,categories,code,internal string}{
		{"SELECT )","SELECT missing_category","42703","SELECT missing_category"},
		{"SELECT missing_source",categories,"42703","SELECT missing_source"},
		{source,"SELECT 1,2","22023",""},
		{"SELECT )","SELECT category FROM (VALUES('a'::text),('a')) AS c(category)","42710",""},
		{"SELECT 1,2",categories,"22023",""},
		{"SELECT k,category,'bad'::text FROM ct_hash_activation",categories,"22P02",""},
	}{
		values,err:=scope.WriteHost().Contrib().TablefuncCrosstab(tc.source,tc.categories,true,n.Usage.Type)
		var d *sqlstate.Error
		if values!=nil||!errors.As(err,&d)||d.Code!=tc.code||d.InternalQuery!=tc.internal{t.Fatal("per-string diagnostic/atomic publication",values,err)}
		unchanged()
		check(source,categories)
	}
	for _,column:=range []string{"category_probe","source_probe"}{
		base,cancel:=context.WithCancel(context.Background())
		probe:=&categoriesActivationCancelContext{Context:base,scope:scope,column:column,cancel:cancel}
		original:=scope.Op.Ctx
		scope.Op.Ctx=probe
		values,err:=scope.WriteHost().Contrib().TablefuncCrosstab(source,categories,true,n.Usage.Type)
		scope.Op.Ctx=original
		cancel()
		if !probe.entered||!errors.Is(err,context.Canceled)||values!=nil{t.Fatal("admitted nested cancellation",column,probe.entered,values,err)}
		unchanged()
		check(source,categories)
	}
}
