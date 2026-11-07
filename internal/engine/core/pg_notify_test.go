package core_test

import (
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGNotifyBinding(t *testing.T) {
	e := engine.New()
	node := catalogRelationsUnitBind(t, e, "pg_notify",
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "chan"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "payload"}})
	call, ok := node.(functions.PgNotify)
	info := expr.InfoOf(node)
	if !ok || info.RoutineReference == nil || info.RoutineReference.RoutineOID() != 3036 || node.ResultUsage().Type.ID() != sqltype.Void || exec.PlannedVolatility(node) != expr.Volatile || plan.GenerationImmutable(node) || exec.ConditionalConstantInputs(node) {
		t.Fatal("genuine volatile void provider", node, info)
	}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	children := call.Children()
	if len(children) != 2 || children[0].ResultUsage().Type.ID() != sqltype.Text || children[1].ResultUsage().Type.ID() != sqltype.Text {
		t.Fatal("TEXT children", children)
	}
	rebuilt, err := call.Rebuild(info, children)
	if err != nil || !analyze.SameGroupExpression(node, rebuilt) {
		t.Fatal("rebuild", rebuilt, err)
	}
	mapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !analyze.SameGroupExpression(node, mapped) {
		t.Fatal("stored remap", mapped, err)
	}

	declaration, err := routine.LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, "pg_notify", []sqltype.ID{sqltype.Text, sqltype.Text})
	if err != nil {
		t.Fatal(err)
	}
	if declaration.Identity.ID != 3036 || declaration.Kind != catalog.FunctionRoutine || declaration.Result != 2278 || declaration.Strict || declaration.Volatility != expr.Volatile || declaration.Parallel != 'r' || declaration.ReturnsSet || declaration.Variadic != 0 || declaration.Defaults != 0 || declaration.Providers != functions.CatalogRoutineProvider || declaration.Outputs != nil {
		t.Fatalf("declaration %#v", declaration)
	}

	procedures := routine.BuiltinProcedures(authorization.PrincipalRef{})
	descriptor := -1
	for i := range procedures {
		if procedures[i].RoutineOID() == 3036 {
			descriptor = i
			break
		}
	}
	if descriptor < 0 {
		t.Fatal("missing seeded pg_notify procedure")
	}
	procedure := &procedures[descriptor]
	if procedure.InputNames != nil || procedure.Builtin == nil || procedure.Builtin.Outputs != nil || procedure.CostEstimate() != 1 || procedure.Display.Source != "pg_notify" || procedure.Display.Language != catalog.InternalRoutineLanguage {
		t.Fatalf("procedure %#v", procedure)
	}
}

func TestPGNotifyStatementQueue(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	host := core.HostOf(scope)
	pid := uint64(op.Session.SessionToken.ID())

	for _, c := range []struct{ name, channel, payload, message string }{
		{"empty-channel", "", "p", "channel name cannot be empty"},
		{"long-channel", strings.Repeat("a", 64), "p", "channel name too long"},
		{"long-payload", "chan", strings.Repeat("a", 8000), "payload string too long"},
	} {
		core.SetScopeNotificationEvents(scope, nil)
		err := host.QueueNotification(c.channel, c.payload)
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != "22023" || diagnostic.Message != c.message || diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" || len(core.ScopeNotificationEvents(scope)) != 0 {
			t.Fatal(c.name, err, core.ScopeNotificationEvents(scope))
		}
	}

	for _, c := range []struct{ name, channel, payload string }{
		{"channel-63", strings.Repeat("a", 63), "p"},
		{"payload-7999", "chan", strings.Repeat("a", 7999)},
		{"empty-payload", "chan", ""},
	} {
		core.SetScopeNotificationEvents(scope, nil)
		if err := host.QueueNotification(c.channel, c.payload); err != nil {
			t.Fatal(c.name, err)
		}
		want := session.Notification{PID: pid, Channel: c.channel, Payload: c.payload}
		if events := core.ScopeNotificationEvents(scope); len(events) != 1 || events[0] != want {
			t.Fatal(c.name, events, want)
		}
	}

	core.SetScopeNotificationEvents(scope, nil)
	for range 2 {
		if err := host.QueueNotification("dup", "same"); err != nil {
			t.Fatal(err)
		}
	}
	events := core.ScopeNotificationEvents(scope)
	if len(events) != 2 {
		t.Fatal("statement events", events)
	}
	_, merged := scope.Owner.MergeNotifications(nil, events)
	if len(merged) != 1 || merged[0] != (session.Notification{PID: pid, Channel: "dup", Payload: "same"}) {
		t.Fatal("transaction fold", merged)
	}

	core.SetScopeNotificationEvents(scope, nil)
	result, err := scope.Execute(enginetest.Statement(t, "NOTIFY chan, 'payload'"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	if events := core.ScopeNotificationEvents(scope); len(events) != 1 || events[0] != (session.Notification{PID: pid, Channel: "chan", Payload: "payload"}) {
		t.Fatal("NOTIFY effect", events)
	}
}
