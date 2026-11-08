package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryLateralStored(t *testing.T) {
	t.Run("immutable_capture_frame_occurrence_remap", lateralStoredRemap)
	t.Run("derived_written_marker_render", lateralStoredDerived)
	t.Run("wrapped_values_traversal_render", lateralStoredValues)
	t.Run("wrapped_values_literal_validation", lateralStoredValuesValidation)
	t.Run("derived_capture_validation", lateralStoredCaptures)
}

func lateralStoredCatalog(t *testing.T) (catalog.Read, *catalog.Relation, *sqltype.Descriptor) {
	t.Helper()
	state := catalog.NewState(1)
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	if integer == nil {
		t.Fatal("missing integer")
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}, ObjectName: "capture", ObjectRevision: 1}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 901}, ObjectNamespace: ns.Address(), ObjectName: "target", ObjectRevision: 1, Columns: []catalog.Column{{Name: "v", Usage: integer.Usage()}}, Attributes: []int32{1}}
	state.SetDescriptor(ns.Address(), ns)
	state.SetDescriptor(relation.Address(), relation)
	return catalog.NewRead(&catalog.Owner{Domain: new(ids.Domain), Principals: new(authorization.Domain)}, state), relation, integer
}

func lateralStoredLiteral(t *testing.T, typ *sqltype.Descriptor, number int64) plan.LiteralExpr {
	t.Helper()
	value, err := sqltype.Integer(typ, number)
	if err != nil {
		t.Fatal(err)
	}
	return plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
}

func lateralStoredRaw(number int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: number}}
}

