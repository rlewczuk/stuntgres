package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestViewCheckOptions(t *testing.T) {
	for _, tc := range []struct {
		value string
		mode  viewCheckMode
		valid bool
	}{{"local", viewCheckLocal, true}, {"LoCaL", viewCheckLocal, true}, {"CASCADED", viewCheckCascaded, true}, {"loc", viewCheckNone, false}, {" local ", viewCheckNone, false}, {"true", viewCheckNone, false}} {
		_, _, mode, err := viewOptions([]parser.ViewOption{{Name: parser.Identifier{Text: "check_option"}, Value: tc.value}})
		if tc.valid {
			if err != nil || mode != tc.mode {
				t.Fatal(tc, mode, err)
			}
		} else {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22023" || d.Detail != `Valid values are "local" and "cascaded".` {
				t.Fatal(tc, err)
			}
		}
	}
	options := []parser.ViewOption{{Name: parser.Identifier{Text: "security_barrier"}, Value: "true"}, {Name: parser.Identifier{Text: "security_invoker"}, Value: "false"}, {Name: parser.Identifier{Text: "check_option"}, Value: "local"}}
	barrier, invoker, mode, err := viewOptions(options)
	if err != nil || !barrier || invoker || mode != viewCheckLocal {
		t.Fatal(barrier, invoker, mode, err)
	}
	options = append(options, options[2])
	_, _, _, err = viewOptions(options)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Message != `parameter "check_option" specified more than once` {
		t.Fatal(err)
	}
}

func TestViewCheckDeclaration(t *testing.T) {
	f := viewWriteMechanics(t)
	if err := validateViewCheckDeclaration(viewOf(f.view)); err != nil {
		t.Fatal(err)
	}
	project := *viewOf(f.view).Query().Root.(*plan.ProjectNode)
	definition := viewOf(f.view).With(func(p *ViewParts) { p.Query = &plan.Query{Root: &project} })
	for _, expressions := range [][]expr.Expr{nil, {plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: sqltype.Null(f.integer)}}} {
		project.Expressions = expressions
		var d *sqlstate.Error
		if err := validateViewCheckDeclaration(definition); !errors.As(err, &d) || d.Code != "0A000" || d.Hint != "Views that have no updatable columns are not automatically updatable." {
			t.Fatal(err)
		}
	}
	definition = definition.With(func(p *ViewParts) { p.Syntax = &parser.Select{Distinct: &parser.DistinctClause{}} })
	var d *sqlstate.Error
	if err := validateViewCheckDeclaration(definition); !errors.As(err, &d) || d.Hint != "Views containing DISTINCT are not automatically updatable." {
		t.Fatal(err)
	}
}

func viewCheckUnitBinding(t *testing.T, f viewWriteFixture, scope *StatementScope, op *session.Operation) analyze.Binding {
	t.Helper()
	b := analyze.Binding{Relation: f.view, Ref: f.viewRef, Targets: []int{0}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: sqltype.Null(f.integer)}}}}
	if err := bindViewWrite(f.e.catalog.owner(), f.authorization(t, op).Catalog, &b, &parser.Update{}); err != nil {
		t.Fatal(err)
	}
	return physicalBinding(b)
}

func TestViewCheckStack(t *testing.T) {
	f := viewWriteMechanics(t)
	guard := plan.LiteralExpr{Info: expr.Info{Usage: f.boolean.Usage()}, Value: sqltype.Boolean(f.boolean, true)}
	project := viewOf(f.view).Query().Root.(*plan.ProjectNode)
	project.Child = &plan.FilterNode{QuerySchema: plan.QuerySchema(f.base.Columns), Child: project.Child, Predicate: guard}
	layers := []*catalog.Relation{f.view}
	refs := []ids.ObjectRef{f.viewRef}
	for depth := range 2 {
		source := layers[depth]
		ref := refs[depth]
		view := *f.view
		view.ObjectAddress.ObjectID = ids.OID(20002 + depth)
		view.ObjectName = fmt.Sprintf("layer%d", depth)
		syntax := &parser.Select{From: &parser.TableReference{}}
		sourceNode := &plan.SourceNode{QuerySchema: plan.QuerySchema(source.Columns), Ref: ref, Occurrence: 0}
		expressions := make([]expr.Expr, len(source.Columns))
		for i, column := range source.Columns {
			origin := ref
			origin.Address.SubID = source.Attributes[i]
			expressions[i] = plan.ColumnExpr{Info: expr.Info{Usage: column.Usage}, Origin: origin, Scope: 1, Frame: 1, Slot: i}
		}
		view.View = NewView(ViewParts{Syntax: syntax, Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(view.Columns), Syntax: syntax, Frame: 1, Expressions: expressions, Child: &plan.FilterNode{QuerySchema: plan.QuerySchema(source.Columns), Child: sourceNode, Predicate: guard}}}, Sources: []viewLeaf{{ref: ref, attributes: source.Attributes}}})
		f.e.catalog.state.SetDescriptor(view.Address(), &view)
		layers = append(layers, &view)
		refs = append(refs, f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), view.Address()))
	}
	scope, op := f.scope(t)
	sets := [27]int{0, 1, 1, 2, 3, 3, 3, 3, 3, 4, 5, 5, 6, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7}
	for top := range 3 {
		for middle := range 3 {
			for leaf := range 3 {
				for i, mode := range []int{leaf, middle, top} {
					layers[i].View = viewOf(layers[i]).With(func(p *ViewParts) { p.CheckOption = viewCheckMode(mode) })
				}
				b := analyze.Binding{Relation: layers[2], Ref: refs[2]}
				if err := bindViewWrite(f.e.catalog.owner(), f.authorization(t, op).Catalog, &b, &parser.Insert{}); err != nil {
					t.Fatal(err)
				}
				var want, got []string
				for depth, bit := range []int{4, 2, 1} {
					if sets[top*9+middle*3+leaf]&bit != 0 {
						want = append(want, layers[2-depth].Name())
					}
				}
				for _, group := range viewWriteOf(b.ViewWrite).checks {
					got = append(got, group.relation.Name())
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal(top, middle, leaf, got, want)
				}
			}
		}
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}

