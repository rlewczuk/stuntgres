package enginetests_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type normalUnitContext struct { *exec.Context; random session.RandomState; ownerCalls int }
func (c *normalUnitContext) FunctionRandomOwner()(expr.RandomOwner,error){c.ownerCalls++;return &c.random,nil}
func normalUnitContextNew()*normalUnitContext{return &normalUnitContext{Context:&exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background()}}}}
func normalUnit(t *testing.T,count int64,mean,dev float64)functions.TablefuncNormal{
	t.Helper();i,f:=seriesUnitType(t,sqltype.Int4),seriesUnitType(t,sqltype.Float8)
	iv,err:=sqltype.Integer(i,count);if err!=nil{t.Fatal(err)};mv,err:=sqltype.Float(f,mean);if err!=nil{t.Fatal(err)};dv,err:=sqltype.Float(f,dev);if err!=nil{t.Fatal(err)}
	return functions.TablefuncNormal{Info:expr.Info{Usage:f.Usage(),Property:expr.Properties{Volatility:expr.Volatile,StageDependent:true}},Args:[3]expr.Expr{plan.LiteralExpr{Info:expr.Info{Usage:i.Usage()},Value:iv},plan.LiteralExpr{Info:expr.Info{Usage:f.Usage()},Value:mv},plan.LiteralExpr{Info:expr.Info{Usage:f.Usage()},Value:dv}}}
}
func TestTablefuncNormalCursor(t *testing.T){
	ctx:=normalUnitContextNew()
	for _,count:=range []int64{0,1,2,3,7}{
		n:=normalUnit(t,count,-7,0);cursor,err:=n.OpenSource(ctx);if err!=nil{t.Fatal(err)}
		if cursor.Values!=nil{t.Fatal("count-sized vector")}
		for range count{v,present,err:=cursor.Next(context.Background());if err!=nil||!present{t.Fatal(present,err)};f,err:=v.Float();if err!=nil||f != -7{t.Fatal(f,err)}}
		for range 2{v,present,err:=cursor.Next(context.Background());if err!=nil||present||!v.IsNull()||v.Type()!=n.Usage.Type{t.Fatal("typed EOF",v,present,err)}}
		cursor.Close();cursor.Close()
		if !ctx.random.Mu.TryLock(){t.Fatal("RNG lock retained")};ctx.random.Mu.Unlock()
	}
	_,err:=normalUnit(t,-1,0,1).OpenSource(ctx);var diagnostic *sqlstate.Error
	if !errors.As(err,&diagnostic)||diagnostic.Code!="22023"||diagnostic.Message!="number of rows cannot be negative"||diagnostic.Position!=""{t.Fatal(err)}
	a,err:=normalUnit(t,3,1,0).OpenSource(ctx);if err!=nil{t.Fatal(err)};defer a.Close()
	b,err:=normalUnit(t,2,9,0).OpenSource(ctx);if err!=nil{t.Fatal(err)};defer b.Close()
	for range 2{for _,cursor:=range []*expr.SourceCursor{&a,&b}{if _,present,err:=cursor.Next(context.Background());err!=nil||!present{t.Fatal(present,err)}}}
	if _,present,err:=b.Next(context.Background());err!=nil||present{t.Fatal(present,err)}
	if _,present,err:=a.Next(context.Background());err!=nil||!present{t.Fatal(present,err)}
	canceled,cancel:=context.WithCancel(context.Background());cancel()
	if _,present,err:=a.Next(canceled);present||!errors.Is(err,context.Canceled)||a.State!=nil{t.Fatal("canceled cursor",present,err)}
	for _,pair:=range [][2]float64{{math.NaN(),0},{0,math.NaN()},{math.Inf(1),0},{math.Inf(-1),1},{0,math.Inf(1)}}{
		cursor,err:=normalUnit(t,3,pair[0],pair[1]).OpenSource(ctx);if err!=nil{t.Fatal(err)}
		for range 3{v,ok,err:=cursor.Next(context.Background());if err!=nil||!ok{t.Fatal(ok,err)};f,err:=v.Float();if err!=nil{t.Fatal(err)};if math.IsNaN(pair[0])||math.IsNaN(pair[1]){if !math.IsNaN(f){t.Fatal(f)}}else if !math.IsInf(f,0)&&!math.IsNaN(f){t.Fatal("raw nonfinite",f)}};cursor.Close()
	}
}
func TestTablefuncNormalExpression(t *testing.T){
	ctx:=normalUnitContextNew();n:=normalUnit(t,3,9,0);var order []int
	for i,arg:=range n.Args{value,err:=arg.Eval(ctx);if err!=nil{t.Fatal(err)};n.Args[i]=normalUnitOperand{Info:expr.InfoOf(arg),run:func()(sqltype.Value,error){order=append(order,i);if i==0{return sqltype.Null(value.Type()),nil};return value,nil}}}
	cursor,err:=n.OpenSource(ctx);if err!=nil{t.Fatal(err)};cursor.Close()
	if !reflect.DeepEqual(order,[]int{0,1,2})||ctx.ownerCalls!=0{t.Fatal(order,ctx.ownerCalls)}
	sentinel:=errors.New("later operand error");n.Args[2]=normalUnitOperand{Info:expr.InfoOf(n.Args[2]),run:func()(sqltype.Value,error){return sqltype.Value{},sentinel}}
	if _,err:=n.OpenSource(ctx);!errors.Is(err,sentinel){t.Fatal("NULL suppressed sibling",err)}
	n=normalUnit(t,3,9,0)
	if n.IntrinsicVolatility()!=expr.Volatile||n.PlanningFold()!=expr.FoldNever{t.Fatal("folded random")}
	rebuilt,err:=n.Rebuild(n.Info,n.Children());if err!=nil||!reflect.DeepEqual(rebuilt,n){t.Fatal(rebuilt,err)}
	if _,err:=n.Rebuild(n.Info,n.Children()[:2]);err==nil{t.Fatal("invalid rebuild")}
	if _,err:=n.Substitute(n.Info,n.Children());err!=nil{t.Fatal(err)}
}
type normalUnitOperand struct{expr.Info;run func()(sqltype.Value,error)}
func(n normalUnitOperand)Eval(expr.Context)(sqltype.Value,error){return n.run()}
