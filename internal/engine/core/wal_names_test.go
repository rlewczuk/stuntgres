package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func walNamesBindOwned(t *testing.T, e *core.Engine, sql string) (functions.WALName, exec.Context) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	raw := enginetest.Statement(t, "SELECT "+sql).(*parser.Select).Targets[0]
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
	b.Query.Auth = &auth
	b.Clause.AggregateClause = "SELECT"
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
	n, ok := node.(functions.WALName)
	if !ok {
		t.Fatalf("not an owned WAL recipe: %T", node)
	}
	return n, evaluation
}

func TestWALNamesMetadata(t *testing.T) {
	t.Run("startup-and-destination", func(t *testing.T) {
		e := engine.New()
		definition, err := e.Settings.Lookup("wal_segment_size")
		if err != nil {
			t.Fatal(err)
		}
		if e.WALNames.SegmentSize != uint32(definition.BootValue().Number) || e.WALNames.Timeline != 1 {
			t.Fatal("registry startup input", e.WALNames, definition.BootValue())
		}
		e.WALNames = functions.WALNameMetadata{SegmentSize: 33554432, Timeline: 0x80000000}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		enginetest.RoleExecute(t, e, op, "CREATE DATABASE wal_names_owned")
		done()
		target, err := e.SelectDatabase(context.Background(), "wal_names_owned")
		if err != nil {
			t.Fatal(err)
		}
		if target == e || target.WALNames != e.WALNames {
			t.Fatal("logical database lost originating startup input")
		}
		if e.Cluster.Seed != nil && e.Cluster.Seed.WALNames == e.WALNames {
			t.Fatal("fixture did not distinguish template defaults")
		}
		destination := e.RestoreStage()
		if destination == e || destination.WALNames != e.WALNames {
			t.Fatal("stage imported factory defaults")
		}
		source := engine.New()
		source.WALNames = functions.WALNameMetadata{SegmentSize: 1048576, Timeline: 0}
		n, sourceEval := walNamesBindOwned(t, source, "pg_walfile_name('0/2000000'::pg_lsn)")
		sql, err := stored.DefaultSQL(context.Background(), source.CatalogRead(), n)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sql, "pg_walfile_name") || strings.Contains(sql, "000000000000000000000020") {
			t.Fatal("rendered a source result instead of recipe", sql)
		}
		next, destinationEval := walNamesBindOwned(t, destination, sql)
		if n.Metadata != source.WALNames || next.Metadata != e.WALNames || n.Metadata == next.Metadata || n.Arg.ResultUsage().Type == next.Arg.ResultUsage().Type || n.RoutineReference == next.RoutineReference {
			t.Fatal("destination retained source definition or metadata")
		}
		v, err := n.Eval(&sourceEval)
		if err != nil {
			t.Fatal(err)
		}
		text, err := v.Text()
		if err != nil || text != "000000000000000000000020" {
			t.Fatal(text, err)
		}
		v, err = next.Eval(&destinationEval)
		if err != nil {
			t.Fatal(err)
		}
		text, err = v.Text()
		if err != nil || text != "800000000000000000000001" {
			t.Fatal(text, err)
		}
		source.WALNames = functions.WALNameMetadata{SegmentSize: 1073741824, Timeline: 7}
		v, err = n.Eval(&sourceEval)
		if err != nil {
			t.Fatal(err)
		}
		text, err = v.Text()
		if err != nil || text != "000000000000000000000020" {
			t.Fatal("bound recipe borrowed mutable runtime", text, err)
		}
	})
	t.Run("accepted-fold-ownership", walNamesAcceptedOwnership)
}

func walNamesAcceptedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const sql = "SELECT pg_walfile_name('0/1000000'::pg_lsn) AS n,pg_split_walfile_name('000000010000000100000000') AS r"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("wal_owned", sql, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("wal_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual definition")
	}
	owner := definition.Description.Folds
	command(func(scope *core.StatementScope) error {
		_, err := scope.DescribeProtocolStatement("wal_owned")
		return err
	})
	analysis := definition.Description.Analysis
	view := e.CatalogRead()
	for _, oid := range []ids.OID{2851, 6213} {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		d := view.MustProcedure(address)
		if analysis.FoldCallables[address] != d {
			t.Fatal("selected declaration lost", oid)
		}
	}
	bind := func(name string) *core.PreparedBinding {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "wal_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Folds != owner || b.Entry.Description.Analysis != analysis {
			t.Fatal("missing real binding")
		}
		return b
	}
	first, second := bind("wal_first"), bind("wal_second")
	accepted := owner.Current()
	retained := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Text, "000000010000000000000001")
	routine := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 2851})
	if retained.Callable != routine || retained.Result.Type.ID() != sqltype.Text || retained.Value.Type() != retained.Result.Type || retained.Source == (expr.FoldSourceOccurrence{}) || retained.Occurrence == 0 || len(retained.Inputs) != 1 {
		t.Fatal("accepted callable/input provenance")
	}
	input, err := retained.Inputs[0].Value.PGLSN()
	if err != nil || input != 0x1000000 || retained.Inputs[0].Usage.Type.ID() != sqltype.PgLSN {
		t.Fatal("accepted formal operand", input, err)
	}
	firstFolds, secondFolds := *first.Folds, *second.Folds
	if accepted.Generation == 0 || len(accepted.Entries) == 0 || first == second || first.Resource == second.Resource || first.Resource.Root == second.Resource.Root || first.Folds == second.Folds || firstFolds.Generation != accepted.Generation || secondFolds.Generation != accepted.Generation || &firstFolds.Entries[0] != &accepted.Entries[0] || &secondFolds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("accepted ownership/backing")
	}
	assert := func(root *plan.ProjectNode) {
		t.Helper()
		if len(root.Expressions) != 2 {
			t.Fatal("missing projections")
		}
		folded, ok := root.Expressions[0].(plan.LiteralExpr)
		if !ok || !sqltype.SameDatum(folded.Value, retained.Value) || folded.Occurrence != retained.Occurrence || folded.Source != retained.Source || folded.Source == (expr.FoldSourceOccurrence{}) {
			t.Fatal("fold backing/provenance")
		}
	}
	firstRoot, secondRoot := first.Resource.Root, second.Resource.Root
	assert(firstRoot)
	assert(secondRoot)
	savedEntries := slices.Clone(accepted.Entries)
	savedDependencies := slices.Clone(accepted.Dependencies)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	func() {
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
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
		if err != nil || publication == nil {
			t.Fatal(err)
		}
		publication.Group.Entries = slices.Clone(publication.Group.Entries)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := publication.Accept(ctx, auth.Catalog, environment, view.Epoch()); !errors.Is(err, context.Canceled) || publication.Group.Sealed {
			t.Fatal("rejected publication", err)
		}
	}()
	for _, actual := range []exec.AcceptedFolds{owner.Current(), *first.Folds, *second.Folds} {
		if actual.Generation != accepted.Generation || len(actual.Entries) != len(savedEntries) || &actual.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(actual.Dependencies, savedDependencies) {
			t.Fatal("rejection mutated accepted backing")
		}
		for i, entry := range actual.Entries {
			if !enginetest.SamePublishedFold(entry, savedEntries[i]) {
				t.Fatal("rejection mutated exact fold", i)
			}
		}
	}
	if definition.Description.Analysis != analysis || definition.Description.Folds != owner || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
		t.Fatal("rejection replaced accepted owner")
	}
	assert(firstRoot)
	assert(secondRoot)
}
