package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// This private protocol witness asserts definition/graph backing, not SQL rows.
// Public direct/prepared outcomes remain exclusively in reference/extensions.
func TestSSLInfoExpressionProtocol(t *testing.T) {
	e := engine.New()
	installer, done := enginetest.RoleOperation(t, e, "postgres")
	err := enginetest.ExtensionUnitExecute(t, e, installer, &parser.CreateExtension{Name: parser.Identifier{Text: "sslinfo"}})
	done()
	if err != nil { t.Fatal(err) }
	s := enginetest.StartSession(t, e, nil)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const query = "SELECT ssl_client_serial(),ssl_is_used(),ssl_version(),ssl_cipher(),ssl_client_cert_present(),ssl_client_dn(),ssl_issuer_dn()"
	parse := func() { command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol("ssl_definition", query, nil); return err }) }
	bind := func(name string) { command(func(scope *core.StatementScope) error { _, err := scope.BindProtocolPortal(name, "ssl_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}}); return err }) }
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("ssl_definition")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil { t.Fatal("missing actual definition owner") }
	cell := definition.Description.Folds
	bind("ssl_first")
	binding := registry.Binding("ssl_first")
	if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Entry.Description.Folds != cell || binding.Folds == nil || len(binding.Folds.Entries) != 0 { t.Fatal("missing private graph or volatile call folded") }
	root := binding.Resource.Root
	if len(root.Expressions) != 7 { t.Fatal("lost seven installed call recipes", root) }
	for _, expression := range root.Expressions {
		n, ok := expression.(plan.InstalledSSLInfoExpr)
		if !ok || n.Procedure == nil || n.RoutineReference != n.Procedure || !n.Procedure.Origin.Valid(n.Ref.Address) { t.Fatal("accepted graph lost installed wrapper", expression) }
		found := false
		for _, dependency := range definition.Description.Analysis.Dependencies {
			if dependency.Ref == n.Ref && dependency.Revision == n.Procedure.Revision() { found = true }
		}
		if !found { t.Fatal("definition lost selected declaration dependency", n.Ref) }
	}
	accepted := cell.Current()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, cell.Environment, e.CatalogRead(), nil)
	if err != nil || publication == nil { t.Fatal("real publication attempt", err) }
	if err := publication.Accept(cancelled, e.CatalogRead(), cell.Environment, e.CatalogRead().Epoch()); !errors.Is(err, context.Canceled) { t.Fatal("canceled publication accepted", err) }
	if cell.Current().Generation != accepted.Generation || binding.Resource.Root != root || binding.Entry.Description.Folds != cell { t.Fatal("rejected publication mutated accepted backing") }
	bind("ssl_second")
	second := registry.Binding("ssl_second")
	if second == binding || second.Resource == nil || second.Resource.Root == root || second.Entry.Description.Folds != cell || binding.Resource.Root != root || len(second.Folds.Entries) != 0 { t.Fatal("definition/runtime ownership mixed") }
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("ssl_definition") })
	if cell.Live() || binding.Retired || binding.Resource.Root != root { t.Fatal("retirement mutated accepted graph") }
	parse()
	if registry.Lookup("ssl_definition").Description.Folds == cell { t.Fatal("new definition reused retired backing") }
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("ssl_first") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("ssl_second") })
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil { t.Fatal(err) }
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil { t.Fatal(err) }
}
