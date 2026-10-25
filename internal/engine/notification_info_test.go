package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestNotificationInfoBinding(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		name       string
		oid        uint32
		result     sqltype.ID
		set        bool
		volatility expr.Volatility
	}{
		{"pg_listening_channels", 3035, sqltype.Text, true, expr.Stable},
		{"pg_notification_queue_usage", 3296, sqltype.Float8, false, expr.Volatile},
	} {
		node := catalogRelationsUnitBind(t, e, tc.name)
		info := expr.InfoOf(node)
		if info.RoutineReference == nil || info.RoutineReference.RoutineOID() != tc.oid || node.ResultUsage().Type.ID() != tc.result || exec.SourceReturnsSet(node) != tc.set || !expr.AdmitsSource(node) {
			t.Fatal("typed routine identity", tc.name, info)
		}
		if exec.PlannedVolatility(node) != tc.volatility || plan.GenerationImmutable(node) || exec.ConditionalConstantInputs(node) {
			t.Fatal("inquiry folded or lost volatility")
		}
		composite := node.(expr.Composite)
		if len(composite.Children()) != 0 {
			t.Fatal("inquiry retained children")
		}
		rebuilt, err := composite.Rebuild(info, nil)
		if err != nil || !analyze.SameGroupExpression(node, rebuilt) {
			t.Fatal("rebuild/grouping", err)
		}
		if _, err := composite.Rebuild(info, []expr.Expr{node}); err == nil {
			t.Fatal("child admitted")
		}
		mapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !analyze.SameGroupExpression(node, mapped) {
			t.Fatal("remap", err)
		}
		rendered, err := node.(interface {
			Render() (expr.Rendering, error)
		}).Render()
		if err != nil || rendered.Form != expr.FormCall || rendered.Name != tc.name || len(rendered.Args) != 0 {
			t.Fatal("render", rendered, err)
		}
		capture, admitted, err := node.(expr.Capturable).Capture(expr.CaptureFunction)
		if err != nil || !admitted || capture.Count != 0 {
			t.Fatal("zero-operand capture", capture, admitted, err)
		}
		found := false
		for _, decl := range routine.BuiltinRoutineDeclarations {
			if uint32(decl.Identity.ID) == tc.oid {
				found = true
				if !decl.Strict || decl.Volatility != tc.volatility || decl.Parallel != 'r' || decl.ReturnsSet != tc.set {
					t.Fatal("release metadata", decl)
				}
			}
		}
		if !found {
			t.Fatal("missing callable declaration")
		}
	}
}

func TestNotificationInfoSource(t *testing.T) {
	e := New()
	scope := tablespaceInfoUnitScope(t, e)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: HostOf(scope)}}
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	floating, _ := e.CatalogRead().LookupType(sqltype.Float8)
	node := functions.ListeningChannels{Info: expr.Info{Usage: text.Usage()}}
	empty, err := node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	v, present, err := empty.Next(context.Background())
	if err != nil || present || !v.IsNull() || v.Type() != text || empty.Values != nil {
		t.Fatal("typed empty EOF", v, present, err)
	}
	listener := PayloadNotificationsRef(PayloadOf(scope.Op.Session))
	e.Mu.Lock()
	listener.State = &session.NotificationState{Channels: map[string]struct{}{"committed": {}}, History: true}
	e.Mu.Unlock()
	first, err := node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	e.Mu.Lock()
	listener.State = &session.NotificationState{Channels: map[string]struct{}{"later": {}}, History: true}
	e.Mu.Unlock()
	second, err := node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, tc := range []struct {
		cursor *expr.SourceCursor
		want   string
	}{{&first, "committed"}, {&second, "later"}} {
		v, present, err := tc.cursor.Next(context.Background())
		textValue, conversion := v.Text()
		if err != nil || conversion != nil || !present || v.IsNull() || textValue != tc.want {
			t.Fatal("invocation snapshot", v, present, err, conversion)
		}
		_, present, err = tc.cursor.Next(context.Background())
		if err != nil || present || tc.cursor.Values != nil {
			t.Fatal("EOF retained vector", err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cursor, err := node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cursor.Next(cancelled); !errors.Is(err, context.Canceled) || cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("cancel retained vector", err)
	}
	cursor, err = node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	cursor.Close()
	if cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("close retained vector")
	}
	if _, err := node.Eval(c); err == nil {
		t.Fatal("set evaluated as scalar")
	}
	usage := functions.NotificationQueueUsage{Info: expr.Info{Usage: floating.Usage()}}
	value, err := usage.Eval(c)
	if err != nil || value.IsNull() {
		t.Fatal(value, err)
	}
	got, err := value.Float()
	if err != nil || got != 0 {
		t.Fatal(got, err)
	}
}