func lateralStoredRemap(t *testing.T) {
	read, relation, integer := lateralStoredCatalog(t)
	oid, ok := read.LookupType(sqltype.Oid)
	if !ok {
		t.Fatal("missing OID")
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Pos: parser.Position{Character: 10}}, Scope: 1, Frame: 1}
	identity := plan.SystemAttributeExpr{Info: expr.Info{Usage: oid.Usage(), Pos: parser.Position{Character: 11}}, Owner: read.Domain().Ref(read.Epoch(), relation.Address()), Scope: 1, Frame: 1, QueryOccurrence: 1, SubID: -6}
	nestedColumn := column
	nestedColumn.Pos.Character = 12
	local, ancestor := column, column
	local.Frame, ancestor.Frame = 2, 9
	record, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	presence := plan.RowExpr{Info: expr.Info{Usage: record.Usage(), Pos: parser.Position{Character: 13}}, PresenceFrame: 1, PresenceSlot: 4}
	localPresence := presence
	localPresence.PresenceFrame = 2
	schema := plan.QuerySchema{{Name: "kept", Usage: integer.Usage()}}
	nested := &plan.ProjectNode{Frame: 3, QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{nestedColumn}}
	project := &plan.ProjectNode{Frame: 2, QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{column, identity, plan.SubqueryExpr{Info: column.Info, QueryPlan: &plan.Query{Root: nested}}, local, ancestor, presence, localPresence}}
	captures := []plan.FunctionCorrelation{{Frame: 1, Slot: 0, Name: "prefix", Pos: column.Pos}, {Frame: 1, QueryOccurrence: 1, Name: "zero_width", Pos: identity.Pos}, {Frame: 1, Slot: 0, Name: "nested", Pos: nestedColumn.Pos}, {Frame: 1, PresenceSlot: 4, Name: "outer_presence", Pos: presence.Pos}}
	derived := &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: project}, ScanFrame: 1, Lateral: true, Captures: captures, RangeName: "retained", ColumnAliases: []string{"kept"}}
	join := &plan.JoinNode{Frame: 1, Left: &plan.SourceNode{Occurrence: 0}, Right: derived, CorrelatedRight: true, CorrelationIdentityPrefix: true, Captures: captures}
	mapper := stored.NewRuleTreeMapper(read, func(frame uint32) uint32 { return frame + 10 }, 7, nil)
	root, err := mapper.Query(join)
	if err != nil {
		t.Fatal(err)
	}
	mappedJoin := root.(*plan.JoinNode)
	mapped := mappedJoin.Right.(*plan.DerivedNode)
	child := mapped.Query.Root.(*plan.ProjectNode)
	if mapped.ScanFrame != 11 || child.Frame != 12 || mappedJoin.Frame != 11 || mappedJoin.Left.(*plan.SourceNode).Occurrence != 7 || !mappedJoin.CorrelationIdentityPrefix || !mapped.Lateral {
		t.Fatal("source frames or invocation identity lost", mappedJoin, mapped, child)
	}
	for i, old := range captures {
		want := old
		want.Frame += 10
		if want.QueryOccurrence != 0 {
			want.QueryOccurrence += 7
		}
		if mapped.Captures[i] != want || mappedJoin.Captures[i] != want {
			t.Fatalf("capture %d = %#v / %#v; want %#v", i, mapped.Captures[i], mappedJoin.Captures[i], want)
		}
	}
	mappedIdentity := child.Expressions[1].(plan.SystemAttributeExpr)
	mappedNested := child.Expressions[2].(plan.SubqueryExpr).QueryPlan.Root.(*plan.ProjectNode)
	if mappedIdentity.Frame != 11 || mappedIdentity.QueryOccurrence != 8 || mappedIdentity.Owner != identity.Owner || mappedNested.Frame != 13 || mappedNested.Expressions[0].(plan.ColumnExpr).Frame != 11 || child.Expressions[3].(plan.ColumnExpr).Frame != 12 || child.Expressions[4].(plan.ColumnExpr).Frame != 19 {
		t.Fatal("child-local or lexical-ancestor coordinates lost", child.Expressions)
	}
	mappedPresence, mappedLocalPresence := child.Expressions[5].(plan.RowExpr), child.Expressions[6].(plan.RowExpr)
	if mappedPresence.PresenceFrame != 11 || mappedPresence.PresenceSlot != 4 || mappedLocalPresence.PresenceFrame != 12 || mappedLocalPresence.PresenceSlot != 4 {
		t.Fatal("presence identity or lexical ownership lost", mappedPresence, mappedLocalPresence)
	}
	if !reflect.DeepEqual(mapped.Schema(), []catalog.Column(schema)) || mapped.RangeName != derived.RangeName || !reflect.DeepEqual(mapped.ColumnAliases, derived.ColumnAliases) {
		t.Fatal("immutable source presentation lost", mapped)
	}
	mapped.Captures[0].Slot = 99
	mappedJoin.Captures[1].QueryOccurrence = 99
	if derived.Captures[0].Slot != 0 || derived.Captures[1].QueryOccurrence != 1 || join.Captures[1].QueryOccurrence != 1 || project.Frame != 2 || identity.Frame != 1 || nested.Frame != 3 {
		t.Fatal("remap mutated original capture or query metadata")
	}
	mapped.Captures[3].PresenceSlot = 99
	if derived.Captures[3].PresenceSlot != 4 || presence.PresenceFrame != 1 || localPresence.PresenceFrame != 2 {
		t.Fatal("remap mutated original presence ownership")
	}
	unchanged, err := stored.NewRuleTreeMapper(read, nil, 0, nil).Query(derived)
	if err != nil {
		t.Fatal(err)
	}
	if &unchanged.(*plan.DerivedNode).Captures[0] != &derived.Captures[0] {
		t.Fatal("unchanged immutable capture metadata was copied")
	}
}

