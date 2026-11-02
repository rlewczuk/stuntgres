package exec

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func queryIdentitySRF(t *testing.T, integer *sqltype.Descriptor) plan.UnnestExpr {
	t.Helper()
	var array *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.IsArray() && typ.Element().ID() == integer.ID() {
			// Builtins returns a fresh descriptor generation on every call.
			// Bind the array to this fixture's actual scalar generation.
			var err error
			array, err = sqltype.Array(typ.ID(), typ.Name(), integer)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if array == nil {
		t.Fatal("missing integer array")
	}
	elements := make([]expr.Expr, 3)
	for i := range elements {
		value, err := sqltype.Integer(integer, int64(i+2))
		if err != nil {
			t.Fatal(err)
		}
		elements[i] = plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}
	}
	return plan.UnnestExpr{Info: expr.Info{Usage: integer.Usage()}, Args: []expr.Expr{
		plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: elements},
	}}
}

func queryIdentityView(root *plan.ProjectNode, inputs *Source, locks *LockContext) *Source {
	return &Source{
		FlatWidth: len(root.Schema()), Materialized: true, Locks: locks,
		NestedRoot: root, NestedInputs: inputs,
		Lineage: &QueryLockLineage{Root: root, Inputs: inputs, Stage: &Source{
			Materialized: true, Occurrences: QueryTreeOccurrences(root, inputs, nil),
		}},
	}
}

