package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIntarraySliceBinding(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t,e,"postgres"); defer done()
	enginetest.RoleExecute(t,e,op,"CREATE EXTENSION intarray")
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"intarray"})).Installation
	for operation := sqltype.IntarrayIcount; operation < sqltype.IntarraySliceOperationCount; operation++ {
		procedure := view.MustProcedure(installation.Members[extension.IntarraySliceProcedureStart+int(operation)])
		n := operation.Arity()
		args := make([]parser.Expr,n)
		for i := range args {
			typ := parser.TypeName{Name:"integer"}
			if i == 0 && operation != sqltype.IntarrayIntset { typ.ArrayBounds = []int32{-1} }
			args[i] = parser.CastExpr{Child:parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}},TypeName:typ,Syntax:parser.PostfixCast}
		}
		raw := enginetest.FunctionCall(procedure.Name(),args...)
		e.Mu.Lock()
		auth,err := e.AuthorizationLocked(op)
		if err != nil { e.Mu.Unlock(); t.Fatal(err) }
		b := core.NewExpressionBinder(context.Background(),e,sqltype.InputEnvironment{},op,nil)
		b.Query.Auth,b.Params.NoParameters = &auth,true
		bound,err := b.BindFunctionProvider(raw)
		e.Mu.Unlock()
		if err != nil { t.Fatal(operation,err) }
		node,ok := bound.(functions.IntarraySlice)
		if !ok || node.Operation != operation || node.Member != uint8(32+operation) || node.RoutineReference != procedure || node.Ref.Address != procedure.Address() { t.Fatal("selected role",bound) }
		selected,err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || selected != procedure { t.Fatal("opaque selected declaration",err) }
		found := false
		for _,dep := range b.Dependencies { if dep.Ref == node.Ref && dep.Revision == procedure.Revision() { found = true } }
		if !found { t.Fatal("selected dependency missing") }
		frozen,ok := b.NormalizeStoredExpression(raw).(parser.FunctionExpr)
		if !ok || !frozen.StoredProviderRoutine || frozen.StoredProviderSymbol != procedure.Display.Source { t.Fatal("stored provenance missing") }
		for _,rebuild := range []func(expr.Info,[]expr.Expr)(expr.Expr,error){node.Rebuild,node.Substitute} {
			next,err := rebuild(node.Info,node.Children())
			if err != nil || !reflect.DeepEqual(next,node) { t.Fatal("immutable rebuild",err) }
			if _,err := rebuild(node.Info,node.Children()[:n-1]); err == nil { t.Fatal("wrong arity rebuilt") }
		}
		for _,kind := range []expr.CaptureKind{expr.CaptureFunction,expr.CaptureOperator} {
			capture,ok,err := node.Capture(kind)
			if err != nil || !ok || capture.Count != n || !reflect.DeepEqual(capture.Args,node.Children()) { t.Fatal("ordered capture",err) }
		}
		spec,err := node.Render()
		if err != nil || len(spec.Args) != n { t.Fatal("formal rendering",err) }
		for i,arg := range spec.Args { if arg.TransportType != node.Formals[i].Usage() || arg.Expr != node.Args[i] { t.Fatal("rendering lost formal/operand order") } }
		next,changed,err := node.Retag(node.Info,node.Children(),false,expr.Retag{Type:func(d *sqltype.Descriptor)*sqltype.Descriptor{return d}})
		if err != nil || changed || !reflect.DeepEqual(next,node) { t.Fatal("identity retag",err) }
		if !node.SameGroup(node,func(a,b expr.Expr)bool{return a==b},false) { t.Fatal("same overload not grouped") }
		if admitted,set := node.SourceAdmission(); !admitted || set { t.Fatal("scalar source contract") }
		for _,populated := range []bool{false,true} {
			counts := [3]int{}
			var order []int
			children := make([]expr.Expr,n)
			for i := range children {
				value := sqltype.Null(node.Formals[i])
				if populated {
					if node.Formals[i].IsArray() { value,err = node.Formals[i].Input(t.Context(),"{11,22,33,22}",-1) } else { value,err = sqltype.Integer(node.Formals[i],2) }
					if err != nil { t.Fatal(err) }
				}
				children[i] = intarraySliceOperand{intarrayCountingOperand:intarrayCountingOperand{Info:expr.Info{Usage:node.Formals[i].Usage()},Value:value,Calls:&counts[i]},Index:i,Order:&order}
			}
			counted,err := node.Rebuild(node.Info,children); if err != nil { t.Fatal(err) }
			context := &intarraySliceEntryContext{Context:&exec.Context{EvalStatement:exec.EvalStatement{Context:t.Context()}}}
			value,err := counted.Eval(context)
			if context.Entries!=1 || context.Leaves!=1 || context.Reference!=procedure || context.Count!=n { t.Fatal("selected routine entered/left more than once") }
			if err != nil || value.IsNull() == populated || value.Type() != node.Usage.Type || len(order) != n { t.Fatal("strict/populated evaluation",err) }
			for i := range n { if counts[i] != 1 || order[i] != i { t.Fatal("operands not evaluated once in source order",counts,order) } }
			if !populated && n > 1 {
				failure := errors.New("later operand sentinel")
				last := children[n-1].(intarraySliceOperand); last.Failure = failure; children[n-1] = last
				counted,err = node.Rebuild(node.Info,children); if err != nil { t.Fatal(err) }
				if _,err := counted.Eval(&exec.Context{EvalStatement:exec.EvalStatement{Context:t.Context()}}); !errors.Is(err,failure) { t.Fatal("NULL skipped a later demanded error",err) }
			}
		}
		for _,corrupt := range []func(*functions.IntarraySlice){
			func(n *functions.IntarraySlice){n.Member++},
			func(n *functions.IntarraySlice){n.Operation=sqltype.IntarraySliceOperationCount},
			func(n *functions.IntarraySlice){n.Args[0]=nil},
			func(n *functions.IntarraySlice){n.Formals[0]=nil},
			func(n *functions.IntarraySlice){n.Usage.Type=nil},
		} { bad:=node; corrupt(&bad); if bad.Validate()==nil { t.Fatal("invalid node admitted") } }
		if n < 3 {
			bad:=node; bad.Args[n]=node.Args[0]
			if bad.Validate()==nil { t.Fatal("inactive operand admitted") }
			bad=node; bad.Formals[n]=node.Formals[0]
			if bad.Validate()==nil { t.Fatal("inactive formal admitted") }
		}
	}
}

type intarraySliceOperand struct {
	intarrayCountingOperand
	Index int
	Order *[]int
	Failure error
}
func (e intarraySliceOperand) Eval(c expr.Context)(sqltype.Value,error) {
	*e.Order=append(*e.Order,e.Index)
	if e.Failure != nil { (*e.Calls)++; return sqltype.Value{},e.Failure }
	return e.intarrayCountingOperand.Eval(c)
}

type intarraySliceEntryContext struct {
	*exec.Context
	Entries, Leaves, Count int
	Reference expr.RoutineReference
}
func (c *intarraySliceEntryContext) FunctionEnterRoutine(reference expr.RoutineReference, _ expr.RoutineRecipeReference, inputs expr.RoutineBodyInputs)(sqltype.RoutineBodyToken,error) {
	c.Entries++
	c.Reference,c.Count=reference,inputs.Count
	return sqltype.NewRoutineBodyToken(c,0,1),nil
}
func (c *intarraySliceEntryContext) LeaveRoutineBody(_ int,_ uint64) { c.Leaves++ }
