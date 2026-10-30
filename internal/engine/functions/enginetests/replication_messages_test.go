package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestReplicationMessagesBinding(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		call      string
		oid       ids.OID
		signature string
		kind      functions.ReplicationMessagesKind
		arity     uint8
		result    sqltype.ID
	}{
		{"pg_logical_emit_message(true,'p','x')", 3577, "16,25,25,16", functions.ReplicationMessageText, 3, sqltype.PgLSN},
		{`pg_logical_emit_message(false,'p','\x00ff'::bytea,true)`, 3578, "16,25,17,16", functions.ReplicationMessageBinary, 4, sqltype.PgLSN},
		{"pg_sync_replication_slots()", 6344, "", functions.ReplicationSyncSlots, 0, sqltype.Void},
	} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.call)
		n := node.(functions.ReplicationMessages)
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || d == nil || d.Address().ObjectID != tc.oid || string(d.Signature) != tc.signature || d.Result != ids.OID(tc.result) || d.ReturnsSet() || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 'u' || d.ACL.Explicit {
			t.Fatal("released routine", d, err)
		}
		if n.Kind != tc.kind || n.Arity != tc.arity || n.Validate() != nil || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.IntrinsicVolatility() != expr.Volatile {
			t.Fatal("typed immutable recipe", n)
		}
		if scalar, set := n.SourceAdmission(); !scalar || set {
			t.Fatal("scalar admission")
		}
		before := n
		_, err = n.Eval(&evaluation)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("physical boundary", err)
		}
		cursor, err := n.OpenSource(&evaluation)
		if err == nil || cursor.Type != nil || cursor.State != nil {
			t.Fatal("published unavailable physical result", cursor, err)
		}
		if !reflect.DeepEqual(before, n) {
			t.Fatal("retained live state")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		evaluation.Context = ctx
		if _, err := n.Eval(&evaluation); err != context.Canceled {
			t.Fatal("context hidden", err)
		}
	}
}
func TestReplicationMessagesProtocol(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, _ := strictNullUnitBind(t, e, op, scope, "pg_logical_emit_message(message=>'x',prefix=>'p',transactional=>false,flush=>true)")
	n := node.(functions.ReplicationMessages)
	before := n
	if n.Slots != [4]uint8{2, 1, 0, 3} {
		t.Fatal("written formal map", n.Slots)
	}
	children := n.Children()
	children[0] = nil
	if n.Args[0] == nil {
		t.Fatal("borrowed backing escaped")
	}
	rebuilt, err := n.Rebuild(n.Info, n.Children())
	if err != nil {
		t.Fatal(err)
	}
	next := rebuilt.(functions.ReplicationMessages)
	if next.RoutineReference != n.RoutineReference || next.Occurrence != n.Occurrence || next.Args != n.Args || next.Slots != n.Slots {
		t.Fatal("provenance changed")
	}
	rendering, err := next.Render()
	if err != nil {
		t.Fatal(err)
	}
	for i, label := range []string{"message", "prefix", "transactional", "flush"} {
		if rendering.Args[i].Label != label {
			t.Fatal("render mapping", rendering)
		}
	}
	capture, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 4 || !capture.Indexed {
		t.Fatal(capture, err)
	}
	for i := range 4 {
		child, err := n.CaptureOperand(i)
		if err != nil || child != n.Args[i] {
			t.Fatal("capture order", i, err)
		}
	}
	for _, change := range []string{"kind", "result", "arity", "nil", "type", "duplicate", "range", "required"} {
		bad := n
		switch change {
		case "kind":
			bad.Kind = 255
		case "result":
			bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
		case "arity":
			bad.Arity = 5
		case "nil":
			bad.Args[0] = nil
		case "type":
			bad.Args[0] = bad.Args[2]
		case "duplicate":
			bad.Slots[1] = bad.Slots[0]
		case "range":
			bad.Slots[0] = 4
		case "required":
			bad.Arity = 2
		}
		if bad.Validate() == nil {
			t.Fatal("accepted malformed recipe", change)
		}
	}
	if _, err := n.Rebuild(n.Info, nil); err == nil {
		t.Fatal("accepted reconstruction arity")
	}
	children = n.Children()
	children[0] = nil
	if _, err := n.Substitute(n.Info, children); err == nil {
		t.Fatal("accepted nil child")
	}
	children = n.Children()
	children[0] = n.Args[2]
	if _, err := n.Rebuild(n.Info, children); err == nil {
		t.Fatal("accepted incompatible typed child")
	}
	info := n.Info
	info.Occurrence++
	replacement, err := n.Substitute(info, n.Children())
	if err != nil {
		t.Fatal(err)
	}
	owned := replacement.(functions.ReplicationMessages)
	if owned.Occurrence != info.Occurrence || owned.RoutineReference != n.RoutineReference || owned.Args != n.Args || owned.Slots != n.Slots {
		t.Fatal("replacement provenance or operands lost")
	}
	badInfo := n.Info
	badInfo.Usage.Type = nil
	if _, err := n.Rebuild(badInfo, n.Children()); err == nil {
		t.Fatal("accepted result retag")
	}
	if !reflect.DeepEqual(before, n) {
		t.Fatal("reconstruction mutated owner")
	}
	omitted, _, _ := strictNullUnitBind(t, e, op, scope, "pg_logical_emit_message(true,'p','x')")
	explicit, _, _ := strictNullUnitBind(t, e, op, scope, "pg_logical_emit_message(true,'p','x',false)")
	binary, _, _ := strictNullUnitBind(t, e, op, scope, `pg_logical_emit_message(true,'p','\x78'::bytea)`)
	same := func(a, b expr.Expr) bool { return true }
	if omitted.(functions.ReplicationMessages).SameGroup(explicit, same, false) || omitted.(functions.ReplicationMessages).SameGroup(binary, same, false) {
		t.Fatal("distinct overload/default groups merged")
	}
	bad := omitted.(functions.ReplicationMessages)
	bad.Args[3] = n.Args[3]
	if bad.Validate() == nil {
		t.Fatal("accepted unused storage")
	}
}
func TestReplicationMessagesEvaluation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, call := range []string{"pg_logical_emit_message(NULL,'p','x')", "pg_logical_emit_message(true,NULL,'x')", "pg_logical_emit_message(true,'p',NULL::text)", "pg_logical_emit_message(true,'p',NULL::bytea)", "pg_logical_emit_message(true,'p','x',NULL)"} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.ReplicationMessages)
		value, err := n.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal("strict typed NULL", value, err)
		}
		cursor, err := n.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := cursor.Next(context.Background())
		if err != nil || !present || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal(value, present, err)
		}
		_, present, err = cursor.Next(context.Background())
		if err != nil || present {
			t.Fatal("scalar cardinality", present, err)
		}
		cursor.Close()
	}
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_logical_emit_message(NULL,'p',nextval('strict_null_steps'::regclass)::text)")
	for iteration := int64(1); iteration <= 2; iteration++ {
		value, err := node.Eval(&evaluation)
		if err != nil || !value.IsNull() {
			t.Fatal(value, err)
		}
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err = current.Eval(&ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Integer()
		if err != nil || got != iteration {
			t.Fatal("all children exactly once", got, err)
		}
	}
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "pg_logical_emit_message(NULL,'p',(1/$1::integer)::text)")
	zero, err := binder.Params.Parameters[1].Type.Input(context.Background(), "0", -1)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.Parameters = []sqltype.Value{zero}
	_, err = node.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal("NULL swallowed later child failure", err)
	}
	t.Run("written-order", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_logical_emit_message(message=>nextval('strict_null_steps'::regclass)::text,prefix=>(1/(nextval('strict_null_steps'::regclass)-1))::text,transactional=>false)")
		_, err := node.Eval(&evaluation)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("formal order replaced written order", err)
		}
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err := current.Eval(&ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Integer()
		if err != nil || got != 2 {
			t.Fatal("written occurrences", got, err)
		}
	})
}