// The nested FROM-SRF view has no identity-reading expressions of its own.
// Its captured physical manifest must nevertheless survive for an outer virtual
// read, including projection of the third expanded row rather than only row zero.
func TestQueryIdentityStoredFunctionLineage(t *testing.T) {
	ctx := context.Background()
	integer := asyncUnitType(t, sqltype.Int4)
	value, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	columns := plan.QuerySchema{{Name: "id", Usage: integer.Usage()}}
	domain := &ids.Domain{}
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 601})
	relation := &catalog.Relation{ObjectAddress: ref.Address, RelationKind: catalog.TableRelation,
		ObjectName: "identity_input", Columns: columns, Attributes: []int32{1}}
	catalogState := catalog.NewState(1)
	catalogState.SetDescriptor(ref.Address, relation)
	rows := []storage.RowVersion{
		{Row: 10, Version: 20, Born: 1, Values: []sqltype.Value{sqltype.Null(integer)}},
		{Row: 11, Version: 21, Born: 2, Values: []sqltype.Value{value}},
	}
	captured := visibility.NewView(&visibility.Data{Catalog: catalog.NewRead(&catalog.Owner{Domain: domain}, catalogState),
		Frontier: 3, Committed: map[ids.ObjectAddress][]storage.RowVersion{ref.Address: rows}})
	defer captured.Release()
	locks := &LockContext{IdentityOnly: true}
	leaf := func() *Source {
		source, err := CapturePrefix(captured, ref)
		if err != nil {
			t.Fatal(err)
		}
		source.Visible = []int{1}
		CaptureQueryLockLeaves(locks, Leaf{Ref: ref, Relation: relation}, source, plan.RowMark{})
		source.Locks = locks
		return source
	}
	column := func(slot int) expr.Expr {
		return plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1, Slot: slot}
	}
	physical, scalarInput, functionInput := leaf(), leaf(), leaf()
	scalarRoot := &plan.ProjectNode{QuerySchema: columns, Frame: 1,
		Child: &plan.SourceNode{Ref: scalarInput.Ref, QuerySchema: columns}, Expressions: []expr.Expr{column(0)}}
	scalar := queryIdentityView(scalarRoot, scalarInput, locks)
	functionColumns := plan.QuerySchema{columns[0], {Name: "v", Usage: integer.Usage()}}
	function := &plan.FunctionFromNode{QuerySchema: functionColumns[1:], Frame: 1, Correlated: true,
		Calls: []expr.Expr{queryIdentitySRF(t, integer)}}
	functionRoot := &plan.ProjectNode{QuerySchema: functionColumns, Frame: 1,
		Child: &plan.JoinNode{QuerySchema: functionColumns, Kind: parser.JoinInner,
			Left: &plan.SourceNode{Ref: functionInput.Ref, QuerySchema: columns}, Right: function,
			LeftWidth: 1, RightWidth: 1, Frame: 1, CorrelatedRight: true},
		Expressions: []expr.Expr{column(0), column(1)}}
	storedFunctionRoot := &plan.ProjectNode{QuerySchema: functionColumns, Frame: 1,
		Child: &plan.DerivedNode{QuerySchema: functionColumns, Query: &plan.Query{Root: functionRoot}},
		Expressions: []expr.Expr{column(0), column(1)}}
	view := queryIdentityView(storedFunctionRoot, functionInput, locks)
	bundle := &Source{Locks: locks, Sources: []*Source{physical, scalar, view}}
	defer bundle.Close()
	leftColumns := plan.QuerySchema{columns[0], columns[0]}
	joinedColumns := plan.QuerySchema{columns[0], columns[0], columns[0], functionColumns[1]}
	joined := &plan.JoinNode{QuerySchema: joinedColumns, Kind: parser.JoinInner,
		Left: &plan.JoinNode{QuerySchema: leftColumns, Kind: parser.JoinInner,
			Left: &plan.SourceNode{Ref: physical.Ref, QuerySchema: columns}, Right: &plan.SourceNode{Ref: scalar.Ref, QuerySchema: columns, Occurrence: 1},
			LeftWidth: 1, RightWidth: 1, Frame: 1},
		Right: &plan.SourceNode{Ref: view.Ref, QuerySchema: functionColumns, Occurrence: 2}, LeftWidth: 2, RightWidth: 2, Frame: 1}
	virtual := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1,
		Slot: 0, RowWidth: 1, QueryOccurrence: 1, Virtual: column(0)}
	root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{columns[0], functionColumns[1]}, Frame: 1,
		Child: joined, Expressions: []expr.Expr{virtual, column(3)}}
	state := &State{Source: bundle, Retain: true, methodsCaptured: true, asyncCaptured: true}
	stage, err := OpenNode(ctx, state, root)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	row := make([]sqltype.Value, 2)
	for i := range 3 {
		present, err := stage.Next(ctx, row)
		if err != nil || !present || hierarchyInteger(t, row[0]) != 7 || hierarchyInteger(t, row[1]) != int64(i+2) {
			t.Fatal("outer virtual/SRF projection", i, present, err, row)
		}
		if got := AppendQueryProvenance(nil, stage.Source, i); !reflect.DeepEqual(got, []int{0, 0, 0}) {
			t.Fatal("expanded row lost captured visible indices", i, got)
		}
	}
	if present, err := stage.Next(ctx, row); present || err != nil {
		t.Fatal("expanded multiplicity", present, err)
	}
	if got := AppendQueryOccurrences(nil, stage.Source); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatal("nested manifest changed", got)
	}
	if !reflect.DeepEqual(AppendQueryOccurrences(nil, view), []int{2}) || len(stage.Source.Provenance) != 9 || len(stage.Source.Versions) != 0 || locks.RowMarked() {
		t.Fatal("identity-only capture became rowmark authority or lost lineage", stage.Source.Provenance, stage.Source.Versions)
	}
	if QueryHasIdentityReads(scalarRoot) || QueryHasIdentityReads(storedFunctionRoot) {
		t.Fatal("fixture introduced inner identity expressions instead of inherited demand")
	}
	if !view.NestedStage.Evaluation.Query.IdentityReads {
		t.Fatal("stored query state lost inherited expression identity demand")
	}
}

