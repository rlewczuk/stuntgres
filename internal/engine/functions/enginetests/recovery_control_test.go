package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
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

var recoveryControlUnitCalls = []string{"pg_is_wal_replay_paused()", "pg_get_wal_replay_pause_state()", "pg_promote()", "pg_wal_replay_pause()", "pg_wal_replay_resume()"}
var recoveryControlUnitOIDs = []ids.OID{3073, 1137, 3436, 3071, 3072}
var recoveryControlUnitTypes = []sqltype.ID{sqltype.Bool, sqltype.Text, sqltype.Bool, sqltype.Void, sqltype.Void}

func TestRecoveryControlTypedRecipes(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for i, call := range recoveryControlUnitCalls {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.RecoveryControl)
		d, err := exec.ExpressionRoutineDescriptor(n)
		signature := ""
		if i == 2 { signature = "16,23" }
		if err != nil || d == nil || d.Address().ObjectID != recoveryControlUnitOIDs[i] || string(d.Signature) != signature || d.Result != ids.OID(recoveryControlUnitTypes[i]) || d.ReturnsSet() || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 's' { t.Fatal("released typed routine", d, err) }
		if n.Kind != functions.RecoveryControlKind(i) || n.Validate() != nil || n.Usage.Type.ID() != recoveryControlUnitTypes[i] || len(n.Children()) != 0 || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.IntrinsicVolatility() != expr.Volatile { t.Fatal("immutable volatile recipe", n) }
		if admitted, set := n.SourceAdmission(); !admitted || set { t.Fatal("scalar admission") }
		before := n
		for range 2 {
			_, err := n.Eval(&evaluation)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != "recovery is not in progress" || diagnostic.Hint != "Recovery control functions can only be executed during recovery." || diagnostic.Position != "" { t.Fatal("primary precondition", err) }
			cursor, err := n.OpenSource(&evaluation)
			if err == nil || cursor.Type != nil || cursor.State != nil { t.Fatal("published fake recovery source", cursor, err) }
		}
		if !reflect.DeepEqual(before, n) { t.Fatal("runtime state retained") }
		ctx, cancel := context.WithCancel(context.Background()); cancel()
		evaluation.Context = ctx
		if _, err := n.Eval(&evaluation); err != context.Canceled { t.Fatal("context hidden", err) }
	}
	for _, call := range []string{"pg_promote(NULL)", "pg_promote(true,NULL)", "pg_promote(wait_seconds=>NULL)", "pg_promote(NULL,0)"} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.RecoveryControl)
		value, err := n.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("explicit NULL is not omitted default", value, err) }
		cursor, err := n.OpenSource(&evaluation)
		if err != nil || cursor.Type != n.Usage.Type { t.Fatal(cursor, err) }
		value, present, err := cursor.Next(context.Background())
		if err != nil || !present || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("typed scalar NULL source", value, present, err) }
		_, present, err = cursor.Next(context.Background())
		if err != nil || present { t.Fatal("scalar repeated", present, err) }
		cursor.Close()
	}
	// Genuine sequence providers prove all supplied children are evaluated even
	// when a prior operand is NULL; no synthetic echo expression is involved.
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_promote(NULL,nextval('strict_null_steps'::regclass)::integer)")
	for iteration := int64(1); iteration <= 2; iteration++ {
		value, err := node.Eval(&evaluation)
		if err != nil || !value.IsNull() { t.Fatal(value, err) }
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err = current.Eval(&ctx)
		if err != nil { t.Fatal(err) }
		got, err := value.Integer()
		if err != nil || got != iteration { t.Fatal("supplied operand must execute exactly once", got, err) }
	}
	errorNode, binder, errorEvaluation := strictNullUnitBind(t, e, op, scope, "pg_promote(NULL,1/$1::integer)")
	zero, err := binder.Params.Parameters[1].Type.Input(context.Background(), "0", -1)
	if err != nil { t.Fatal(err) }
	errorEvaluation.Parameters = []sqltype.Value{zero}
	_, err = errorNode.Eval(&errorEvaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" { t.Fatal("child error swallowed", err) }
	t.Run("written-order", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_promote(wait_seconds=>nextval('strict_null_steps'::regclass)::integer,wait=>1/(nextval('strict_null_steps'::regclass)-1)=0)")
		_, err := node.Eval(&evaluation)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" { t.Fatal("formal order replaced written order", err) }
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err := current.Eval(&ctx)
		if err != nil { t.Fatal(err) }
		got, err := value.Integer()
		if err != nil || got != 2 { t.Fatal("written children did not execute once", got, err) }
	})
}