// Build through the existing public payload seam; rendering and validation own
// the private source lookup, namespace resolution and literal-frame traversal.
func lateralStoredView(t *testing.T, values bool) (catalog.Read, *stored.View, *plan.DerivedNode) {
	t.Helper()
	read, _, integer := lateralStoredCatalog(t)
	one := lateralStoredLiteral(t, integer, 1)
	schema := plan.QuerySchema{{Name: "v", Usage: integer.Usage()}}
	var source parser.FromItem
	child := &plan.ProjectNode{Frame: 2, QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{one}}
	literals := map[analyze.ViewLiteralPath]plan.LiteralExpr{{Query: 1, Clause: analyze.ViewTargetLiteral}: one}
	queries := make(map[*parser.Select]*plan.ProjectNode)
	var namespaces []analyze.ViewNamespace
	if !values {
		child.Syntax = &parser.Select{Targets: []parser.Expr{lateralStoredRaw(1)}, Labels: []parser.Identifier{{Text: "v"}}}
		queries[child.Syntax] = child
		source = &parser.AliasReference{Input: &parser.DerivedQuery{Query: child.Syntax, Lateral: true}, Name: parser.Identifier{Text: "kept"}}
		literals[analyze.ViewLiteralPath{Query: 2, Clause: analyze.ViewTargetLiteral}] = one
		namespaces = []analyze.ViewNamespace{{Frame: 1, Node: analyze.ViewDerivedNamespace, Ordinal: 2, Qualifiers: []analyze.ViewNamespaceQualifier{{Name: "kept"}}}, {Frame: 1, Node: analyze.ViewDerivedNamespace, Ordinal: 2}}
	} else {
		bigint, ok := read.LookupType(sqltype.Int8)
		if !ok {
			t.Fatal("missing bigint")
		}
		raw := &parser.ValuesQuery{Lateral: true, Rows: [][]parser.Expr{{lateralStoredRaw(2)}, {lateralStoredRaw(1)}}, Order: &parser.Order{Keys: []parser.OrderKey{{Expr: lateralStoredRaw(1)}}}, Bounds: &parser.QueryBounds{Count: lateralStoredRaw(1)}}
		schema[0].Name = "column1"
		two := lateralStoredLiteral(t, integer, 2)
		cells := &plan.ValuesNode{QuerySchema: schema, Rows: [][]expr.Expr{{two}, {one}}, ViewOrdinal: 7}
		projection := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 2}
		sort := &plan.SortNode{QuerySchema: schema, Child: cells, Keys: []plan.SortKey{{Expression: projection}}}
		limit := &plan.LimitNode{QuerySchema: schema, Child: sort, Count: lateralStoredLiteral(t, bigint, 1)}
		child.Child, child.Expressions, child.FrozenValues = limit, []expr.Expr{projection}, true
		source = &parser.AliasReference{Input: raw, Name: parser.Identifier{Text: "f"}, Columns: []parser.Identifier{{Text: "n"}}}
		literals[analyze.ViewLiteralPath{Query: 2, Clause: analyze.ViewValuesCellLiteral, Source: 7, Row: 0}] = two
		literals[analyze.ViewLiteralPath{Query: 2, Clause: analyze.ViewValuesCellLiteral, Source: 7, Row: 1}] = one
		literals[analyze.ViewLiteralPath{Query: 2, Clause: analyze.ViewValuesLimitLiteral, Source: 7}] = limit.Count.(plan.LiteralExpr)
		namespaces = []analyze.ViewNamespace{{Frame: 1, Node: analyze.ViewValuesNamespace, Ordinal: 7, Columns: []analyze.ViewNamespaceColumn{{Name: "n", Slot: 0, Leaf: -1}}, Qualifiers: []analyze.ViewNamespaceQualifier{{Name: "f"}}}, {Frame: 1, Node: analyze.ViewValuesNamespace, Ordinal: 7, Columns: []analyze.ViewNamespaceColumn{{Name: "column1", Slot: 0, Leaf: -1}}}}
	}
	derived := &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: child}, ScanFrame: 1, Lateral: true}
	syntax := &parser.Select{Targets: []parser.Expr{lateralStoredRaw(1)}, From: source}
	root := &plan.ProjectNode{Frame: 1, Syntax: syntax, QuerySchema: plan.QuerySchema{{Name: "unit", Usage: integer.Usage()}}, Child: derived, Expressions: []expr.Expr{one}}
	queries[syntax] = root
	view := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: root}, Syntax: syntax, Frames: map[uint32]*plan.ProjectNode{1: root, 2: child}, Queries: queries, Literals: literals, Namespaces: namespaces})
	return read, view, derived
}