func TestViewCheckMapping(t *testing.T) {
	f := viewWriteMechanics(t)
	project := viewOf(f.view).Query().Root.(*plan.ProjectNode)
	column := project.Expressions[0]
	// Reorder live physical columns while preserving frozen attribute identities.
	f.base.Attributes = []int32{11, 7, 19}
	f.base.Columns = []catalog.Column{f.base.Columns[1], f.base.Columns[0], f.base.Columns[2]}
	f.view.View = viewOf(f.view).With(func(p *ViewParts) { p.CheckOption = viewCheckLocal })
	project.Child = &plan.FilterNode{QuerySchema: plan.QuerySchema(f.base.Columns), Child: project.Child, Predicate: column}
	scope, op := f.scope(t)
	b := viewCheckUnitBinding(t, f, scope, op)
	mapped, ok := viewDirectColumn(viewWriteOf(b.ViewWrite).checks[0].predicates[0])
	if !ok || mapped.Slot != 0 || !sameViewRelation(mapped.Origin, f.baseRef) {
		t.Fatal(mapped, ok)
	}
	old := f.e.storage.Relations[f.base.Address()].Versions[0].Values
	ctx := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{old[1], old[0], old[2]}}}
	got, err := mapped.Eval(&ctx)
	if err != nil || got != old[1] {
		t.Fatal(got, err)
	}
}

func TestViewCheckPredicate(t *testing.T) {
	f := viewWriteMechanics(t)
	scope, _ := f.scope(t)
	row := f.e.storage.Relations[f.base.Address()].Versions[0].Values
	old := []sqltype.Value{sqltype.Null(f.integer)}
	calls := 0
	pass := queryProbe(f.boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if len(c.Row) != 3 || c.Row[1] != row[1] {
			t.Fatal("not coherent physical NEW", c.Row)
		}
		return sqltype.Boolean(f.boolean, true), nil
	})
	for _, value := range []sqltype.Value{sqltype.Boolean(f.boolean, false), sqltype.Null(f.boolean)} {
		b := analyze.Binding{Relation: f.base, Ref: f.baseRef, ViewWrite: viewWriteSlotOf(&viewWrite{checks: []viewCheckGroup{{relation: &catalog.Relation{ObjectName: "outer"}, predicates: []expr.Expr{pass}}, {relation: f.view, predicates: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: f.boolean.Usage()}, Value: value}}}}})}
		eval := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: old}}
		err := f.e.checkViewRow(scope, &b, row, &eval)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "44000" || d.Message != `new row violates check option for view "logical"` || d.Detail != "Failing row contains (1, 10, 42)." || calls != 0 || len(eval.Row) != 1 {
			t.Fatal(err, calls, eval.Row)
		}
	}
	b := analyze.Binding{Relation: f.base, Ref: f.baseRef, ViewWrite: viewWriteSlotOf(&viewWrite{checks: []viewCheckGroup{{relation: f.view, predicates: []expr.Expr{pass}}}})}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: old}}
	if err := f.e.checkViewRow(scope, &b, row, &eval); err != nil || calls != 1 || len(eval.Row) != 1 {
		t.Fatal("produced NEW check skipped or OLD frame replaced", err, calls, eval.Row)
	}
}

