package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func tablespaceInfoUnitScope(t *testing.T, e *core.Engine) *core.StatementScope {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled) })
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestTablespaceInfoBinding(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		name   string
		oid    uint32
		result sqltype.ID
		set    bool
	}{
		{"pg_tablespace_location", 3778, sqltype.Text, false}, {"pg_tablespace_databases", 2556, sqltype.Oid, true},
	} {
		node := catalogRelationsUnitBind(t, e, tc.name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		ref := expr.InfoOf(node).RoutineReference
		if ref == nil || ref.RoutineOID() != tc.oid || node.ResultUsage().Type.ID() != tc.result || exec.SourceReturnsSet(node) != tc.set || !expr.AdmitsSource(node) {
			t.Fatal("typed binding", tc.name, ref)
		}
		if exec.PlannedVolatility(node) != expr.Stable || plan.GenerationImmutable(node) || exec.ConditionalConstantInputs(node) {
			t.Fatal("inquiry lost symbolic STABLE semantics")
		}
		child := node.(expr.Composite).Children()[0]
		if child.ResultUsage().Type.ID() != sqltype.Oid {
			t.Fatal("wrong sole formal")
		}
		found := false
		for _, decl := range routine.BuiltinRoutineDeclarations {
			if uint32(decl.Identity.ID) == tc.oid {
				found = true
				if !decl.Strict || decl.Volatility != expr.Stable || decl.Parallel != 's' || decl.ReturnsSet != tc.set {
					t.Fatal("release declaration", decl)
				}
			}
		}
		if !found {
			t.Fatal("missing successful declaration")
		}
	}
}