func lateralStoredSource(t *testing.T, text string) *parser.AliasReference {
	t.Helper()
	statements, err := parser.Parse(text)
	if err != nil {
		t.Fatalf("emitted SQL %q: %v", text, err)
	}
	alias, ok := statements[0].(*parser.Select).From.(*parser.AliasReference)
	if !ok {
		t.Fatal("emitted source alias lost", text)
	}
	return alias
}

func lateralStoredDerived(t *testing.T) {
	read, view, derived := lateralStoredView(t, false)
	if err := view.ValidateLiterals(read); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []stored.Presentation{{}, {Deparse: true}, {Deparse: true, Rule: true}, {Deparse: true, Rule: true, Pretty: true}} {
		text, err := view.SelectSQL(context.Background(), read, policy)
		if err != nil {
			t.Fatal(err)
		}
		alias := lateralStoredSource(t, text)
		input, ok := alias.Input.(*parser.DerivedQuery)
		if !ok || !input.Lateral || alias.Name.Text != "kept" {
			t.Fatal("uncorrelated written LATERAL or alias lost", text)
		}
	}
	if len(derived.Captures) != 0 || derived.Query.Root.(*plan.ProjectNode).Frame != 2 || derived.ScanFrame != 1 {
		t.Fatal("render changed immutable source state")
	}
}

func lateralStoredValues(t *testing.T) {
	read, view, derived := lateralStoredView(t, true)
	child := derived.Query.Root.(*plan.ProjectNode)
	if err := stored.ValidateViewLeaves(read, view); err != nil {
		t.Fatal(err)
	}
	check := func(text string) {
		alias := lateralStoredSource(t, text)
		values, ok := alias.Input.(*parser.ValuesQuery)
		if !ok || !values.Lateral || len(values.Rows) != 2 || values.Order == nil || len(values.Order.Keys) != 1 || values.Bounds == nil || values.Bounds.Count == nil {
			t.Fatal("emitted source lost VALUES rows, written LATERAL, order or bounds", text)
		}
	}
	for _, policy := range []stored.Presentation{{}, {Deparse: true}, {Deparse: true, Rule: true}} {
		text, err := view.SelectSQL(context.Background(), read, policy)
		if err != nil {
			t.Fatal(err)
		}
		check(text)
	}
	action := stored.NewRuleAction(stored.RuleActionParts{Syntax: view.Syntax(), Binding: analyze.Binding{Query: view.Query()}, Presentation: view})
	text, err := stored.RuleActionSQL(context.Background(), read, action)
	if err != nil {
		t.Fatal(err)
	}
	check(text)
	if child.Syntax != nil || child.Frame != 2 || !child.FrozenValues || child.Child.(*plan.LimitNode).Child.(*plan.SortNode).Child.(*plan.ValuesNode).ViewOrdinal != 7 {
		t.Fatal("render flattened or mutated the retained VALUES query")
	}
}