func TestViewCheckAdmission(t *testing.T) {
	for _, closeFailure := range []bool{false, true} {
		f := viewWriteMechanics(t)
		scope, _ := f.scope(t)
		calls := 0
		failure := errors.New("required close veto")
		guard := queryProbe(f.boolean.Usage(), func(*exec.Context) (sqltype.Value, error) {
			calls++
			return sqltype.Boolean(f.boolean, closeFailure || calls == 1), nil
		})
		b := analyze.Binding{Relation: f.base, Ref: f.baseRef, Targets: []int{0, 1, 2}, ViewWrite: viewWriteSlotOf(&viewWrite{checks: []viewCheckGroup{{relation: f.view, predicates: []expr.Expr{guard}}}})}
		originals := f.e.storage.Relations[f.base.Address()].Versions
		cursor := &insertConflictInputCursor{schema: f.base.Columns, rows: [][]sqltype.Value{originals[0].Values, originals[1].Values}}
		if closeFailure {
			cursor.closeError = failure
		}
		rows, err := assembleInsertRows(scope.op.Context(), b, cursor, -1, "public", scope.input, scope.output, scope, false, nil, nil)
		if closeFailure {
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
		} else {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "44000" {
				t.Fatal(err)
			}
		}
		if rows.values != nil || cursor.closes != 1 || calls != 2 || len(scope.stage.rows) != 0 || f.e.visibility.CommitSeq() != 1 {
			t.Fatal("prefix/close leak", rows, cursor.closes, calls)
		}
		if err := scope.Finish(err); err == nil {
			t.Fatal("failure suppressed")
		}
		lockEmpty(t, f.e)
	}
}

func TestViewCheckCancellation(t *testing.T) {
	f := viewWriteMechanics(t)
	scope, _ := f.scope(t)
	originals := f.e.storage.Relations[f.base.Address()].Versions
	row := append([]sqltype.Value(nil), originals[0].Values...)
	row[1], _ = sqltype.Integer(f.integer, 99)
	if err := scope.Writes().replace(f.baseRef, []storage.RowVersion{originals[0]}, indexWriteBatch{values: [][]sqltype.Value{row}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(scope.op.Context())
	defer cancel()
	guard := queryProbe(f.boolean.Usage(), func(*exec.Context) (sqltype.Value, error) { cancel(); return sqltype.Boolean(f.boolean, true), nil })
	b := analyze.Binding{Relation: f.base, Ref: f.baseRef, ViewWrite: viewWriteSlotOf(&viewWrite{checks: []viewCheckGroup{{relation: f.view, predicates: []expr.Expr{guard}}}})}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Output: scope.output}}
	err := f.e.checkViewRow(scope, &b, row, &eval)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := scope.Finish(err); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(originals, f.e.storage.Relations[f.base.Address()].Versions) || len(scope.stage.rows) != 0 || scope.viewWrite != nil || f.e.visibility.CommitSeq() != 1 {
		t.Fatal("cancelled check installed stage")
	}
	lockEmpty(t, f.e)
}

func TestViewCheckFinish(t *testing.T) {
	f := viewWriteMechanics(t)
	session := sessionStart(t, f.e, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := f.e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.gate(&parser.Update{}); err != nil {
		t.Fatal(err)
	}
	w := &viewWrite{base: f.base, ref: f.baseRef, layers: []viewWriteLayer{{relation: f.view, ref: f.viewRef, source: f.base, sourceRef: f.baseRef}}}
	scope.lockedRef, scope.lockedDescriptor = f.viewRef, f.view
	if err := scope.acquireViewWriteTarget(w); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			_ = scope.Finish(errors.New("test cleanup"))
		}
	})
	originals := f.e.storage.Relations[f.base.Address()].Versions
	row := append([]sqltype.Value(nil), originals[0].Values...)
	row[1], _ = sqltype.Integer(f.integer, 99)
	w.checks = []viewCheckGroup{{relation: f.view, predicates: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: f.boolean.Usage()}, Value: sqltype.Boolean(f.boolean, true)}}}}
	b := analyze.Binding{Relation: f.base, Ref: f.baseRef, ViewWrite: viewWriteSlotOf(w)}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Output: scope.output}}
	if err := f.e.checkViewRow(scope, &b, row, &eval); err != nil {
		t.Fatal(err)
	}
	if err := scope.Writes().replace(f.baseRef, []storage.RowVersion{originals[0]}, indexWriteBatch{values: [][]sqltype.Value{row}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(originals, f.e.storage.Relations[f.base.Address()].Versions) || len(scope.stage.rows) != 0 || scope.viewWrite != nil || f.e.visibility.CommitSeq() != 1 {
		t.Fatal("Finish veto installed checked row")
	}
	lockEmpty(t, f.e)
}

func TestViewCheckControlRemap(t *testing.T) {
	f := viewWriteMechanics(t)
	for _, mode := range []viewCheckMode{viewCheckNone, viewCheckLocal, viewCheckCascaded} {
		f.view.View = viewOf(f.view).With(func(p *ViewParts) { p.CheckOption = mode })
		ref := func(old ids.ObjectRef) ids.ObjectRef { old.Epoch++; return old }
		copy, err := remapFrozenPresentation(f.e.CatalogRead(), viewOf(f.view), nil, ref, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if copy == viewOf(f.view) || copy.CheckOption() != mode || copy.Sources()[0].ref.Epoch != f.baseRef.Epoch+1 || viewOf(f.view).Sources()[0].ref != f.baseRef {
			t.Fatal("mode/remint/mutation", mode, copy)
		}
	}
}