func TestTablespaceInfoProtocol(t *testing.T) {
	e := engine.New()
	for _, name := range []string{"pg_tablespace_location", "pg_tablespace_databases"} {
		node := catalogRelationsUnitBind(t, e, name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		count := 0
		plan.ExpressionChildren(node, func(expr.Expr) { count++ })
		if count != 1 {
			t.Fatal("physical child traversal", count)
		}
		composite := node.(expr.Composite)
		mapped, err := expr.Substitute(composite, expr.InfoOf(node), composite.Children())
		if err != nil || !analyze.SameGroupExpression(node, mapped) {
			t.Fatal("substitution/grouping", err)
		}
		if _, err := expr.Substitute(composite, expr.InfoOf(node), nil); err == nil {
			t.Fatal("missing child admitted")
		}
		if _, err := expr.Substitute(composite, expr.InfoOf(node), []expr.Expr{nil}); err == nil {
			t.Fatal("nil child admitted")
		}
		rendering, err := node.(interface {
			Render() (expr.Rendering, error)
		}).Render()
		if err != nil || rendering.Name != name || !rendering.Replay.CatalogSnapshot || len(rendering.Args) != 1 {
			t.Fatal("symbolic render", rendering, err)
		}
		captured, ok, err := node.(interface {
			Capture(expr.CaptureKind) (expr.CaptureOperands, bool, error)
		}).Capture(expr.CaptureFunction)
		if err != nil || !ok || captured.Count != 1 || !analyze.SameGroupExpression(captured.Inline[0], composite.Children()[0]) {
			t.Fatal("original routine capture", captured, ok, err)
		}
		if expr.IsSetSource(node) {
			if !analyze.SameQuerySetOccurrence(node, mapped) {
				t.Fatal("reconstruction lost the written set occurrence")
			}
			binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			requests := []expr.Expr{node}
			root, err := binder.LowerQuerySets(&plan.SingletonNode{}, requests, nil, false)
			if err != nil {
				t.Fatal("query-set lowering", err)
			}
			stage, ok := root.(*plan.SetProjectNode)
			if !ok || len(stage.Calls) != 1 {
				t.Fatal("missing selected set stage")
			}
			if err := exec.ValidateQuerySetSteps(stage.Steps, len(stage.Calls), len(stage.Scalars)); err != nil {
				t.Fatal("missing set occurrence schedule", err)
			}
			if _, ok := requests[0].(plan.ColumnExpr); !ok {
				t.Fatal("set occurrence was not rewritten to its owned slot")
			}
		}
	}
}

func TestTablespaceInfoAuthority(t *testing.T) {
	e := engine.New()
	scope := tablespaceInfoUnitScope(t, e)
	host := core.HostOf(scope)
	oidType, _ := e.CatalogRead().LookupType(sqltype.Oid)
	textType, _ := e.CatalogRead().LookupType(sqltype.Text)
	view := scope.Inquiry.CommentCatalog
	witness, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	if !ok || witness.RelationKind != catalog.CatalogRelation || witness.VirtualView || pgcatalog.ClassKind(view, witness) != "r" {
		t.Fatal("missing real pg_class logical storage witness")
	}
	state, oid, revision := e.Catalog.State, e.Catalog.NextOID(), e.Catalog.NextRevision()
	value, present, err := host.TablespaceDatabase(uint32(catalog.DefaultTablespaceOID), oidType)
	got, conversion := value.OID()
	db, ok := view.Database(catalog.CurrentDatabaseAddress)
	if err != nil || conversion != nil || !present || !ok || got != uint32(db.Address().ObjectID) {
		t.Fatal("retained attachment", got, present, err, conversion)
	}
	for _, id := range []uint32{0, uint32(catalog.DefaultTablespaceOID), uint32(catalog.GlobalTablespaceOID)} {
		value, err := host.TablespaceLocation(id, textType)
		text, conversion := value.Text()
		if err != nil || conversion != nil || text != "" {
			t.Fatal("builtin/default logical location", text, err, conversion)
		}
	}
	if _, err := host.TablespaceLocation(^uint32(0), textType); err == nil {
		t.Fatal("physical namespace fabricated")
	}
	if e.Catalog.State != state || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Catalog.State.Epoch() != view.Epoch() {
		t.Fatal("inquiry changed catalog/allocator")
	}
	for _, bad := range []catalog.Read{{}, engine.New().CatalogRead(), view.WithState(catalog.NewState(view.Epoch()))} {
		scope.Inquiry.CommentCatalog = bad
		if _, err := host.TablespaceLocation(1663, textType); err == nil {
			t.Fatal("invalid retained tablespace authority accepted")
		}
		if _, _, err := host.TablespaceDatabase(1663, oidType); err == nil {
			t.Fatal("invalid retained database authority accepted")
		}
	}
	scope.Inquiry.CommentCatalog = view
	missingDatabase := catalog.CloneState(view.State())
	missingDatabase.RemoveDescriptor(catalog.CurrentDatabaseAddress)
	scope.Inquiry.CommentCatalog = view.WithState(missingDatabase)
	if _, _, err := host.TablespaceDatabase(1663, oidType); err == nil {
		t.Fatal("missing attachment silently treated as empty")
	}
	scope.Inquiry.CommentCatalog = view
}

type tablespaceInfoCountingArg struct {
	plan.LiteralExpr
	calls *int
}

func (n tablespaceInfoCountingArg) Eval(expr.Context) (sqltype.Value, error) {
	*n.calls++
	return n.Value, nil
}

func TestTablespaceInfoCursor(t *testing.T) {
	e := engine.New()
	scope := tablespaceInfoUnitScope(t, e)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	oidType, _ := e.CatalogRead().LookupType(sqltype.Oid)
	textType, _ := e.CatalogRead().LookupType(sqltype.Text)
	value, err := sqltype.OIDValue(oidType, 1663)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	arg := tablespaceInfoCountingArg{plan.LiteralExpr{Info: expr.Info{Usage: oidType.Usage()}, Value: value}, &calls}
	node := functions.TablespaceDatabases{Info: expr.Info{Usage: oidType.Usage()}, Arg: arg}
	first, err := node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if calls != 2 || first.State != nil || first.Values != nil || second.State != nil || second.Values != nil {
		t.Fatal("per-invocation inline ownership", calls)
	}
	for _, cursor := range []*expr.SourceCursor{&first, &second} {
		v, present, err := cursor.Next(context.Background())
		got, conversion := v.OID()
		if err != nil || conversion != nil || !present || got != uint32(catalog.CurrentDatabaseAddress.ObjectID) {
			t.Fatal("single logical occupant", v, present, err)
		}
		eof, present, err := cursor.Next(context.Background())
		if err != nil || present || !eof.IsNull() || eof.Type() != oidType {
			t.Fatal("typed source EOF", eof, present, err)
		}
	}
	arg.Value = sqltype.Null(oidType)
	node.Arg = arg
	empty, err := node.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	v, present, err := empty.Next(context.Background())
	if err != nil || present || !v.IsNull() || v.Type() != oidType || calls != 3 {
		t.Fatal("strict empty set", v, present, err, calls)
	}
	scalar := functions.TablespaceLocation{Info: expr.Info{Usage: textType.Usage()}, Arg: arg}
	null, err := scalar.Eval(c)
	if err != nil || !null.IsNull() || null.Type() != textType || calls != 4 {
		t.Fatal("strict scalar NULL", null, err, calls)
	}
	source, err := scalar.OpenSource(c)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	v, present, err = source.Next(context.Background())
	if err != nil || !present || !v.IsNull() || v.Type() != textType || calls != 5 {
		t.Fatal("scalar NULL source must emit one row", v, present, err, calls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := source.Next(canceled); !errors.Is(err, context.Canceled) || !source.Exhausted || source.State != nil {
		t.Fatal("cancellation must close cursor", err)
	}
}

func TestTablespaceInfoStored(t *testing.T) {
	e := engine.New()
	fresh := engine.New()
	for _, name := range []string{"pg_tablespace_location", "pg_tablespace_databases"} {
		node := catalogRelationsUnitBind(t, e, name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		mapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !analyze.SameGroupExpression(node, mapped) {
			t.Fatal("stored remap", err)
		}
		demand := false
		text, err := (stored.ScalarRenderer{CommentSnapshot: &demand}).ScalarFunctionSQL(context.Background(), e.CatalogRead(), mapped)
		if err != nil || !demand || !strings.Contains(text, "pg_catalog."+name+"(") {
			t.Fatal("symbolic replay", text, demand, err)
		}
		if err := table.ValidateGenerationPlan(e.CatalogRead(), nil, mapped); err != nil {
			t.Fatal("stored provider", err)
		}
		retagged, _, err := stored.RetagTypes(fresh.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := fresh.CatalogRead().LookupType(d.ID())
			return next
		})
		if err != nil {
			t.Fatal("descriptor retag", err)
		}
		destination, _ := fresh.CatalogRead().LookupType(node.ResultUsage().Type.ID())
		if retagged.ResultUsage().Type != destination {
			t.Fatal("retag retained foreign result descriptor")
		}
		if err := retagged.(interface{ Validate() error }).Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
