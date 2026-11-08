package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func lateralBindingFixture(t *testing.T) *analyze.Binder {
	t.Helper()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Query.Frame, b.Query.NextQueryFrame = 7, 7
	typ := b.Type(sqltype.Int4)
	columns := []analyze.QueryColumn{
		{Descriptor: catalog.Column{Name: "a", Usage: typ.Usage()}, Slot: 0, Leaf: -1, Physical: -1},
		{Descriptor: catalog.Column{Name: "b", Usage: typ.Usage()}, Slot: 1, Leaf: -1, Physical: -1},
	}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: columns, Frame: []catalog.Column{columns[0].Descriptor, columns[1].Descriptor}, Qualifiers: []analyze.QueryQualifier{{Name: "p", Columns: columns}}}
	return b
}

func lateralBindingColumn(name, member string) parser.Expr {
	return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}, {Text: member}}}, Pos: parser.Position{Offset: 20, Character: 21}}
}

func lateralBindingDerived(targets ...parser.Expr) *parser.DerivedQuery {
	return &parser.DerivedQuery{Lateral: true, Query: &parser.Select{Targets: targets}}
}

func TestQueryLateralBinding(t *testing.T) {
	t.Run("child_frames_and_values", func(t *testing.T) {
		for _, values := range []bool{false, true} {
			b := lateralBindingFixture(t)
			parent := b.Query.Namespace
			read := lateralBindingColumn("p", "b")
			var item parser.FromItem = lateralBindingDerived(read, parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{read}}})
			if values {
				item = &parser.ValuesQuery{Lateral: true, Rows: [][]parser.Expr{{read}}}
			}
			bound, ns, err := b.BindFrom(item)
			if err != nil {
				t.Fatal(err)
			}
			source := bound.(*plan.DerivedNode)
			child := source.Query.Root.(*plan.ProjectNode)
			if b.Query.Namespace != parent || b.FrameID() != 7 || len(b.Query.Scopes) != 0 || child.Frame == 7 || source.ScanFrame != 7 || !source.Lateral || len(ns.Columns) == 0 {
				t.Fatal("child borrowed or leaked the parent frame", source, child.Frame, b.FrameID())
			}
			if len(source.Captures) == 0 {
				t.Fatal("free parent reads lost")
			}
			for _, capture := range source.Captures {
				if capture.Frame != 7 || capture.Slot != 1 || capture.Name != "p" || capture.Pos.Character != 21 {
					t.Fatal("child-local read escaped or origin lost", capture)
				}
			}
			if values {
				input := child.Child.(*plan.ValuesNode)
				cell := input.Rows[0][0].(plan.ColumnExpr)
				output := child.Expressions[0].(plan.ColumnExpr)
				kind, ordinal := analyze.FrozenNamespaceNode(source)
				frames, queries := analyze.CollectQueryFrames(source)
				if !child.FrozenValues || child.Syntax != nil || cell.Frame != 7 || cell.Slot != 1 || output.Frame != child.Frame || output.Slot != 0 || kind != analyze.ViewValuesNamespace || ordinal != input.ViewOrdinal || frames[child.Frame] != child || len(queries) != 0 {
					t.Fatal("VALUES lost its real distinct frozen frame", cell, output, frames)
				}
			}
		}
	})
	t.Run("prefix_membership", func(t *testing.T) {
		for _, tc := range []struct {
			name               string
			targets            []parser.Expr
			dependent          bool
			prefix, free, slot int
		}{
			{"local", []parser.Expr{lateralBindingColumn("l", "v")}, true, 0, 0, 0},
			{"inherited", []parser.Expr{lateralBindingColumn("p", "a")}, false, 2, 1, 0},
			{"mixed", []parser.Expr{lateralBindingColumn("p", "a"), lateralBindingColumn("l", "v")}, true, 2, 1, 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				b := lateralBindingFixture(t)
				left := &parser.AliasReference{Input: &parser.ValuesQuery{Rows: [][]parser.Expr{{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 5}}}}}, Name: parser.Identifier{Text: "l"}, Columns: []parser.Identifier{{Text: "v"}}}
				bound, _, err := b.BindFrom(&parser.JoinReference{Left: left, Right: lateralBindingDerived(tc.targets...)})
				if err != nil {
					t.Fatal(err)
				}
				join := bound.(*plan.JoinNode)
				right := join.Right.(*plan.DerivedNode)
				if join.CorrelatedRight != tc.dependent || join.CorrelationPrefix != tc.prefix || len(join.Captures) != tc.free || right.Captures[len(right.Captures)-1].Slot != tc.slot || right.Captures[0].Frame != 7 {
					t.Fatal("wrong free/local prefix partition", join, right.Captures)
				}
				reads := []int{}
				analyze.WalkQueryArgumentNode(right.Query.Root, func(node expr.Expr) {
					if col, ok := node.(plan.ColumnExpr); ok && col.Frame == 7 {
						reads = append(reads, col.Slot)
					}
				})
				want := []int{tc.slot}
				if tc.name == "mixed" {
					want = []int{0, 2}
				}
				if !reflect.DeepEqual(reads, want) {
					t.Fatal("bound reads and capture metadata disagree", reads, want)
				}
			})
		}
	})
	t.Run("scope_restore_and_join_legality", func(t *testing.T) {
		for _, values := range []bool{false, true} {
			b := lateralBindingFixture(t)
			parent := b.Query.Namespace
			var source parser.FromItem = &parser.DerivedQuery{Query: &parser.Select{Targets: []parser.Expr{lateralBindingColumn("p", "a")}}}
			if values {
				source = &parser.ValuesQuery{Rows: [][]parser.Expr{{lateralBindingColumn("p", "a")}}}
			}
			_, _, err := b.BindFrom(source)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" || b.Query.Namespace != parent || b.FrameID() != 7 || len(b.Query.Scopes) != 0 {
				t.Fatal("ordinary source gained sibling visibility or leaked scope", err)
			}
			_, _, err = b.BindFrom(lateralBindingDerived(lateralBindingColumn("later", "a")))
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" || b.Query.Namespace != parent || b.FrameID() != 7 || len(b.Query.Scopes) != 0 {
				t.Fatal("failed lateral child leaked its frame", err)
			}
		}
		for _, kind := range []parser.JoinKind{parser.JoinRight, parser.JoinFull} {
			for _, local := range []bool{false, true} {
				b := lateralBindingFixture(t)
				left := &parser.AliasReference{Input: &parser.ValuesQuery{Rows: [][]parser.Expr{{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 5}}}}}, Name: parser.Identifier{Text: "l"}, Columns: []parser.Identifier{{Text: "v"}}}
				read := lateralBindingColumn("p", "a")
				if local {
					read = lateralBindingColumn("l", "v")
				}
				bound, _, err := b.BindFrom(&parser.JoinReference{Left: left, Right: lateralBindingDerived(read), Kind: kind})
				if local {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "42P10" || diagnostic.Detail != "The combining JOIN type must be INNER or LEFT for a LATERAL reference." {
						t.Fatal(err)
					}
				} else if err != nil || bound.(*plan.JoinNode).CorrelatedRight {
					t.Fatal("ancestor-only capture treated as join-local", bound, err)
				}
			}
		}
	})
	t.Run("immutable_lexical_rebase", lateralBindingRebase)
	t.Run("zero_width_system_occurrences", lateralBindingIdentity)
	t.Run("zero_width_whole_row_presence", lateralBindingPresence)
	t.Run("presence_owner_after_prefix_rebase", lateralBindingRebasedPresence)
	t.Run("execution_source_reads", lateralBindingExecutionSourceReads)
}

