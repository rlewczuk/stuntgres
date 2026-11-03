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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestBackendMemoryRuntimeBindingAndSource(t *testing.T) {
	e := engine.New()
	node := catalogRelationsUnitBind(t, e, "pg_get_backend_memory_contexts")
	n, ok := node.(functions.BackendMemoryContexts)
	info := expr.InfoOf(node)
	if !ok || info.RoutineReference == nil || info.RoutineReference.RoutineOID() != 2282 || !exec.SourceReturnsSet(node) || !expr.AdmitsSource(node) || exec.PlannedVolatility(node) != expr.Volatile || plan.GenerationImmutable(node) || exec.ConditionalConstantInputs(node) {
		t.Fatal("genuine volatile callable provider", node, info)
	}
	if err := n.Validate(); err != nil { t.Fatal(err) }
	if len(n.Children()) != 0 { t.Fatal("session authority retained as a child") }
	if _, err := n.Rebuild(info, []expr.Expr{node}); err == nil { t.Fatal("unexpected operand") }
	rebuilt, err := n.Rebuild(info, nil)
	if err != nil || !analyze.SameGroupExpression(node, rebuilt) { t.Fatal("rebuild/group", rebuilt, err) }
	mapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !analyze.SameGroupExpression(node, mapped) { t.Fatal("stored remap", mapped, err) }
	rendered, err := n.Render()
	if err != nil || rendered.Form != expr.FormCall || rendered.Name != "pg_get_backend_memory_contexts" || len(rendered.Args) != 0 { t.Fatal("render", rendered, err) }
	scope := tablespaceInfoUnitScope(t, e)
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	first, err := n.OpenSource(evaluation)
	if err != nil || len(first.Values) != 3 { t.Fatal("real owner source", first, err) }
	defer first.Close()
	registry := core.PayloadOf(scope.Op.Session).PreparedSQL
	registry.Install("backend_memory_private", &core.PreparedDefinition{})
	second, err := n.OpenSource(evaluation)
	if err != nil || len(second.Values) != 3 { t.Fatal("fresh real owner source", second, err) }
	defer second.Close()
	oldDefinition := first.Values[2].CompositeField(9)
	newDefinition := second.Values[2].CompositeField(9)
	oldBytes, _ := oldDefinition.Integer(); newBytes, _ := newDefinition.Integer()
	if oldBytes != 0 || newBytes <= oldBytes { t.Fatal("owner snapshot reused", oldBytes, newBytes) }
	registry.Remove("backend_memory_private")
	// Cancellation after an emitted root discards every still-private source
	// value. The already detached root datum remains immutable and typed.
	root, present, err := second.Next(context.Background())
	if err != nil || !present || root.Type() != n.ResultUsage().Type { t.Fatal(root, present, err) }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	value, present, err := second.Next(ctx)
	if !errors.Is(err, context.Canceled) || present || value.Type() != nil || second.Values != nil || !second.Exhausted { t.Fatal("canceled source exposed prefix", value, present, err, second) }
	rootName := root.CompositeField(0); name, _ := rootName.Text()
	if name != "TopMemoryContext" { t.Fatal("retained datum changed", name) }
	// Rebuilding into an independent destination rehomes every array/composite
	// descriptor; neither the old expression nor its rows supply current types.
	destination := engine.New()
	destinationNode := catalogRelationsUnitBind(t, destination, "pg_get_backend_memory_contexts")
	current, err := n.Rebuild(expr.InfoOf(destinationNode), nil)
	if err != nil || current.ResultUsage().Type == n.ResultUsage().Type { t.Fatal("destination result generation", current, err) }
	destinationScope := tablespaceInfoUnitScope(t, destination)
	currentSource, err := current.(functions.BackendMemoryContexts).OpenSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(destinationScope)}})
	if err != nil { t.Fatal(err) }
	defer currentSource.Close()
	for _, value := range currentSource.Values {
		if value.Type() != current.ResultUsage().Type { t.Fatal("foreign composite result", value) }
		for i := range 10 {
			field := value.CompositeField(i)
			if field.Type() != current.ResultUsage().Type.CompositeMember(i).Usage.Type { t.Fatal("foreign member", i, field) }
		}
		path := value.CompositeField(4)
		for i := range path.ArrayLen() { element := path.ArrayElement(i); if element.Type() != current.ResultUsage().Type.CompositeMember(3).Usage.Type { t.Fatal("foreign path element", element) } }
	}
	if _, err := n.OpenSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: core.HostOf(scope)}}); !errors.Is(err, context.Canceled) { t.Fatal("precanceled open", err) }
	bad := n
	bad.Info.Usage = sqltype.Usage{}
	if bad.Validate() == nil { t.Fatal("missing record accepted") }
}