func TestRecoveryControlRebuildValidation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, _ := strictNullUnitBind(t, e, op, scope, "pg_promote(wait_seconds=>1,wait=>false)")
	n := node.(functions.RecoveryControl)
	if n.Arity != 2 || n.Slots != [2]uint8{1, 0} { t.Fatal("source/formal mapping", n) }
	before := n
	children := n.Children(); children[0] = nil
	if n.Args[0] == nil { t.Fatal("borrowed storage escaped") }
	info := n.Info; info.Occurrence++
	rebuilt, err := n.Rebuild(info, n.Children())
	if err != nil { t.Fatal(err) }
	next := rebuilt.(functions.RecoveryControl)
	if next.RoutineReference != n.RoutineReference || next.Occurrence != info.Occurrence || next.Usage != n.Usage || next.Args != n.Args || next.Slots != n.Slots || !reflect.DeepEqual(n, before) { t.Fatal("rebuild changed authority or original") }
	rendering, err := next.Render()
	if err != nil || rendering.Args[0].Label != "wait_seconds" || rendering.Args[1].Label != "wait" { t.Fatal("named destinations lost", rendering, err) }
	captured, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || captured.Count != 2 || !captured.Indexed { t.Fatal("capture", captured, err) }
	for i := range 2 { child, err := n.CaptureOperand(i); if err != nil || child != n.Args[i] { t.Fatal("source-order capture", i, err) } }
	for _, change := range []string{"kind", "result", "arity", "nil", "type", "duplicate", "range", "unused", "zero"} {
		bad := n
		switch change {
		case "kind": bad.Kind = 255
		case "result": bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
		case "arity": bad.Arity = 3
		case "nil": bad.Args[0] = nil
		case "type": bad.Args[0] = bad.Args[1]
		case "duplicate": bad.Slots[1] = bad.Slots[0]
		case "range": bad.Slots[0] = 2
		case "unused": bad.Arity = 1
		case "zero": bad.Kind = functions.RecoveryReplayPaused
		}
		if bad.Validate() == nil { t.Fatal("accepted malformed carrier", change) }
	}
	if _, err := n.Rebuild(n.Info, nil); err == nil { t.Fatal("accepted wrong arity") }
	children = n.Children(); children[0] = nil
	if _, err := n.Rebuild(n.Info, children); err == nil { t.Fatal("accepted nil child") }
	badInfo := n.Info; badInfo.Usage.Type = nil
	if _, err := n.Rebuild(badInfo, n.Children()); err == nil { t.Fatal("accepted invalid result") }
	if !reflect.DeepEqual(n, before) { t.Fatal("rejection mutated original") }
	node, _, _ = strictNullUnitBind(t, e, op, scope, "pg_promote(wait_seconds=>NULL)")
	omitted := node.(functions.RecoveryControl)
	rendering, err = omitted.Render()
	if err != nil || omitted.Arity != 1 || omitted.Slots[0] != 1 || rendering.Args[0].Label != "wait_seconds" { t.Fatal("omission became positional wait", rendering, err) }
}

func TestRecoveryControlCatalogSeeds(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	if got := slices.Collect(routine.RestrictedRecoveryControlRoutines()); !slices.Equal(got, []ids.OID{3071, 3072, 3436}) { t.Fatal("restricted roster", got) }
	for i, call := range recoveryControlUnitCalls {
		node, _, _ := strictNullUnitBind(t, e, op, scope, call)
		d, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil { t.Fatal(err) }
		restricted := i >= 2
		if d.ACL.Explicit != restricted { t.Fatal("seed ACL presence", d.ACL) }
		if restricted {
			want := authorization.ACLEntry{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: d.Owner}, Privileges: authorization.ExecutePrivilege}
			if !slices.Equal(d.ACL.Entries, []authorization.ACLEntry{want}) { t.Fatal("owner-only mutable seed", d.ACL) }
		}
		var initial *catalog.InitialPrivilegeRecord
		for address, record := range e.CatalogRead().State().InitialPrivileges() { if address == d.Address() { initial = record } }
		if restricted { if initial == nil || initial.PrivType != 'i' || !reflect.DeepEqual(initial.ACL, d.ACL) { t.Fatal("initial privilege capture", initial) } } else if initial != nil { t.Fatal("PUBLIC default has no initial row") }
		defaults := uint8(0)
		if i == 2 { defaults = 2 }
		if d.Builtin.Defaults != defaults || d.Builtin.Variadic != 0 { t.Fatal("released default count/nonvariadic identity", d.Builtin) }
		if i == 2 {
			if d.LiteralDefaults.Len() != 2 || d.InputNames.Len() != 2 { t.Fatal("full defaulted signature") }
			if d.InputNames.At(0) != "wait" || d.InputNames.At(1) != "wait_seconds" { t.Fatal("released formal labels") }
			for j, want := range []catalog.RoutineLiteral{{SourceType: 16, Text: "true"}, {SourceType: 23, Text: "60"}} { if d.LiteralDefaults.At(j) != want { t.Fatal("released default", d.LiteralDefaults.At(j)) } }
		}
	}
}