func lateralBindingRebase(t *testing.T) {
	b := lateralBindingFixture(t)
	typ := b.Type(sqltype.Int4)
	record, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "n", Usage: typ.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	column := func(frame uint32, slot int) plan.ColumnExpr {
		return plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage(), Pos: parser.Position{Character: 17}}, Scope: 1, Frame: frame, Slot: slot}
	}
	private := column(7, 0)
	virtual := column(7, 3)
	virtual.Virtual, virtual.RowStart, virtual.RowWidth, virtual.QueryOccurrence = private, 2, 2, 4
	local := column(8, 3)
	capture := []plan.FunctionCorrelation{{Frame: 7, Slot: 3, Name: "p", Pos: virtual.Pos}}
	nested := &plan.DerivedNode{Lateral: true, Captures: capture, ScanFrame: 8, Query: &plan.Query{Root: &plan.ProjectNode{Frame: 10, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{column(7, 3)}}}}
	sub := plan.SubqueryExpr{Info: expr.Info{Usage: typ.Usage()}, QueryPlan: &plan.Query{Root: &plan.ProjectNode{Frame: 9, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{virtual}}}}
	row := plan.RowExpr{Info: expr.Info{Usage: record.Usage()}, Fields: []expr.Expr{sub}, PresenceFrame: 7, PresenceSlot: 9}
	child := &plan.ProjectNode{Frame: 8, Child: nested, Expressions: []expr.Expr{row, local}}
	original := &plan.DerivedNode{Lateral: true, Captures: capture, ScanFrame: 7, Query: &plan.Query{Root: child}}
	mapped, err := analyze.RebaseFromCorrelation(b.Host, b.BindingCatalog(), original, 7, 2)
	if err != nil {
		t.Fatal(err)
	}
	copy := mapped.(*plan.DerivedNode)
	project := copy.Query.Root.(*plan.ProjectNode)
	mappedRow := project.Expressions[0].(plan.RowExpr)
	mappedSub := mappedRow.Fields[0].(plan.SubqueryExpr)
	mappedVirtual := mappedSub.QueryPlan.Root.(*plan.ProjectNode).Expressions[0].(plan.ColumnExpr)
	mappedNested := project.Child.(*plan.DerivedNode)
	if copy == original || copy.Query == original.Query || project == child || copy.Captures[0].Slot != 1 || original.Captures[0].Slot != 3 || mappedNested.Captures[0].Slot != 1 || nested.Captures[0].Slot != 3 || mappedNested.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.ColumnExpr).Slot != 1 {
		t.Fatal("free capture metadata or immutable graph ownership lost", copy, original)
	}
	if project.Frame != 8 || project.Expressions[1].(plan.ColumnExpr) != local || mappedVirtual.Slot != 1 || mappedVirtual.RowStart != 0 || mappedVirtual.RowWidth != 2 || mappedVirtual.QueryOccurrence != 4 || mappedVirtual.Virtual != virtual.Virtual || mappedVirtual.Virtual.(plan.ColumnExpr).Slot != 0 || mappedRow.PresenceFrame != 7 || mappedRow.PresenceSlot != 9 || child.Expressions[0].(plan.RowExpr).Fields[0].(plan.SubqueryExpr).QueryPlan != sub.QueryPlan {
		t.Fatal("rebase entered a private owner, child frame or presence coordinate", mappedVirtual, mappedRow)
	}
}

