package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var walPositionUnitCalls = []string{"pg_current_wal_flush_lsn()", "pg_current_wal_insert_lsn()", "pg_current_wal_lsn()", "pg_switch_wal()", "pg_create_restore_point('unit_marker')", "pg_log_standby_snapshot()"}
var walPositionUnitOIDs = []ids.OID{3330, 2852, 2849, 2848, 3098, 6305}

func TestWALPositionsTypedRecipes(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for i, call := range walPositionUnitCalls {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.WALPosition)
		d, err := exec.ExpressionRoutineDescriptor(n)
		signature, count := "", 0
		if i == 4 { signature, count = "25", 1 }
		if err != nil || d == nil || d.Address().ObjectID != walPositionUnitOIDs[i] || string(d.Signature) != signature || d.Result != 3220 || d.ReturnsSet() || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 's' { t.Fatal("selected declaration", d, err) }
		if n.Kind != functions.WALPositionKind(i) || n.Validate() != nil || n.Usage.Type.ID() != sqltype.PgLSN || len(n.Children()) != count || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.IntrinsicVolatility() != expr.Volatile { t.Fatal("typed immutable volatile recipe", n) }
		if admitted, set := n.SourceAdmission(); !admitted || set { t.Fatal("scalar admission") }
		before := n
		for range 2 {
			value, err := n.Eval(&evaluation)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != n.Kind.Name()+" physical WAL access is unsupported in embedded mode" || diagnostic.Position != "" || value.Type() != nil { t.Fatal("physical boundary", value, err) }
			cursor, err := n.OpenSource(&evaluation)
			if err == nil || cursor.Type != nil || cursor.State != nil { t.Fatal("published fake WAL source", cursor, err) }
		}
		if !reflect.DeepEqual(before, n) { t.Fatal("runtime state retained") }
		ctx, cancel := context.WithCancel(context.Background()); cancel()
		evaluation.Context = ctx
		if _, err := n.Eval(&evaluation); err != context.Canceled { t.Fatal("context hidden", err) }
	}
	for _, text := range []string{"", strings.Repeat("a",63), strings.Repeat("é",31)+"a", strings.Repeat("a",64), strings.Repeat("é",32)} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_create_restore_point('"+text+"')")
		_, err := node.Eval(&evaluation)
		var diagnostic *sqlstate.Error
		code := "0A000"
		if len(text) >= 64 { code = "22023" }
		if !errors.As(err, &diagnostic) || diagnostic.Code != code { t.Fatal("byte limit", len(text), err) }
		if code == "22023" && diagnostic.Message != "value too long for restore point (maximum 63 characters)" { t.Fatal(err) }
	}
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_create_restore_point(NULL::text)")
	n := node.(functions.WALPosition)
	value, err := n.Eval(&evaluation)
	if err != nil || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("descriptor-owned NULL", value, err) }
	cursor, err := n.OpenSource(&evaluation)
	if err != nil || cursor.Type != n.Usage.Type { t.Fatal(cursor, err) }
	defer cursor.Close()
	value, present, err := cursor.Next(context.Background())
	if err != nil || !present || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("scalar NULL", value, present, err) }
	_, present, err = cursor.Next(context.Background())
	if err != nil || present { t.Fatal("scalar repeated", present, err) }
	errorNode, binder, evaluation := strictNullUnitBind(t, e, op, scope, "pg_create_restore_point((1/$1::integer)::text)")
	zero, err := binder.Params.Parameters[1].Type.Input(context.Background(), "0", -1)
	if err != nil { t.Fatal(err) }
	evaluation.Parameters = []sqltype.Value{zero}
	_, err = errorNode.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" { t.Fatal("child failure swallowed", err) }
	sequence, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_create_restore_point(nextval('strict_null_steps'::regclass)::text)")
	for want := int64(1); want <= 2; want++ {
		sequence.Eval(&evaluation)
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err := current.Eval(&ctx)
		if err != nil { t.Fatal(err) }
		got, err := value.Integer()
		if err != nil || got != want { t.Fatal("child executes once", got, err) }
	}
}

