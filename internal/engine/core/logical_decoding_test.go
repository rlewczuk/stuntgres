package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var logicalDecodingUnitNames = []string{"pg_logical_slot_get_changes", "pg_logical_slot_get_binary_changes", "pg_logical_slot_peek_changes", "pg_logical_slot_peek_binary_changes"}

func logicalDecodingUnitNode(t *testing.T, e *core.Engine, name string) functions.LogicalDecoding {
	t.Helper()
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	return catalogRelationsUnitBind(t, e, name, null, null, null).(functions.LogicalDecoding)
}

func logicalDecodingUnitError(t *testing.T, err error, code, message string) *sqlstate.Error {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" { t.Fatalf("diagnostic: %v, want %s %s", err, code, message) }
	return d
}

func TestLogicalDecodingDeclarations(t *testing.T) {
	e, other := engine.New(), engine.New()
	for i, name := range logicalDecodingUnitNames {
		oid := ids.OID(3782+i)
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		p, ok := e.CatalogRead().Procedure(address)
		q, exists := other.CatalogRead().Procedure(address)
		if !ok || !exists || p.Name() != name || p.Strict || p.Volatility != expr.Volatile || p.Result != 2249 || p.Builtin == nil { t.Fatal("missing released declaration", oid) }
		d := p.Builtin
		if d != q.Builtin || d != routine.BuiltinRoutine(oid) || !d.ReturnsSet || d.Parallel != 'u' || d.Defaults != 1 || d.Variadic != 25 || d.Identity.Signature != "19,3220,23,1009" || d.Providers != functions.CatalogRoutineProvider || p.Cost != 1000 || p.RowEstimate() != 1000 { t.Fatal("wrong immutable declaration", oid, d) }
		if p.LiteralDefaults != q.LiteralDefaults || p.LiteralDefaults.Len() != 1 || p.LiteralDefaults.At(0).SourceType != 1009 || p.LiteralDefaults.At(0).Text != "{}" { t.Fatal("default ownership", oid) }
		out := p.OutputDeclaration()
		data := ids.OID(25)
		if i%2 != 0 { data = 17 }
		if out != q.OutputDeclaration() || out.Inputs() != 4 || out.Len() != 3 || out.Type(0) != 3220 || out.Type(1) != 28 || out.Type(2) != data { t.Fatal("output owner", oid) }
		for j, want := range []string{"slot_name", "upto_lsn", "upto_nchanges", "options", "lsn", "xid", "data"} { if out.Name(j) != want { t.Fatal("formal name", oid, j) } }
	}
}

func TestLogicalDecodingExpressionValidation(t *testing.T) {
	e := engine.New()
	for _, name := range logicalDecodingUnitNames {
		n := logicalDecodingUnitNode(t, e, name)
		if err := n.Validate(); err != nil { t.Fatal(err) }
		if !expr.AdmitsSource(n) || !exec.SourceReturnsSet(n) || exec.PlannedVolatility(n) != expr.Volatile || n.RoutineReference == (expr.Info{}).RoutineReference { t.Fatal("source identity") }
		children := n.Children()
		rebuilt, err := n.Rebuild(n.Info, children)
		if err != nil || !analyze.SameGroupExpression(n, rebuilt) || expr.InfoOf(rebuilt).RoutineReference != n.RoutineReference { t.Fatal("reconstruction identity", err) }
		children[0] = nil
		if n.Args[0] == nil || rebuilt.(functions.LogicalDecoding).Args[0] == nil { t.Fatal("borrowed argument backing") }
		render, err := n.Render()
		if err != nil || render.Name != name || len(render.Args) != 3 { t.Fatal("render", err) }
		captured, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || captured.Count != 3 { t.Fatal("capture", err) }
		for i := range n.Args { child, err := captured.At(n, i); if err != nil || child != n.Args[i] { t.Fatal("written capture", i, err) } }
		bad := n
		bad.Kind = 255
		if bad.Validate() == nil { t.Fatal("invalid kind admitted") }
		bad = n
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		bad.Info.Usage = text.Usage()
		if bad.Validate() == nil { t.Fatal("invalid shape admitted") }
		bad = n
		bad.Slots = []uint8{0,0,2}
		if bad.Validate() == nil { t.Fatal("invalid mapping admitted") }
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "SELECT"
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: logicalDecodingUnitNames[0]}}}, Args: []parser.Expr{null,null,null,null}, ArgNames: []parser.Identifier{{Text:"upto_nchanges"},{Text:"slot_name"},{Text:"upto_lsn"},{Text:"options"}}, Variadic:true}
	bound, err := binder.BindFunctionProvider(raw)
	if err != nil { t.Fatal(err) }
	n := bound.(functions.LogicalDecoding)
	if !reflect.DeepEqual(n.Slots, []uint8{2,0,1,3}) { t.Fatal("written-to-formal mapping", n.Slots) }
	render, err := n.Render()
	if err != nil || !render.Variadic { t.Fatal("named rendering", err) }
	for i, label := range raw.ArgNames { if render.Args[i].Label != label.Text { t.Fatal("render label", i) } }
	captured, _, _ := n.Capture(expr.CaptureFunction)
	for i := range n.Args { child, err := captured.At(n,i); if err != nil || child != n.Args[i] { t.Fatal("named capture order", i, err) } }
}