// Physical sources enter through the full query admission boundary, with the
// same operation/authorization owner used by enginetest.BindSelect.
func lateralPhysicalBinding(t *testing.T, e *core.Engine, from parser.FromItem) (*analyze.Binder, *plan.ProjectNode) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	b := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.Query.Auth = &auth
	b.Query.Frame, b.Query.NextQueryFrame = 6, 6
	var query *plan.Query
	if err == nil {
		query, err = b.BindSelect(&parser.Select{From: from, Targets: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}}, false)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return b, query.Root.(*plan.ProjectNode)
}

func lateralBindingIdentity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE lateral_empty()")
	table := func(name string) parser.FromItem {
		return &parser.AliasReference{Input: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "lateral_empty"}}}}, Name: parser.Identifier{Text: name}}
	}
	source := &parser.JoinReference{Left: table("a"), Right: &parser.JoinReference{Left: table("b"), Right: lateralBindingDerived(lateralBindingColumn("a", "tableoid"), lateralBindingColumn("b", "tableoid"))}}
	b, project := lateralPhysicalBinding(t, e, source)
	outer := project.Child.(*plan.JoinNode)
	join := outer.Right.(*plan.JoinNode)
	derived := join.Right.(*plan.DerivedNode)
	if len(outer.Left.Schema()) != 0 || join.LeftWidth != 0 || !join.CorrelatedRight || join.CorrelationPrefix != 0 || !join.CorrelationIdentityPrefix || len(join.Captures) != 1 || join.Captures[0].QueryOccurrence != 1 || len(derived.Captures) != 2 || derived.Captures[1].QueryOccurrence != 2 {
		t.Fatal("zero-width identities became ordinary row slots", join, derived.Captures)
	}
	for i, leaf := range b.Query.Sources {
		if !leaf.SystemAttributeRead || len(leaf.Uses) != 0 || derived.Captures[i].Frame != 7 || derived.Captures[i].Slot != 0 {
			t.Fatal("identity read corrupted ordinary column demand", i, leaf, derived.Captures)
		}
	}
	for i, node := range derived.Query.Root.(*plan.ProjectNode).Expressions {
		system := node.(plan.SystemAttributeExpr)
		if system.Frame != 7 || system.QueryOccurrence != i+1 || system.Owner != b.Query.Sources[i].Ref {
			t.Fatal("bound source occurrence/owner changed", system)
		}
	}
}

