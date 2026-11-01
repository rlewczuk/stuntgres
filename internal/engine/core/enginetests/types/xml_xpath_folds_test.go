package types_test

import (
	"context"
	"errors"
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestXMLXPathAcceptedFoldOwnership(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT xpath('1 div 3','<r/>'::xml) AS v")
	if d.Folds == nil || len(d.Folds.Current().Entries) != 0 {
		t.Fatal("analysis computed XPath")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	entry := enginetest.PreparedFoldEntry(t, d, 143, "{0.3333333333333333}")
	before := enginetest.SnapshotFoldDescription(d)
	owner := d.Folds
	if entry.Callable == nil || entry.Callable.RoutineOID() != 2932 || entry.Value.Type() != entry.Result.Type || entry.Value.ArrayElement(0).Type() != entry.Result.Type.Element() {
		t.Fatal("lost exact overload/result generation", entry)
	}
	payload, err := entry.Value.ArrayElement(0).XML()
	if err != nil || payload.Original() != "0.3333333333333333" || payload.RequestedMode() != sqltype.XMLContent {
		t.Fatal(payload, err)
	}
	enginetest.SessionSQL(t, s, "SET extra_float_digits=-3")
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	after := enginetest.PreparedFoldEntry(t, d, 143, "{0.3333333333333333}")
	if d.Folds != owner || d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, after) || !sqltype.SameDatum(entry.Value, after.Value) {
		t.Fatal("constant XPath lost accepted backing")
	}
	// Cancel the real publication operation, retaining the actual accepted objects.
	func() {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&d, ast, environment, auth.Catalog, nil)
		if err != nil {
			t.Fatal(err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(canceled, auth.Catalog, environment, e.Catalog.State.Epoch())
		current := owner.Current()
		if !errors.Is(err, context.Canceled) || publication.Group.Sealed || current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, before, 143, "{0.3333333333333333}")) {
			t.Fatal("rejected publication changed accepted XPath backing", err)
		}
	}()
	// The actual producer's rejection cannot publish staged XML[] entries.
	badAst, bad := enginetest.PreparedDescription(t, e, s, "SELECT xpath('1 div 3','<r/>'::xml),xpath('[','<r/>'::xml,'{}'::text[])")
	rejected, err := enginetest.PreparedExecute(e, s, badAst, &bad)
	if rejected.Rows != nil {
		rejected.Rows.Close()
		t.Fatal("failed planning exposed rows")
	}
	if err == nil {
		t.Fatal("malformed XPath accepted")
	}
	if len(bad.Folds.Current().Entries) != 0 {
		t.Fatal("failed planning published partial XML[]")
	}
	if &before.Accepted.Entries[0] != &owner.Current().Entries[0] || owner.Current().Generation != before.Accepted.Generation || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, before, 143, "{0.3333333333333333}")) {
		t.Fatal("rejected sibling mutated accepted artifacts")
	}
	enginetest.SessionSQL(t, s, "CREATE ROLE xpath_fold_reader")
	enginetest.SessionSQL(t, s, "REVOKE EXECUTE ON FUNCTION pg_catalog.xpath(text,xml) FROM PUBLIC")
	enginetest.SessionSQL(t, s, "SET ROLE xpath_fold_reader")
	rejected, err = enginetest.PreparedExecute(e, s, ast, &d)
	if rejected.Rows != nil {
		rejected.Rows.Close()
		t.Fatal("failed admission exposed rows")
	}
	if err == nil {
		t.Fatal("fold bypassed selected EXECUTE admission")
	}
	enginetest.SessionSQL(t, s, "RESET ROLE")
	enginetest.SessionSQL(t, s, "GRANT EXECUTE ON FUNCTION pg_catalog.xpath(text,xml) TO PUBLIC")
	if enginetest.PreparedFoldEntry(t, before, 143, "{0.3333333333333333}").Result.Type != entry.Result.Type {
		t.Fatal("rejected authority changed retained generation")
	}
}

