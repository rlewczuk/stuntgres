package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var fileReadUnitIDs = []ids.OID{3826,6208,2624,3293,3828,6209,3827,3295,2623,3307,5029,5030}

func TestGenericFileReadBinding(t *testing.T) {
	e := engine.New()
	for _,name := range []string{"pg_read_file","pg_read_binary_file","pg_stat_file"} {
		for arity := 1; arity <= 4; arity++ {
			if name == "pg_stat_file" && arity > 2 { continue }
			args := make([]parser.Expr,arity)
			for i := range args { args[i] = parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}} }
			n := catalogRelationsUnitBind(t,e,name,args...).(functions.GenericFileRead)
			if err := n.Validate(); err != nil { t.Fatal(err) }
			rebuilt,err := n.Rebuild(n.Info,n.Children())
			if err != nil || !analyze.SameGroupExpression(n,rebuilt) || expr.InfoOf(rebuilt).RoutineReference != n.RoutineReference { t.Fatal("reconstruction",err) }
			if exec.SourceReturnsSet(n) || !expr.AdmitsSource(n) { t.Fatal("scalar source classification") }
			if _,err := n.Render(); err != nil { t.Fatal(err) }
			want := expr.StrictAfterBare
			if name == "pg_stat_file" { want = expr.StrictNone }
			if n.StrictNull() != want { t.Fatal("planning strictness",name) }
		}
	}
	for _,oid := range fileReadUnitIDs {
		p,ok := e.CatalogRead().Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:oid})
		if !ok || p.Builtin == nil || !p.Strict || p.Volatility != expr.Volatile || p.Builtin.Parallel != 's' { t.Fatal("declaration",oid) }
		count := 1; if oid == 5029 || oid == 5030 { count = 2 }
		if !p.ACL.Explicit || len(p.ACL.Entries) != count { t.Fatal("seed",oid,p.ACL) }
	}
}

func TestGenericFileReadRuntime(t *testing.T) {
	e := engine.New()
	host := core.HostOf(tablespaceInfoUnitScope(t,e))
	before := e.CatalogRead().State()
	for _,kind := range []functions.GenericFileReadKind{functions.GenericFileText,functions.GenericFileBinary,functions.GenericFileStat} {
		for _,tc := range []struct{length int64; explicit bool; code,message string}{
			{-1,true,"22023","requested length cannot be negative"},
			{1073741820,true,"22023","requested length too large"},
			{0,false,"0A000",""},{0,true,"0A000",""},{1073741819,true,"0A000",""},
		} {
			if kind == functions.GenericFileStat && tc.explicit { continue }
			var d *sqlstate.Error
			err := host.GenericFileRead(kind,"PG_VERSION",tc.length,tc.explicit)
			if !errors.As(err,&d) || d.Code != tc.code || tc.message != "" && d.Message != tc.message { t.Fatal(kind,tc,err) }
		}
	}
	for _,tc := range []struct{oid uint32; code string}{{0,"42704"},{4294967295,"42704"},{1663,"0A000"},{1664,"0A000"}} {
		var d *sqlstate.Error
		if err := host.GenericFileTmpdir(tc.oid); !errors.As(err,&d) || d.Code != tc.code { t.Fatal(tc,err) }
	}
	if before != e.CatalogRead().State() { t.Fatal("physical rejection published state") }
}

func TestGenericFileReadOwnership(t *testing.T) {
	e := engine.New()
	n := catalogRelationsUnitBind(t,e,"pg_stat_file",parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}}).(functions.GenericFileRead)
	children := n.Children()
	children[0] = nil
	if n.Args[0] == nil { t.Fatal("borrowed child backing") }
	ctx := &exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(tablespaceInfoUnitScope(t,e))}}
	cursor,err := n.OpenSource(ctx)
	if err != nil || cursor.Exhausted { t.Fatal("scalar NULL must yield a row",err) }
	value,present,err := cursor.Next(context.Background())
	if err != nil || !present || !value.IsNull() || value.Type() != n.ResultUsage().Type { t.Fatal("typed scalar NULL",value,present,err) }
	cursor.Close()
	text,_ := e.CatalogRead().LookupType(sqltype.Text)
	boolean,_ := e.CatalogRead().LookupType(sqltype.Bool)
	order := []int{}
	n = catalogRelationsUnitBind(t,e,"pg_stat_file",parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}},parser.LiteralExpr{Value:parser.Value{Kind:parser.NullValue}}).(functions.GenericFileRead)
	n.Slots = [4]uint8{1,0,2,3}
	n.Args[0] = fileListOperand{plan.LiteralExpr{Info:expr.Info{Usage:boolean.Usage()},Value:sqltype.Null(boolean)},&order,0}
	n.Args[1] = fileListOperand{plan.LiteralExpr{Info:expr.Info{Usage:text.Usage()},Value:sqltype.String(text,"PG_VERSION")},&order,1}
	if err := n.Validate(); err != nil { t.Fatal(err) }
	for range 2 { if _,err := n.Eval(ctx); err != nil { t.Fatal(err) } }
	if len(order) != 4 || order[0] != 0 || order[1] != 1 || order[2] != 0 || order[3] != 1 { t.Fatal("written evaluation order",order) }
	canceled,cancel := context.WithCancel(context.Background()); cancel(); ctx.Context = canceled
	if _,err := n.Eval(ctx); !errors.Is(err,context.Canceled) { t.Fatal("pre-cancellation",err) }
	foreign := tablespaceInfoUnitScope(t,e)
	foreign.Inquiry.CommentCatalog = engine.New().CatalogRead()
	if err := core.HostOf(foreign).GenericFileTmpdir(1663); err == nil { t.Fatal("foreign catalog snapshot admitted") }
}

func TestGenericFileReadACLControls(t *testing.T) {
	e,other := engine.New(),engine.New()
	for _,oid := range fileReadUnitIDs {
		a := ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:oid}
		p,_ := e.CatalogRead().Procedure(a); q,_ := other.CatalogRead().Procedure(a)
		if &p.ACL.Entries[0] == &q.ACL.Entries[0] { t.Fatal("shared mutable ACL",oid) }
	}
}