func lateralBindingPresence(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE lateral_presence()")
	table := func(name string) parser.FromItem {
		return &parser.AliasReference{Input: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "lateral_presence"}}}}, Name: parser.Identifier{Text: name}}
	}
	nullable := func() parser.FromItem {
		return &parser.JoinReference{Left: table("a"), Right: table("z"), Kind: parser.JoinLeft, On: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue}}}
	}
	whole := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "z"}}}, Pos: parser.Position{Offset: 20, Character: 21}}
	b, project := lateralPhysicalBinding(t, e, &parser.JoinReference{Left: nullable(), Right: lateralBindingDerived(whole)})
	local := project.Child.(*plan.JoinNode)
	derived := local.Right.(*plan.DerivedNode)
	row := derived.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.RowExpr)
	if local.LeftWidth != 0 || !local.CorrelatedRight || len(derived.Captures) != 1 || derived.Captures[0].PresenceSlot != row.PresenceSlot || row.PresenceSlot == 0 || row.PresenceFrame != 7 || len(row.Fields) != 0 || derived.Captures[0].Name != "z" {
		t.Fatal("zero-column nullable whole row lost its dependency", local, derived.Captures, row)
	}
	mapped, err := analyze.RebaseFromCorrelation(b.Host, b.BindingCatalog(), derived, 7, 9)
	if err != nil || mapped.(*plan.DerivedNode).Captures[0].PresenceSlot != row.PresenceSlot || mapped.(*plan.DerivedNode).Captures[0].Slot != 0 || derived.Captures[0].Slot != 0 {
		t.Fatal("presence identity was treated as an ordinary tuple coordinate", mapped, err)
	}
	// The same producer is inherited, not local-left, at this boundary.
	_, project = lateralPhysicalBinding(t, e, &parser.JoinReference{Left: nullable(), Right: &parser.JoinReference{Left: table("l"), Right: lateralBindingDerived(whole)}})
	ancestor := project.Child.(*plan.JoinNode).Right.(*plan.JoinNode)
	if ancestor.CorrelatedRight || ancestor.CorrelationPrefix != 0 || !ancestor.CorrelationIdentityPrefix || len(ancestor.Captures) != 1 || ancestor.Captures[0].PresenceSlot == 0 || ancestor.Captures[0].QueryOccurrence != 0 {
		t.Fatal("inherited presence was consumed as local-left", ancestor)
	}
}