func TestWALPositionsRebuildValidation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, _ := strictNullUnitBind(t, e, op, scope, "pg_create_restore_point('x')")
	n := node.(functions.WALPosition)
	before := n
	children := n.Children(); children[0] = nil
	if n.Arg == nil { t.Fatal("writable backing escaped") }
	info := n.Info; info.Occurrence++
	rebuilt, err := n.Rebuild(info,n.Children())
	if err != nil { t.Fatal(err) }
	next := rebuilt.(functions.WALPosition)
	if next.RoutineReference != n.RoutineReference || next.Occurrence != info.Occurrence || next.Arg != n.Arg || next.Usage != n.Usage || !reflect.DeepEqual(n,before) { t.Fatal("owned provenance changed") }
	rendering, err := next.Render()
	if err != nil || rendering.Name != "pg_create_restore_point" || len(rendering.Args) != 1 || rendering.Args[0].Label != "" { t.Fatal(rendering,err) }
	captured, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || captured.Count != 1 || !captured.Indexed { t.Fatal(captured,err) }
	if _,ok,err := n.Capture(expr.CaptureKind(255)); err != nil || ok { t.Fatal("non-function capture",ok,err) }
	if !n.SameGroup(n,func(a,b expr.Expr) bool { return a==b },false) { t.Fatal("equal recipe grouping") }
	if n.SameGroup(functions.WALPosition{},func(a,b expr.Expr) bool { return a==b },false) { t.Fatal("different recipe grouping") }
	child, err := n.CaptureOperand(0)
	if err != nil || child != n.Arg { t.Fatal(child,err) }
	for _, index := range []int{-1,1} { if _,err := n.CaptureOperand(index); err == nil { t.Fatal("capture index accepted") } }
	wrong, _, _ := strictNullUnitBind(t,e,op,scope,"pg_current_wal_lsn()")
	for _, change := range []string{"kind","result","usage","nil","type","unused"} {
		bad := n
		switch change {
		case "kind": bad.Kind = 255
		case "result": bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
		case "usage": bad.Usage.Typmod = 4
		case "nil": bad.Arg = nil
		case "type": bad.Arg = wrong
		case "unused": bad.Kind = functions.WALCurrentWrite
		}
		if bad.Validate() == nil { t.Fatal("accepted malformed recipe",change) }
	}
	badInfo := n.Info; badInfo.Usage.Type = nil
	if _,err := n.Rebuild(badInfo,n.Children()); err == nil { t.Fatal("nil result descriptor") }
	zero := wrong.(functions.WALPosition)
	if len(zero.Children())!=0 { t.Fatal("zero-input children") }
	if _,err := zero.Rebuild(zero.Info,[]expr.Expr{n.Arg}); err==nil { t.Fatal("zero-input reconstruction accepted operand") }
	if _,err := zero.CaptureOperand(0); err==nil { t.Fatal("zero-input capture accepted") }
	if _,err := n.Rebuild(n.Info,nil); err == nil { t.Fatal("wrong arity") }
	if _,err := n.Rebuild(n.Info,[]expr.Expr{nil}); err == nil { t.Fatal("nil child") }
	if _,err := n.Rebuild(n.Info,[]expr.Expr{n.Arg,n.Arg}); err == nil { t.Fatal("extra child") }
	if !reflect.DeepEqual(n,before) { t.Fatal("rejection mutated original") }
}

func TestWALPositionsCatalogSeeds(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	if got := slices.Collect(routine.RestrictedWALPositionRoutines()); !slices.Equal(got,[]ids.OID{2848,3098,6305}) { t.Fatal("restricted roster",got) }
	for i, call := range walPositionUnitCalls {
		node, _, _ := strictNullUnitBind(t,e,op,scope,call)
		d, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil { t.Fatal(err) }
		restricted := i >= 3
		if d.ACL.Explicit != restricted { t.Fatal("raw seed",d.ACL) }
		if restricted {
			want := authorization.ACLEntry{Grantor:d.Owner,Grantee:authorization.ACLGrantee{Role:d.Owner},Privileges:authorization.ExecutePrivilege}
			if !slices.Equal(d.ACL.Entries,[]authorization.ACLEntry{want}) { t.Fatal("owner seed",d.ACL) }
		}
		var initial *catalog.InitialPrivilegeRecord
		for address, record := range e.CatalogRead().State().InitialPrivileges() { if address == d.Address() { initial = record } }
		if restricted { if initial == nil || initial.PrivType != 'i' || !reflect.DeepEqual(initial.ACL,d.ACL) { t.Fatal("historical seed",initial) } } else if initial != nil { t.Fatal("default PUBLIC has no initial row") }
		if d.Builtin.Defaults != 0 || d.Builtin.Variadic != 0 || d.InputNames.Len() != 0 { t.Fatal("no names/defaults/variadic",d) }
	}
}
