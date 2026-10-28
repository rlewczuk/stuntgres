package core_test

import (
	"context"
	"errors"
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func xmlPreparedMode(t *testing.T, e *core.Engine, s *session.Session, mode string) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, &parser.Set{Name: parser.Identifier{Text: "xmloption"}, Values: []string{mode}}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
}
func xmlPreparedLiteral(t *testing.T, d core.Description) sqltype.Value {
	t.Helper()
	for _, capture := range d.Temporals {
		if capture.TypeID == sqltype.XML {
			return capture.Value
		}
	}
	t.Fatal("missing accepted XML input artifact")
	return sqltype.Value{}
}
func xmlGraphLiteral(t *testing.T, root *plan.ProjectNode) sqltype.Value {
	t.Helper()
	var result sqltype.Value
	plan.QueryExpressions(root, func(node expr.Expr) {
		plan.WalkExpression(node, func(node expr.Expr) {
			if literal, ok := node.(plan.LiteralExpr); ok && literal.Value.Type() != nil && literal.Value.Type().ID() == sqltype.XML {
				result = literal.Value
			}
		})
	})
	if result.Type() == nil {
		t.Fatal("accepted graph lost XML literal")
	}
	return result
}

func TestXMLSQLPreparedOwnership(t *testing.T) {
	t.Run("AcceptedDescriptionAcrossMode", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		ast, d := sequencePreparedDescription(t, e, s, `SELECT XML 'text<a/>' AS frozen, length('owned') AS folded`)
		literal := xmlPreparedLiteral(t, d)
		owner, epoch, analysis := d.CatalogOwner, d.CatalogEpoch, d.Analysis
		if owner != &e.Catalog.Owner || epoch != e.CatalogRead().Epoch() || analysis == nil || d.Folds == nil || d.Folds.Current().Entries != nil {
			t.Fatal("analysis and fold acceptance conflated")
		}
		preparedFoldExecute(t, e, s, ast, &d)
		folds := d.Folds.Current()
		if len(folds.Entries) == 0 {
			t.Fatal("missing actual fold publication")
		}
		cell := d.Folds
		first := &folds.Entries[0]
		for _, mode := range []string{"document", "content", "document"} {
			xmlPreparedMode(t, e, s, mode)
			preparedFoldExecute(t, e, s, ast, &d)
			if d.CatalogOwner != owner || d.CatalogEpoch != epoch || d.Folds != cell || !sqltype.SameDatum(literal, xmlPreparedLiteral(t, d)) {
				t.Fatal("mode transition replaced accepted input backing")
			}
			current := cell.Current()
			if current.Generation != folds.Generation || &current.Entries[0] != first {
				t.Fatal("mode transition replaced accepted fold generation")
			}
		}
		// A real canceled publication attempt cannot mutate the accepted artifacts.
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		env, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&d, ast, env, auth.Catalog, nil)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(canceled, auth.Catalog, env, e.Catalog.State.Epoch())
		e.Mu.Unlock()
		if !errors.Is(err, context.Canceled) || publication.Group.Sealed || cell.Current().Generation != folds.Generation || &cell.Current().Entries[0] != first || !sqltype.SameDatum(literal, xmlPreparedLiteral(t, d)) {
			t.Fatal("rejected publication changed retained state", err)
		}
		e.Mu.Lock()
		stale, err := core.BeginFoldPublication(&d, ast, env, auth.Catalog, nil)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		stale.Environment.Namespace++
		err = stale.Accept(context.Background(), auth.Catalog, env, e.Catalog.State.Epoch())
		e.Mu.Unlock()
		if !errors.Is(err, ids.ErrCatalogReplaced) || stale.Group.Sealed || cell.Current().Generation != folds.Generation || &cell.Current().Entries[0] != first || !sqltype.SameDatum(literal, xmlPreparedLiteral(t, d)) {
			t.Fatal("stale publication changed retained state", err)
		}
	})
	t.Run("ActualBindingSurvivesFailedProducer", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		transaction := enginetest.TransactionBegin(t, s)
		defer enginetest.TransactionEnd(t, s, transaction, false)
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("xml_owned", `SELECT XML 'text<a/>' AS frozen, 'text<a/>'::text::xml AS live, length('owned') AS folded`, nil)
			return err
		})
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("xml_owned")
		literal := xmlPreparedLiteral(t, definition.Description)
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal("xml_portal", "xml_owned", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		binding := registry.Binding("xml_portal")
		if binding == nil || binding.Folds == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Entry.Description.Folds != definition.Description.Folds {
			t.Fatal("Bind did not retain actual definition and accepted graph")
		}
		graph := binding.Resource.Root
		accepted := *binding.Folds
		if len(accepted.Entries) == 0 {
			t.Fatal("no actual accepted fold backing")
		}
		graphLiteral := xmlGraphLiteral(t, graph)
		if !sqltype.SameDatum(literal, graphLiteral) || binding.Entry.Description.CatalogOwner != definition.Description.CatalogOwner {
			t.Fatal("graph copied or lost accepted literal authority")
		}
		first := &accepted.Entries[0]
		generation := accepted.Generation
		xmlPreparedMode(t, e, s, "document")
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartProtocolCommand(op, cycle)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		_, err = scope.ExecuteProtocolPortal("xml_portal", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		failure := scope.Finish(err)
		op.Done()
		var diagnostic *sqlstate.Error
		if !errors.As(failure, &diagnostic) || diagnostic.Code != "2200M" || diagnostic.Context != "" {
			t.Fatal("runtime TEXT cast failure timing/provenance", failure)
		}
		// Save the accepted graph/group before the failed producer's terminal detach;
		// detachment is not evidence that those immutable artifacts were mutated.
		if !sqltype.SameDatum(graphLiteral, xmlGraphLiteral(t, graph)) || accepted.Generation != generation || &accepted.Entries[0] != first || !sqltype.SameDatum(literal, xmlPreparedLiteral(t, definition.Description)) {
			t.Fatal("failed live producer mutated accepted XML state")
		}
		if binding.Resource.Root != nil {
			t.Fatal("failed producer retained its live graph")
		}
		if binding.Entry.Description.CatalogEpoch != e.CatalogRead().Epoch() || definition.Description.CatalogOwner == nil || binding.Transaction == (ids.TxRef{}) {
			t.Fatal("lost actual owner/transaction authority")
		}
	})
}