func lateralBindingRebasedPresence(t *testing.T) {
	b := lateralBindingFixture(t)
	left := &parser.AliasReference{Input: &parser.ValuesQuery{Rows: [][]parser.Expr{{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 5}}}}}, Name: parser.Identifier{Text: "l"}, Columns: []parser.Identifier{{Text: "v"}}}
	right := &parser.AliasReference{Input: lateralBindingDerived(lateralBindingColumn("l", "v")), Name: parser.Identifier{Text: "x"}}
	nested := &parser.JoinReference{Left: left, Right: right, Kind: parser.JoinLeft}
	whole := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "x"}}}}
	bound, _, err := b.BindFrom(&parser.JoinReference{Left: nested, Right: lateralBindingDerived(whole)})
	if err != nil {
		t.Fatal(err)
	}
	join := bound.(*plan.JoinNode)
	producer := join.Left.(*plan.JoinNode).Right.(*plan.DerivedNode)
	row := join.Right.(*plan.DerivedNode).Query.Root.(*plan.ProjectNode).Expressions[0].(plan.RowExpr)
	if producer.PresenceSlot == 0 || producer.PresenceSlot != row.PresenceSlot || row.PresenceFrame != 7 || row.Fields[0].(plan.ColumnExpr).Slot != 1 || producer.Captures[0].Slot != 0 {
		t.Fatal("later whole-row binding retained a discarded producer", producer, row)
	}
}

