package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatsResetDatabaseExpressionOwnership(t *testing.T) {
	x := databaseResetFixtureOf(t)
	for _, tc := range []struct {
		query  string
		kind   functions.StatsResetDatabaseKind
		oid    uint32
		strict expr.Strict
		count  int
	}{
		{"SELECT pg_stat_reset()", functions.ResetDatabase, 2274, expr.StrictNone, 0},
		{"SELECT pg_stat_reset_single_table_counters(0::oid)", functions.ResetTableCounters, 3776, expr.StrictAfter, 1},
		{"SELECT pg_stat_reset_single_function_counters(0::oid)", functions.ResetFunctionCounters, 3777, expr.StrictAfter, 1},
	} {
		op, err := x.s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		query := enginetest.BindSelect(t, x.e, op, tc.query)
		node, ok := query.Root.(*plan.ProjectNode).Expressions[0].(functions.StatsResetDatabase)
		if !ok || node.Validate() != nil || node.Kind != tc.kind || node.RoutineReference == nil || uint32(node.RoutineReference.RoutineOID()) != tc.oid || node.StrictNull() != tc.strict || node.IntrinsicVolatility() != expr.Volatile || node.PlanningFold() != expr.FoldNever || node.PreflightFold() != expr.PreflightKeep || len(node.Children()) != tc.count {
			op.Done()
			t.Fatal("typed declaration", node)
		}
		children := node.Children()
		rebuilt, err := node.Rebuild(node.Info, children)
		if err != nil || !node.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
			t.Fatal("reconstruction", err)
		}
		if tc.count == 1 {
			children[0] = nil
			if node.Argument == nil {
				t.Fatal("borrowed child backing")
			}
			if _, err := node.Rebuild(node.Info, children); err == nil {
				t.Fatal("invalid child admitted")
			}
		}
		rendering, err := node.Render()
		if err != nil || rendering.Name != node.Kind.Name() || len(rendering.Args) != tc.count {
			t.Fatal("render", err)
		}
		capture, accepted, err := node.Capture(expr.CaptureFunction)
		if err != nil || !accepted || capture.Count != tc.count || !capture.Indexed || node.ValidateStoredProvider() != nil {
			t.Fatal("capture/stored validation", err)
		}
		op.Done()
	}
	statsFunctionExec(t, x.s, "CREATE VIEW database_reset_view AS SELECT pg_stat_reset_single_table_counters(0::oid) IS NULL AS v", "CREATE ROLE database_reset_owned_reader")
	read := x.e.CatalogRead()
	ref := enginetest.CatalogRef(t, x.e, read, "database_reset_view")
	view := stored.ViewOf(read.MustRelation(ref.Address))
	storedRoot := view.Query().Root
	revision := view.DefinitionRevision()
	storedTyped := false
	for _, expression := range storedRoot.(*plan.ProjectNode).Expressions {
		plan.WalkExpression(expression, func(child expr.Expr) {
			if n, ok := child.(functions.StatsResetDatabase); ok {
				storedTyped = true
				if n.ValidateStoredProvider() != nil || n.RoutineReference == nil || n.RoutineReference.RoutineOID() != 3776 {
					t.Fatal("stored routine authority")
				}
			}
		})
	}
	if !storedTyped {
		t.Fatal("stored recipe lost typed provider")
	}
	_, vd := enginetest.PreparedDescription(t, x.e, x.s, "SELECT v FROM database_reset_view")
	defer x.e.RetirePreparedDescription(&vd)
	if stored.ViewOf(x.e.CatalogRead().MustRelation(ref.Address)) != view || view.Query().Root != storedRoot || view.DefinitionRevision() != revision {
		t.Fatal("stored Prepare changed authority")
	}
	databaseResetSeedCore(x.e, map[statistics.Key]statistics.Fact{x.a: {Inserts: 5}})
	before := x.e.Statistics.Read(x.a)
	transaction := enginetest.TransactionBegin(t, x.s)
	defer enginetest.TransactionEnd(t, x.s, transaction, false)
	cycle := x.s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, x.e, x.s, cycle, fn)
	}
	const source = "SELECT pg_stat_reset_single_table_counters('database_reset_a'::regclass::oid),abs(-7) AS folded"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("database_reset_owned", source, nil)
		return err
	})
	registry := core.PayloadOf(x.s).PreparedSQL
	definition := registry.Lookup("database_reset_owned")
	if definition == nil || definition.Description.Folds == nil || x.e.Statistics.Read(x.a) != before {
		t.Fatal("Parse lost owner/executed reset")
	}
	owner := definition.Description.Folds
	analysis := definition.Description.Analysis
	routineAddress := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3776}
	routine := read.MustProcedure(routineAddress)
	routineRef := x.e.Catalog.Domain.Ref(read.Epoch(), routineAddress)
	found := false
	for _, dependency := range analysis.Dependencies {
		if dependency.Ref == routineRef && dependency.Revision == routine.Revision() {
			found = true
		}
	}
	if !found || analysis.FoldCallables[routineAddress] != routine || definition.Description.CatalogOwner != read.Owner() || definition.Description.CatalogEpoch != read.Epoch() || definition.Description.PlanOwner != x.s.SessionToken || analysis.Domain != x.e.Catalog.Domain {
		t.Fatal("definition lost actual provider/catalog/session authority")
	}
	bind := func(name string) *core.PreparedBinding {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "database_reset_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil {
			t.Fatal("missing accepted graph")
		}
		return b
	}
	first, second := bind("database_reset_first"), bind("database_reset_second")
	root1, root2 := first.Resource.Root, second.Resource.Root
	resource1, resource2 := first.Resource, second.Resource
	folds1, folds2 := *first.Folds, *second.Folds
	accepted := owner.Current()
	saved := slices.Clone(accepted.Entries)
	inputBacking := make([][]exec.FoldInput, len(saved))
	dependencies := slices.Clone(accepted.Dependencies)
	for i := range saved {
		inputBacking[i] = saved[i].Inputs
		saved[i].Inputs = slices.Clone(saved[i].Inputs)
	}
	if len(saved) == 0 || first == second || resource1 == resource2 || root1 == root2 || &root1.Expressions[0] == &root2.Expressions[0] || &folds1.Entries[0] != &accepted.Entries[0] || &folds2.Entries[0] != &accepted.Entries[0] || x.e.Statistics.Read(x.a) != before {
		t.Fatal("accepted backing/Prepare effects")
	}
	assertNode := func(root *plan.ProjectNode) functions.StatsResetDatabase {
		n, ok := root.Expressions[0].(functions.StatsResetDatabase)
		if !ok || n.RoutineReference == nil || n.RoutineReference.RoutineOID() != 3776 || n.Source == (expr.FoldSourceOccurrence{}) || n.Occurrence == 0 || n.Argument == nil {
			t.Fatal("accepted reset recipe", root.Expressions[0])
		}
		child := expr.InfoOf(n.Argument)
		if child.Source == (expr.FoldSourceOccurrence{}) || child.Occurrence == 0 || child.Source == n.Source && child.Occurrence == n.Occurrence {
			t.Fatal("operand occurrence lost")
		}
		folded, ok := root.Expressions[1].(plan.LiteralExpr)
		entry := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Int4, "7")
		if !ok || !sqltype.SameDatum(folded.Value, entry.Value) || folded.Source != entry.Source || folded.Occurrence != entry.Occurrence {
			t.Fatal("sibling stopped consuming actual fold")
		}
		return n
	}
	n1, n2 := assertNode(root1), assertNode(root2)
	left, right := n1.Info, n2.Info
	left.Occurrence, right.Occurrence = 0, 0
	if left != right {
		t.Fatal("provider/logical authority changed across bindings")
	}
	assertFolds := func(got exec.AcceptedFolds) {
		if got.Generation != accepted.Generation || len(got.Entries) != len(saved) || &got.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(got.Dependencies, dependencies) {
			t.Fatal("accepted fold generation/backing/authority")
		}
		for i, entry := range got.Entries {
			if !enginetest.SamePublishedFold(entry, saved[i]) || len(entry.Inputs) != len(inputBacking[i]) || len(entry.Inputs) != 0 && &entry.Inputs[0] != &inputBacking[i][0] {
				t.Fatal("accepted payload/input backing changed")
			}
		}
	}
	op, err := x.s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer op.Done()
		x.e.Mu.Lock()
		defer x.e.Mu.Unlock()
		auth, err := x.e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		env, err := x.e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, env, auth.Catalog, nil)
		if err != nil || publication == nil {
			t.Fatal("real publication", err)
		}
		publication.Group.Entries = slices.Clone(publication.Group.Entries)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := publication.Accept(ctx, auth.Catalog, env, read.Epoch()); !errors.Is(err, context.Canceled) || publication.Group.Sealed {
			t.Fatal("rejected publication accepted", err)
		}
	}()
	if definition.Description.Analysis != analysis || definition.Description.Folds != owner || first.Resource != resource1 || second.Resource != resource2 || resource1.Root != root1 || resource2.Root != root2 {
		t.Fatal("rejected publication changed owner")
	}
	assertFolds(owner.Current())
	assertFolds(folds1)
	assertFolds(folds2)
	// An already-bound portal retains its initialized admission. Demand its
	// actual row and fail delivery, distinguishing executor cleanup from any
	// mutation of the accepted graph/fold values retained above.
	statsFunctionExec(t, x.s, "SAVEPOINT reset_ownership")
	deliveryFailure := errors.New("reset ownership delivery failure")
	delivered := 0
	err = enginetest.PreparedFoldProtocolTry(t, x.e, x.s, cycle, false, func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("database_reset_first", 0, func(fields []session.ProtocolField) error {
			delivered++
			if len(fields) != 2 || fields[0].Null || len(fields[0].Bytes) != 0 || fields[1].Null || string(fields[1].Bytes) != "7" {
				t.Fatal("demanded typed reset/fold row", fields)
			}
			return deliveryFailure
		}, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if !errors.Is(err, deliveryFailure) || delivered != 1 || x.e.Statistics.Read(x.a).Inserts != 0 {
		t.Fatal("actual portal delivery/effect", delivered, x.e.Statistics.Read(x.a), err)
	}
	if resource1.Root != nil || resource2.Root != root2 {
		t.Fatal("failed producer cleanup changed sibling graph")
	}
	assertFolds(owner.Current())
	assertFolds(accepted)
	assertFolds(folds1)
	assertFolds(folds2)
	statsFunctionExec(t, x.s, "ROLLBACK TO reset_ownership")
	if x.e.Statistics.Read(x.a).Inserts != 0 {
		t.Fatal("delivery failure rollback undid published reset")
	}
	databaseResetSeedCore(x.e, map[statistics.Key]statistics.Fact{x.a: {Inserts: 5}})
	before = x.e.Statistics.Read(x.a)
	// Re-execution of the held prepared definition, unlike continuing its
	// initialized portal, must perform current routine EXECUTE admission.
	statsFunctionExec(t, x.s, "SET ROLE database_reset_owned_reader")
	result, failure := enginetest.PreparedExecute(x.e, x.s, definition.Body, &definition.Description)
	if result.Rows != nil {
		result.Rows.Close()
	}
	err = failure
	if err == nil || x.e.Statistics.Read(x.a) != before {
		t.Fatal("held prepared definition retained EXECUTE authority", err)
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
		t.Fatal("current EXECUTE classification", err)
	}
	if resource1.Root != nil || resource2.Root != root2 {
		t.Fatal("failed producer cleanup changed sibling graph")
	}
	assertFolds(owner.Current())
	assertFolds(accepted)
	assertFolds(folds1)
	assertFolds(folds2)
	if !reflect.DeepEqual(n1, assertNode(root1)) || !reflect.DeepEqual(n2, assertNode(root2)) {
		t.Fatal("failure mutated retained graph")
	}
}