func TestXMLXPathParameterFoldOwnership(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("xpath_parameter", "SELECT xpath($1::text,'<r/>'::xml) AS v", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("xpath_parameter")
	if definition == nil || definition.Description.Folds == nil || definition.Description.Parameters.Count != 1 {
		t.Fatal("missing actual parameter definition")
	}
	owner := definition.Description.Folds
	definitionAccepted := owner.Current()
	if definitionAccepted.Generation != 0 || len(definitionAccepted.Entries) != 0 {
		t.Fatal("Parse published parameter work", definitionAccepted)
	}
	body, source := definition.Body, definition.Source
	// Parameter-dependent computation is intentionally not an accepted entry.
	// Inspect the literal in the very graph consumed by the portal instead.
	literal := func(root *plan.ProjectNode, want string) plan.LiteralExpr {
		t.Helper()
		if len(root.Expressions) != 1 {
			t.Fatal("private XPath projection lost its single result")
		}
		value, ok := root.Expressions[0].(plan.LiteralExpr)
		if !ok {
			t.Fatalf("private XPath result is %T, not a completed literal", root.Expressions[0])
		}
		if !value.Property.ParameterDependent || value.RoutineReference != nil || value.Usage.Type == nil || value.Usage.Type.ID() != 143 || value.Value.Type() != value.Usage.Type || value.Value.ArrayLen() != 1 || value.Value.ArrayElement(0).Type() != value.Usage.Type.Element() {
			t.Fatal("private literal lost parameter/result/leaf provenance", value)
		}
		xml, err := value.Value.ArrayElement(0).XML()
		if err != nil || xml.Original() != want || xml.RequestedMode() != sqltype.XMLContent {
			t.Fatal("private literal lost owned CONTENT result", xml, err)
		}
		return value
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "xpath_parameter", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 1, 0, 0, 0, 7, '1', ' ', 'd', 'i', 'v', ' ', '3', 0, 0}})
			return err
		})
	}
	enginetest.SessionSQL(t, s, "SET plan_cache_mode=force_custom_plan")
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	bind("xpath_old")
	old := registry.Binding("xpath_old")
	if old == nil || old.Resource == nil || old.Resource.Root == nil || old.Folds == nil {
		t.Fatal("missing actual private portal graph/folds")
	}
	graph := old.Resource.Root
	accepted := *old.Folds
	if accepted.Generation != 0 || len(accepted.Entries) != 0 {
		t.Fatal("parameter binding retained persistent fold entries", accepted)
	}
	private := literal(graph, "0.3333333333333333")
	folds, resource, birth := old.Folds, old.Resource, old.Birth
	enginetest.SessionSQL(t, s, "SET extra_float_digits=-3")
	bind("xpath_new")
	next := registry.Binding("xpath_new")
	if next == nil || next.Resource == nil || next.Resource.Root == nil || next.Resource.Root == graph || next.Resource == resource || next == old || next.Birth <= birth {
		t.Fatal("parameter portals shared a private producer lifetime")
	}
	current := owner.Current()
	if registry.Lookup("xpath_parameter") != definition || owner != definition.Description.Folds || old.Entry.Description.Folds != owner || next.Entry.Description.Folds != owner || definition.Body != body || definition.Source != source {
		t.Fatal("binding replaced the actual definition or its fold owner")
	}
	if len(current.Entries) != 0 || current.Generation != definitionAccepted.Generation || len(current.Dependencies) != len(definitionAccepted.Dependencies) {
		t.Fatal("parameter work published into definition folds", current)
	}
	if old.Folds != folds || next.Folds == nil || next.Folds == folds || len(folds.Entries) != 0 || len(next.Folds.Entries) != 0 || folds.Generation != accepted.Generation || next.Folds.Generation != 0 || len(folds.Dependencies) != len(accepted.Dependencies) {
		t.Fatal("parameter bindings changed or retained accepted entries", folds, next.Folds)
	}
	if old.Resource != resource || old.Resource.Root != graph || old.Birth != birth || old.Retired {
		t.Fatal("new binding changed the old private producer lifetime")
	}
	newValue := literal(next.Resource.Root, "0.333333333333")
	retained := literal(graph, "0.3333333333333333")
	if retained.Info != private.Info || !sqltype.SameDatum(retained.Value, private.Value) || !sqltype.SameDatum(retained.Value.ArrayElement(0), private.Value.ArrayElement(0)) {
		t.Fatal("new binding mutated old literal provenance/backing")
	}
	if &graph.Expressions[0] == &next.Resource.Root.Expressions[0] || sqltype.SameDatum(private.Value, newValue.Value) || sqltype.SameDatum(private.Value.ArrayElement(0), newValue.Value.ArrayElement(0)) {
		t.Fatal("parameter executions shared result/leaf backing")
	}
	if private.Usage != newValue.Usage || private.Usage != old.Entry.Description.Results[0].Usage || newValue.Usage != next.Entry.Description.Results[0].Usage || private.Source != newValue.Source || private.Pos != newValue.Pos {
		t.Fatal("private bindings lost source or exact result descriptor generations")
	}
	for _, name := range []string{"xpath_old", "xpath_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
