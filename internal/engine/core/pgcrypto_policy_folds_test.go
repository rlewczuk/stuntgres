package core_test

import (
	"fmt"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestPgcryptoPolicyFoldOwnership(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pgcrypto")
	enginetest.SessionSQL(t, s, "SET plan_cache_mode=force_generic_plan")
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("pgcrypto_policy_fold", "SELECT crypt('foox','NB') AS value", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("pgcrypto_policy_fold")
	owner := definition.Description.Folds
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "pgcrypto_policy_fold", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("pgcrypto_policy_original")
	portal := registry.Binding("pgcrypto_policy_original")
	if portal.Resource == nil || portal.Resource.Root == nil || portal.Folds == nil { t.Fatal("missing actual accepted portal graph/fold owner") }
	graph, boundFolds := portal.Resource.Root, *portal.Folds
	accepted := enginetest.SnapshotFoldDescription(definition.Description)
	original := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Text, "NB53EGGqrrb5E")
	procedure, ok := original.Callable.(*catalog.Procedure)
	if !ok || procedure == nil || procedure.RoutineOID() == 0 || len(original.Inputs) != 2 {
		t.Fatal("fold lost authentic installed authority/inputs", original)
	}
	analysis := definition.Description.Analysis
	for i, policy := range []string{"off", "on", "fips", "off"} {
		enginetest.SessionSQL(t, s, "SET pgcrypto.builtin_crypto_enabled='"+policy+"'")
		name := fmt.Sprintf("pgcrypto_policy_reuse_%d", i)
		bind(name)
		current := definition.Description.Folds.Current()
		entry := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Text, "NB53EGGqrrb5E")
		if definition.Description.Analysis != analysis || definition.Description.Folds != owner || current.Generation != accepted.Accepted.Generation || &current.Entries[0] != &accepted.Accepted.Entries[0] || entry.Callable != procedure || !enginetest.SamePublishedFold(original, entry) {
			t.Fatal("setting mutation replaced accepted immutable authority/backing", policy)
		}
		if portal.Resource.Root != graph || portal.Folds.Generation != boundFolds.Generation || &portal.Folds.Entries[0] != &boundFolds.Entries[0] {
			t.Fatal("new Bind mutated original accepted graph/fold generation", policy)
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	enginetest.ConvertRejectPublication(t, e, s, definition.Body, definition.Description, accepted.Accepted)
	enginetest.PreparedFoldEntry(t, accepted, sqltype.Text, "NB53EGGqrrb5E")
	if portal.Resource.Root != graph || &portal.Folds.Entries[0] != &boundFolds.Entries[0] { t.Fatal("rejected publication mutated original portal") }
	t.Run("InstalledRebuildRetainsOriginalGraphAuthority", func(t *testing.T) {
		op, err := s.Acquire(t.Context())
		if err != nil { t.Fatal(err) }
		defer op.Done()
		query := enginetest.BindSelect(t, e, op, "SELECT crypt('foox','NB') AS value")
		project, ok := query.Root.(*plan.ProjectNode)
		if !ok || len(project.Expressions) != 1 { t.Fatal("original installed graph", query.Root) }
		original, ok := project.Expressions[0].(plan.InstalledCryptoExpr)
		if !ok || original.Procedure == nil || original.Ref.Address.ObjectID == 0 { t.Fatal("installed graph authority", project.Expressions[0]) }
		children := append([]expr.Expr(nil), original.Args...)
		rebuilt, err := original.Rebuild(original.Info, children)
		if err != nil { t.Fatal(err) }
		next, ok := rebuilt.(plan.InstalledCryptoExpr)
		if !ok || next.Procedure != original.Procedure || next.Ref != original.Ref || next.Plan != original.Plan || next.Properties() != original.Properties() || len(next.Args) != len(original.Args) {
			t.Fatal("rebuild replaced captured provider/procedure", rebuilt)
		}
		for i := range children {
			if next.Args[i] != original.Args[i] || original.Args[i] != children[i] { t.Fatal("original graph operand changed", i) }
		}
		if next.Procedure != procedure { t.Fatal("fold and graph selected different installed procedure") }
	})
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("pgcrypto_policy_original") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("pgcrypto_policy_fold") })
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