func lateralBindingExecutionSourceReads(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ancestor   bool
		local      bool
		scalar     bool
		selfJoin   bool
		childLocal bool
		wantPrefix int
		wantSlot   int
		wantReads  map[int][]int32
	}{
		{name: "ordinary_local", local: true, wantSlot: 0, wantReads: map[int][]int32{0: {1}}},
		{name: "stripped_prefix_local", ancestor: true, local: true, wantSlot: 0, wantReads: map[int][]int32{0: {}, 1: {1}}},
		{name: "stripped_prefix_scalar", ancestor: true, local: true, scalar: true, wantSlot: 0, wantReads: map[int][]int32{0: {}, 1: {1}}},
		{name: "retained_prefix_only", ancestor: true, wantPrefix: 1, wantSlot: 0, wantReads: map[int][]int32{0: {1}, 1: {}}},
		{name: "retained_prefix_mixed", ancestor: true, local: true, wantPrefix: 1, wantSlot: 1, wantReads: map[int][]int32{0: {1}, 1: {1}}},
		{name: "self_join_occurrences", ancestor: true, local: true, selfJoin: true, wantSlot: 0, wantReads: map[int][]int32{0: {}, 1: {1}}},
		{name: "child_local_same_slot", ancestor: true, local: true, selfJoin: true, childLocal: true, wantSlot: 0, wantReads: map[int][]int32{0: {}, 1: {1}, 2: {1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE lateral_demand_prefix(n integer)")
			enginetest.Run(t, e, "CREATE VIEW lateral_demand_view AS SELECT 7 AS n, 9 AS spare")
			table := func(relation, alias string) parser.FromItem {
				return &parser.AliasReference{Input: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: relation}}}}, Name: parser.Identifier{Text: alias}}
			}
			read := lateralBindingColumn("b", "n")
			if !tc.local {
				read = lateralBindingColumn("a", "n")
			}
			var target parser.Expr = read
			if tc.scalar {
				target = parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{read}}}
			}
			child := lateralBindingDerived(target)
			if tc.wantPrefix != 0 && tc.local {
				child.Query.Targets = append(child.Query.Targets, lateralBindingColumn("a", "n"))
			}
			if tc.childLocal {
				selectChild := child.Query
				selectChild.From = table("lateral_demand_view", "c")
				selectChild.Targets = append(selectChild.Targets, lateralBindingColumn("c", "n"))
			}
			var from parser.FromItem = &parser.JoinReference{Left: table("lateral_demand_view", "b"), Right: child}
			if tc.ancestor {
				relation := "lateral_demand_prefix"
				if tc.selfJoin {
					relation = "lateral_demand_view"
				}
				from = &parser.JoinReference{Left: table(relation, "a"), Right: from}
			}
			_, project := lateralPhysicalBinding(t, e, from)
			join := project.Child.(*plan.JoinNode)
			if tc.ancestor {
				join = join.Right.(*plan.JoinNode)
			}
			derived := join.Right.(*plan.DerivedNode)
			childProject := derived.Query.Root.(*plan.ProjectNode)
			if join.CorrelationPrefix != tc.wantPrefix || join.CorrelatedRight != tc.local || derived.Captures[0].Slot != tc.wantSlot {
				t.Fatal("fixture did not distinguish normalized and full-frame slots", join, derived.Captures)
			}
			localSource := join.Left.(*plan.SourceNode)
			readExpression := childProject.Expressions[0]
			if tc.scalar {
				readExpression = readExpression.(plan.SubqueryExpr).QueryPlan.Root.(*plan.ProjectNode).Expressions[0]
			}
			boundRead := readExpression.(plan.ColumnExpr)
			if boundRead.Slot != tc.wantSlot || boundRead.Frame != join.Frame || boundRead.Origin.Address.SubID != 1 {
				t.Fatal("executable read lost its normalized lexical coordinate", boundRead)
			}
			if tc.local && (boundRead.Origin.Address.ClassID != localSource.Ref.Address.ClassID || boundRead.Origin.Address.ObjectID != localSource.Ref.Address.ObjectID) {
				t.Fatal("executable read lost its local source authority", boundRead, localSource.Ref)
			}
			if tc.childLocal {
				childRead := childProject.Expressions[1].(plan.ColumnExpr)
				if childRead.Slot != boundRead.Slot || childRead.Frame == boundRead.Frame || childRead.Frame != childProject.Frame {
					t.Fatal("child-local equal numeric slot lost its distinct lexical frame", childRead, boundRead)
				}
			}
			prefixWidth := 1
			if tc.selfJoin {
				prefixWidth = 2
			}
			if len(localSource.Schema()) != 2 || (tc.ancestor && project.Child.(*plan.JoinNode).LeftWidth != prefixWidth) {
				t.Fatal("fixture lost its physical source spans")
			}
			var sources []*plan.SourceNode
			var snapshotSources []plan.SourceNode
			var columns []plan.ColumnExpr
			analyze.WalkQueryArgumentNode(project, func(node expr.Expr) {
				if column, ok := node.(plan.ColumnExpr); ok {
					columns = append(columns, column)
				}
			})
			var snapshotNodes func(plan.Node)
			snapshotNodes = func(node plan.Node) {
				if node == nil {
					return
				}
				if source, ok := node.(*plan.SourceNode); ok {
					sources = append(sources, source)
					snapshotSources = append(snapshotSources, *source)
				}
				left, right := plan.QueryInputs(node)
				snapshotNodes(left)
				snapshotNodes(right)
			}
			snapshotNodes(project)
			captures := append([]plan.FunctionCorrelation(nil), derived.Captures...)
			expressions := append([]expr.Expr(nil), childProject.Expressions...)
			captureBacking := &derived.Captures[0]
			expressionBacking := &childProject.Expressions[0]
			reads := analyze.QueryExecutionSourceReads(project, nil)
			if !reflect.DeepEqual(reads, tc.wantReads) {
				t.Fatalf("physical attribute reads by occurrence = %v, want %v", reads, tc.wantReads)
			}
			var afterColumns []plan.ColumnExpr
			analyze.WalkQueryArgumentNode(project, func(node expr.Expr) {
				if column, ok := node.(plan.ColumnExpr); ok {
					afterColumns = append(afterColumns, column)
				}
			})
			if derived.Query.Root != childProject || !reflect.DeepEqual(derived.Captures, captures) || &derived.Captures[0] != captureBacking || &childProject.Expressions[0] != expressionBacking || !reflect.DeepEqual(childProject.Expressions, expressions) || !reflect.DeepEqual(afterColumns, columns) {
				t.Fatal("demand collection rewrote the bound query or captures")
			}
			for i, source := range sources {
				if !reflect.DeepEqual(*source, snapshotSources[i]) {
					t.Fatal("demand collection rewrote physical source authority", source, snapshotSources[i])
				}
			}
		})
	}
}