func TestQueryIdentitySetProjectAdapters(t *testing.T) {
	for _, kind := range []string{"scalar", "input", "lineage", "alternatives", "unlocked", "locator"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			var locks *LockContext
			if kind != "unlocked" {
				locks = &LockContext{IdentityOnly: true}
			}
			f := newHierarchyFixture(t, locks)
			input := f.local
			schema := f.node.QuerySchema
			switch kind {
			case "input":
				input = &Source{Input: input, Locks: locks, Layout: []int{1, 0}, Scratch: make([]sqltype.Value, 2)}
			case "lineage":
				root := &plan.ProjectNode{QuerySchema: schema, Frame: 1, Child: &plan.SourceNode{Ref: input.Ref, QuerySchema: schema}, Expressions: []expr.Expr{
					plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage()}, Scope: 1, Frame: 1, Slot: 0},
					plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage()}, Scope: 1, Frame: 1, Slot: 1},
				}}
				input = queryIdentityView(root, input, locks)
				defer input.Close()
			case "alternatives":
				input = f.source
			}
			columns := append(plan.QuerySchema(nil), schema...)
			columns = append(columns, catalog.Column{Name: "expanded", Usage: f.integer.Usage()})
			node := &plan.SetProjectNode{QuerySchema: columns, Child: &plan.SourceNode{Ref: input.Ref, QuerySchema: schema},
				BaseWidth: 2, MarkerWidth: 1, Calls: []expr.Expr{queryIdentitySRF(t, f.integer)}, CallSlots: []int{0},
				Steps: []plan.SetStep{{Kind: QuerySetCallStep}}, Frame: 1}
			state := &State{Source: input, IdentityReads: locks != nil && kind != "locator", Retain: true, methodsCaptured: true, asyncCaptured: true}
			// The dispatcher installs invocation retention on the opened stage.
			// Keep that actual owner alive through EOF and lineage inspection.
			stage, err := OpenNode(ctx, state, node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if !stage.Retain {
				t.Fatal("dispatcher lost retained invocation ownership")
			}
			count := 3
			if kind == "alternatives" {
				count = 9
			}
			row := make([]sqltype.Value, 3)
			for i := range count {
				present, err := stage.Next(ctx, row)
				if err != nil || !present || hierarchyInteger(t, row[2]) != int64(i%3+2) {
					t.Fatal("set expansion", i, present, err, row)
				}
				if !state.IdentityReads {
					continue
				}
				want := []int{0}
				if kind == "alternatives" {
					want = []int{0, AbsentQueryIndex}
					if i >= 3 {
						want = []int{AbsentQueryIndex, i/3 - 1}
					}
				}
				if got := AppendQueryProvenance(nil, stage.Source, i); !reflect.DeepEqual(got, want) {
					t.Fatal("SRF lost actual child provenance", i, got, want)
				}
				versions := AppendQueryVersions(nil, stage.Source, i, locks)
				if i < 3 && (versions[0].Row != 12 || versions[0].Version != 22) {
					t.Fatal("SRF reconstructed physical version", versions)
				}
			}
			if present, err := stage.Next(ctx, row); present || err != nil {
				t.Fatal("set expansion multiplicity", present, err)
			}
			wantOccurrences := []int{0}
			if kind == "alternatives" {
				wantOccurrences = []int{0, 1}
			} else if !state.IdentityReads {
				wantOccurrences = nil
			}
			if got := AppendQueryOccurrences(nil, stage.Source); !reflect.DeepEqual(got, wantOccurrences) {
				t.Fatal("SRF copied adapter fields instead of resolved occurrences", got, wantOccurrences)
			}
			if len(stage.Source.Versions) != 0 || stage.Source.VersionValid != nil || stage.Source.JoinPairs != nil || stage.Source.JoinLeft != nil || stage.Source.JoinRight != nil {
				t.Fatal("identity-only expansion allocated rowmark recheck caches")
			}
			if !state.IdentityReads && len(stage.Source.Provenance) != 0 {
				t.Fatal("unlocked or locator-only expansion acquired physical lineage")
			}
		})
	}
}