func lateralStoredValuesValidation(t *testing.T) {
	for _, corrupt := range []string{"", "missing_frame", "written_marker", "bound_marker", "wrong_literal_frame", "orphan_values_frame"} {
		t.Run(corrupt, func(t *testing.T) {
			read, view, derived := lateralStoredView(t, true)
			parts := view.Parts()
			raw := view.Syntax().From.(*parser.AliasReference).Input.(*parser.ValuesQuery)
			switch corrupt {
			case "missing_frame":
				delete(parts.Frames, 2)
			case "written_marker":
				raw.Lateral = false
			case "bound_marker":
				derived.Lateral = false
			case "wrong_literal_frame":
				for path, literal := range parts.Literals {
					if path.Query != 2 {
						continue
					}
					delete(parts.Literals, path)
					path.Query = 1
					parts.Literals[path] = literal
					break
				}
			case "orphan_values_frame":
				parts.Frames[3] = &plan.ProjectNode{Frame: 3, FrozenValues: true}
			}
			view = stored.NewView(parts)
			err := view.ValidateLiterals(read)
			if (err != nil) != (corrupt != "") {
				t.Fatalf("corruption %q: %v", corrupt, err)
			}
			if corrupt == "" {
				roots, err := view.LiteralRoots()
				if err != nil {
					t.Fatal(err)
				}
				childRoots := 0
				for path := range roots {
					if path.Query == 2 {
						if path.Source != 7 {
							t.Fatal("VALUES literals escaped source ownership", path)
						}
						childRoots++
					} else if path != (analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}) {
						t.Fatal("unexpected literal owner", path)
					}
				}
				if childRoots != 4 || len(roots) != 5 {
					t.Fatal("missing VALUES cell, order, bound or parent root", roots)
				}
			}
		})
	}
}

func lateralStoredCaptures(t *testing.T) {
	read, _, integer := lateralStoredCatalog(t)
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Pos: parser.Position{Character: 8}}, Scope: 1, Frame: 1}
	capture := plan.FunctionCorrelation{Frame: 1, Pos: column.Pos}
	for _, corrupt := range []string{"", "missing", "extra", "frame", "slot", "position", "nonlateral"} {
		t.Run(corrupt, func(t *testing.T) {
			derived := &plan.DerivedNode{ScanFrame: 1, Lateral: true, Captures: []plan.FunctionCorrelation{capture, capture}, Query: &plan.Query{Root: &plan.ProjectNode{Frame: 2, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{column, column}}}}
			switch corrupt {
			case "missing":
				derived.Captures = derived.Captures[:1]
			case "extra":
				derived.Captures = append(derived.Captures, capture)
			case "frame":
				derived.Captures[0].Frame = 2
			case "slot":
				derived.Captures[0].Slot = 1
			case "position":
				derived.Captures[0].Pos.Character++
			case "nonlateral":
				derived.Lateral = false
			}
			view := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: derived}})
			if err := stored.ValidateViewLeaves(read, view); (err != nil) != (corrupt != "") {
				t.Fatalf("corruption %q: %v", corrupt, err)
			}
		})
	}
	record, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []string{"", "missing", "extra", "frame", "presence", "position", "ordinary_mode", "occurrence_mode", "negative"} {
		t.Run("zero_field_presence/"+corrupt, func(t *testing.T) {
			row := plan.RowExpr{Info: expr.Info{Usage: record.Usage(), Pos: parser.Position{Character: 11}}, PresenceFrame: 1, PresenceSlot: 4}
			local := row
			local.PresenceFrame, local.PresenceSlot = 2, 9
			capture := plan.FunctionCorrelation{Frame: 1, PresenceSlot: 4, Pos: row.Pos}
			derived := &plan.DerivedNode{ScanFrame: 1, Lateral: true, Captures: []plan.FunctionCorrelation{capture}, Query: &plan.Query{Root: &plan.ProjectNode{Frame: 2, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{row, local}}}}
			switch corrupt {
			case "missing":
				derived.Captures = nil
			case "extra":
				derived.Captures = append(derived.Captures, capture)
			case "frame":
				derived.Captures[0].Frame = 2
			case "presence":
				derived.Captures[0].PresenceSlot = 5
			case "position":
				derived.Captures[0].Pos.Character++
			case "ordinary_mode":
				derived.Captures[0].Slot = 1
			case "occurrence_mode":
				derived.Captures[0].QueryOccurrence = 1
			case "negative":
				derived.Captures[0].PresenceSlot = -1
			}
			view := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: derived}})
			if err := stored.ValidateViewLeaves(read, view); (err != nil) != (corrupt != "") {
				t.Fatalf("presence corruption %q: %v", corrupt, err)
			}
		})
	}
}