func TestLogicalDecodingOptionValidation(t *testing.T) {
	e := engine.New()
	name, _ := e.CatalogRead().LookupType(sqltype.Name)
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	array, _ := e.CatalogRead().LookupType(1009)
	slot := sqltype.String(name, "absent")
	makeArray := func(dims []sqltype.ArrayDimension, values ...sqltype.Value) sqltype.Value {
		v, err := sqltype.ArrayValue(array, dims, values)
		if err != nil { t.Fatal(err) }
		return v
	}
	empty := makeArray(nil)
	null := sqltype.Null(text)
	x := sqltype.String(text,"x")
	for _, tc := range []struct{ slot, options sqltype.Value; code, message string }{
		{sqltype.Null(name),sqltype.Null(array),"22004","slot name must not be null"},
		{slot,sqltype.Null(array),"22004","options array must not be null"},
		{slot,makeArray([]sqltype.ArrayDimension{{Lower:1,Length:1},{Lower:1,Length:1}},null),"0A000","array must be one-dimensional"},
		{slot,makeArray([]sqltype.ArrayDimension{{Lower:1,Length:1}},null),"0A000","array must not contain nulls"},
		{slot,makeArray([]sqltype.ArrayDimension{{Lower:1,Length:1}},x),"0A000","array must have even number of elements"},
	} { logicalDecodingUnitError(t,functions.CheckLogicalDecodingOptions(tc.slot,tc.options),tc.code,tc.message) }
	for _, options := range []sqltype.Value{empty,makeArray([]sqltype.ArrayDimension{{Lower:-5,Length:2}},x,x)} {
		if err := functions.CheckLogicalDecodingOptions(slot,options); err != nil { t.Fatal("valid options",err) }
	}
}

func TestLogicalDecodingRuntimeOwnership(t *testing.T) {
	e := engine.New()
	admin := enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,admin,"CREATE ROLE ld_unit_actor")
	n := logicalDecodingUnitNode(t,e,logicalDecodingUnitNames[0])
	// Deliberately reorder the three fixed operands. Their evaluation order is
	// written order, not formal order, including the unused physical limits.
	n.Args = []expr.Expr{n.Args[2],n.Args[0],n.Args[1]}
	n.Slots = []uint8{2,0,1}
	order := []int{}
	for i, arg := range n.Args {
		n.Args[i] = fileListOperand{plan.LiteralExpr{Info:expr.Info{Usage:arg.ResultUsage()},Value:sqltype.Null(arg.ResultUsage().Type)},&order,i}
	}
	run := func(actor string, code, message string) {
		t.Helper()
		session := enginetest.StartSession(t,e,nil)
		if actor != "" { enginetest.SessionSQL(t,session,"SET ROLE "+actor) }
		op, err := session.Acquire(context.Background())
		if err != nil { t.Fatal(err) }
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil { t.Fatal(err) }
		defer scope.Finish(context.Canceled)
		c := &exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
		before := e.CatalogRead().State()
		cursor, err := n.OpenSource(c)
		d := logicalDecodingUnitError(t,err,code,message)
		if code == "42501" && d.Detail != "Only roles with the REPLICATION attribute may use replication slots." { t.Fatal("permission detail",d) }
		if cursor.Type != nil || cursor.State != nil || cursor.Values != nil || before != e.CatalogRead().State() { t.Fatal("failed source published state") }
		canceled,cancel := context.WithCancel(context.Background()); cancel(); c.Context = canceled
		if _,err := n.OpenSource(c); !errors.Is(err,context.Canceled) { t.Fatal("pre-cancellation",err) }
	}
	run("ld_unit_actor","42501","permission denied to use replication slots")
	enginetest.SessionSQL(t,admin,"ALTER ROLE ld_unit_actor REPLICATION")
	run("ld_unit_actor","22004","slot name must not be null")
	enginetest.SessionSQL(t,admin,"ALTER ROLE ld_unit_actor NOREPLICATION")
	run("ld_unit_actor","42501","permission denied to use replication slots")
	run("","22004","slot name must not be null")
	if !reflect.DeepEqual(order,[]int{0,1,2,0,1,2,0,1,2,0,1,2}) { t.Fatal("operand occurrence order",order) }
}
