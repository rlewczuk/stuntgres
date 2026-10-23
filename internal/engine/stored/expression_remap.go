package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement copies the closed bound tree; source and expression identities
// are reminted together. Syntax and frozen layouts are immutable and shared.
func RemapExpression(node expr.Expr, ref func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int) (expr.Expr, error) {
	return RemapExpressionWithQuery(node, ref, slots, func(root plan.Node) (plan.Node, error) {
		return (&ViewQueryRemapper{Ref: ref, Type: func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, referencesOnly: true}).Query(root)
	}, nil)
}

func RemapExpressionWithQuery(node expr.Expr, ref func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int, queryMapper func(plan.Node) (plan.Node, error), rowRef func(ids.ObjectRef) ids.ObjectRef) (expr.Expr, error) {
	if rowRef == nil {
		rowRef = ref
	}
	return RemapScalar(node, ref, rowRef, slots, queryMapper)
}

// Row copies retain explicit object constants and external calls. Replacement
// callers map both roles, whereas LIKE and partition inheritance supply a
// distinct owner-row mapper.
func RemapScalar(node expr.Expr, ref, rowRef func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int, queryMapper func(plan.Node) (plan.Node, error)) (expr.Expr, error) {
	return remapScalar(node, ref, rowRef, slots, queryMapper, 0, nil)
}

// Only immutable routine presentations supply a closed formal namespace.
// Ordinary persisted expressions must continue to reject runtime parameters.
func remapScalar(node expr.Expr, ref, rowRef func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int, queryMapper func(plan.Node) (plan.Node, error), routineFormals int, addressMapper func(ids.ObjectAddress) ids.ObjectAddress) (expr.Expr, error) {
	if node == nil {
		return nil, nil
	}
	var err error
	child := func(old expr.Expr) expr.Expr {
		if err != nil {
			return old
		}
		var next expr.Expr
		next, err = remapScalar(old, ref, rowRef, slots, queryMapper, routineFormals, addressMapper)
		return next
	}
	args := func(old []expr.Expr) []expr.Expr {
		next := make([]expr.Expr, len(old))
		for i, n := range old {
			next[i] = child(n)
		}
		return next
	}
	var next expr.Expr
	switch n := node.(type) {
	case plan.ParameterExpr:
		if n.Number <= 0 || n.Number > routineFormals || n.Usage.Type == nil || !n.Usage.Valid() {
			return nil, fmt.Errorf("stuntgres: unpersistable scalar expression %T", node)
		}
		// Retagging belongs to the routine recipe's existing type mapper.
		next = n
	case plan.SQLRoutineExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.AssignmentValueExpr:
		n.Base, n.Value = child(n.Base), child(n.Value)
		targets, failure := MapRuleAssignmentTargets([]*plan.ArrayAssignmentTarget{n.Target}, func(expr expr.Expr) (expr.Expr, bool, error) {
			if expr == nil {
				return nil, false, nil
			}
			return child(expr), true, err
		}, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
		if failure != nil {
			return nil, failure
		}
		n.Target = targets[0]
		next = n
	case plan.UnknownOutput:
		// Captured contextual analysis carries immutable literal syntax only;
		// it is not a persisted scalar value or a remappable object reference.
		literal, ok := n.Raw.(parser.LiteralExpr)
		if !ok || n.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
			return nil, fmt.Errorf("stuntgres: invalid contextual view output")
		}
		next = n
	case plan.RowExpr:
		n, _, err = plan.MapRowAnalysis(n, func(old expr.Expr) (expr.Expr, bool, error) { next := child(old); return next, true, err })
		next = n
	case plan.FieldExpr:
		n.Base = child(n.Base)
		next = n
	case plan.ArrayExpr:
		n.Elements = args(n.Elements)
		next = n
	case plan.SubscriptExpr:
		n, _, err = plan.MapSubscript(n, func(old expr.Expr) (expr.Expr, bool, error) {
			if old == nil {
				return nil, false, nil
			}
			next := child(old)
			return next, true, err
		})
		next = n
	case plan.SubqueryExpr:
		if queryMapper != nil {
			root, failure := queryMapper(n.QueryPlan.Root)
			if failure != nil {
				return nil, failure
			}
			copy := *n.QueryPlan
			copy.Root = root
			n.QueryPlan = &copy
		}
		n.Comparisons = append([]plan.ComparisonExpr(nil), n.Comparisons...)
		for i, c := range n.Comparisons {
			mapped := child(c)
			if err != nil {
				return nil, err
			}
			n.Comparisons[i] = mapped.(plan.ComparisonExpr)
		}
		n.LeftExpansions, _, err = plan.MapRowComparisonExpansions(n.LeftExpansions, func(old expr.Expr) (expr.Expr, bool, error) {
			next := child(old)
			return next, true, err
		})
		if n.LeftConstructor != nil {
			n.LeftConstructor = child(n.LeftConstructor)
		}
		next = n
	case plan.UpdateOutputExpr:
		next = n
	case plan.EarthCheckExpr:
		if !n.Valid() {
			return nil, fmt.Errorf("stuntgres: invalid remapped earth CHECK")
		}
		mapped := child(n.Value)
		if err != nil {
			return nil, err
		}
		value, ok := mapped.(plan.DomainValueExpr)
		if !ok {
			return nil, fmt.Errorf("stuntgres: foreign earth CHECK child")
		}
		n.Value = value
		next = n
	case plan.DomainValueExpr:
		next = n
	case plan.LiteralExpr:
		if ref == nil {
			next = n
			break
		}
		if n.RegclassRef != (ids.ObjectRef{}) {
			n.RegclassRef = ref(n.RegclassRef)
		}
		if n.Usage.Type.ContainsRegclass() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = regobj.RemapClassDatum(n.Value, func(id ids.OID) ids.OID {
				return remapAddress(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ref, addressMapper).ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if (n.Usage.Type.ContainsRegtype() || n.Usage.Type.ContainsRegObject()) && !n.Value.IsNull() {
			var failure error
			n.Value, failure = regobj.RemapAliasDatum(n.Value, func(address ids.ObjectAddress) ids.ObjectAddress {
				return remapAddress(address, ref, addressMapper)
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsRegproc() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = regobj.RemapRoutineDatum(n.Value, func(id ids.OID) ids.OID {
				return remapAddress(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}, ref, addressMapper).ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsOperatorAlias() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = regobj.RemapOperatorDatum(n.Value, func(id ids.OID) ids.OID {
				return remapAddress(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}, ref, addressMapper).ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsRegTS() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = regobj.RemapTextSearchDatum(n.Value, func(address ids.ObjectAddress) ids.ObjectAddress {
				return remapAddress(address, ref, addressMapper)
			})
			if failure != nil {
				return nil, failure
			}
		}
		next = n
	case plan.TableOIDExpr:
		n.Owner = rowRef(n.Owner)
		next = n
	case plan.ReturningColumnExpr:
		mapped := child(n.ColumnExpr)
		if err != nil {
			return nil, err
		}
		column, ok := mapped.(plan.ColumnExpr)
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid remapped RETURNING column")
		}
		n.ColumnExpr = column
		n.Value = child(n.Value)
		next = n
	case plan.ReturningWholeRowExpr:
		n.Members = args(n.Members)
		next = n
	case plan.MergeActionExpr:
		next = n
	case plan.ColumnExpr:
		n.Origin = rowRef(n.Origin)
		if ref == nil && slots != nil {
			// Owner-row copies carry attribute identity only. CREATE attaches
			// the completed destination's recipe and full layout afterward.
			n.Virtual = nil
			n.RowStart, n.RowWidth, n.QueryOccurrence = 0, 0, 0
		} else {
			n.Virtual = child(n.Virtual)
		}
		if slots != nil {
			slot, ok := slots[n.Origin.Address.SubID]
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing remapped row attribute")
			}
			n.Slot = slot
			if n.Virtual != nil {
				n.RowStart = 0
				n.RowWidth = len(slots)
			}
		}
		next = n
	case plan.GroupExpr:
		next = n
	case plan.AggregateExpr:
		next = n
	case plan.GroupingExpr:
		n.Args = args(n.Args)
		next = n
	case *plan.WindowExpr:
		copy := *n
		next = &copy
	case plan.CachedOrderExpr:
		next = n // The cache slot belongs to this immutable query plan.
	case plan.CollationExpr:
		n.Child = child(n.Child)
		next = n
	case plan.CoercionExpr:
		n.Child = child(n.Child)
		next = n
	case plan.BitwiseExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case plan.SelectedOperatorExpr:
		n.Provider = child(n.Provider)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.ArithmeticExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case plan.UnnestExpr:
		n.Args = args(n.Args)
		if ref != nil && n.Procedure != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.LikeExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case plan.RowComparisonExpr:
		n.Comparisons = append([]plan.ComparisonExpr(nil), n.Comparisons...)
		for i, pair := range n.Comparisons {
			n.Comparisons[i] = child(pair).(plan.ComparisonExpr)
		}
		if err != nil {
			return nil, err
		}
		for side, expansions := range n.Expansions {
			n.Expansions[side], _, err = plan.MapRowComparisonExpansions(expansions, func(old expr.Expr) (expr.Expr, bool, error) {
				next := child(old)
				return next, true, err
			})
			if err != nil {
				return nil, err
			}
		}
		next = n
	case plan.ScalarArrayComparisonExpr:
		n.Left = child(n.Left)
		if n.Array != nil {
			n.Array = child(n.Array)
		}
		n.Items = append([]expr.Expr(nil), n.Items...)
		for i := range n.Items {
			n.Items[i] = child(n.Items[i])
		}
		next = n
	case plan.ComparisonExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case plan.NullTestExpr:
		n.Child = child(n.Child)
		next = n
	case plan.TruthTestExpr:
		n.Child = child(n.Child)
		next = n
	case plan.DistinctExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case plan.LogicalExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case plan.ConditionalExpr:
		n.Args = args(n.Args)
		next = n
	case plan.ExtensionPathsExpr:
		n.Arg = child(n.Arg)
		next = n
	case plan.InstalledCubeExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.InstalledSoundexExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		if err == nil {
			n.Soundex, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case plan.InstalledMetaphoneExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		if err == nil {
			n.Metaphone, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case plan.InstalledLevenshteinExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		if err == nil {
			n.Levenshtein, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case plan.InstalledUUIDOsspExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		if err == nil {
			n.UUIDGeneration, err = n.Reconstruct(n.Info, n.Args, nil)
		}
		next = n
	case plan.InstalledCryptoExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		if err == nil {
			n.Pgcrypto, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case plan.InstalledTextExpr:
		n.Arg = child(n.Arg)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.InstalledPgrowlocksExpr:
		n.Arg = child(n.Arg)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case functions.SequenceCall:
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		n.Args = args(n.Args)
		next = n
	case plan.CaseExpr:
		n.Test, n.Otherwise = child(n.Test), child(n.Otherwise)
		n.Arms = append([]plan.CaseBranch(nil), n.Arms...)
		for i := range n.Arms {
			n.Arms[i].Test = child(n.Arms[i].Test)
			n.Arms[i].Condition, n.Arms[i].Result = child(n.Arms[i].Condition), child(n.Arms[i].Result)
		}
		next = n
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence. Remap
		// copies the whole tree, so every child counts as replaced.
		next, _, err = expr.Map(n, func(old expr.Expr) (expr.Expr, bool, error) {
			next, failure := remapScalar(old, ref, rowRef, slots, queryMapper, routineFormals, addressMapper)
			return next, true, failure
		})
		if err == nil && ref != nil {
			next = remapQuoteOwner(next, ref)
		}
	default:
		return nil, fmt.Errorf("stuntgres: unpersistable scalar expression %T", node)
	}
	return next, err
}

// Schema origins and alias datums contain projected addresses, not retained
// ObjectRef authority. Routine replacement supplies their distinct mapper so
// actual captures still pass the checked provenance boundary.
func remapAddress(address ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, addressMapper func(ids.ObjectAddress) ids.ObjectAddress) ids.ObjectAddress {
	if addressMapper != nil {
		return addressMapper(address)
	}
	return ref(ids.ObjectRef{Address: address}).Address
}

func RemapViewQuery(view catalog.Read, node plan.Node, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) (plan.Node, error) {
	typ = analyze.RecordTypeMapper(typ, collations...)
	return remapViewQueryMapped(view, node, ref, typ, collations...)
}

func remapViewQueryMapped(view catalog.Read, node plan.Node, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) (plan.Node, error) {
	var collation typedef.CollationRemapper
	if len(collations) != 0 {
		collation = collations[0]
	}
	mapper := &ViewQueryRemapper{Catalog: view, Ref: ref, Type: typ, Collation: collation}
	if _, ok := node.(*plan.ProjectNode); ok {
		return mapper.Nested(node)
	}
	return mapper.Query(node)
}

type ViewQueryRemapper struct {
	Ref            func(ids.ObjectRef) ids.ObjectRef
	Type           func(*sqltype.Descriptor) *sqltype.Descriptor
	queries        map[*plan.ProjectNode]plan.Node
	Collation      typedef.CollationRemapper
	Catalog        catalog.Read
	referencesOnly bool
	ctes           map[*plan.CTEDefinition]*plan.CTEDefinition
	Mutation       func(*plan.CTEDefinition) (analyze.Binding, error)
	routineFormals int // Private provenance from a frozen routine presentation.
	addressMapper  func(ids.ObjectAddress) ids.ObjectAddress
}

func (m *ViewQueryRemapper) Nested(node plan.Node) (plan.Node, error) {
	project, ok := node.(*plan.ProjectNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing remapped query frame")
	}
	if root := m.queries[project]; root != nil {
		return root, nil
	}
	if m.queries == nil {
		m.queries = make(map[*plan.ProjectNode]plan.Node)
	}
	// Register the query identity before recursive-definition remapping can
	// encounter its canonical project again. Self scans remain opaque leaves.
	copy := *project
	m.queries[project] = &copy
	root, err := m.Query(node)
	if err != nil {
		delete(m.queries, project)
		return nil, err
	}
	copy = *root.(*plan.ProjectNode)
	return &copy, nil
}

func (m *ViewQueryRemapper) Query(node plan.Node) (plan.Node, error) {
	ref, typ := m.Ref, m.Type
	schema := append(plan.QuerySchema(nil), node.Schema()...)
	for i := range schema {
		schema[i].Usage.Type = typ(schema[i].Usage.Type)
		if m.Collation != nil {
			capability, err := m.Collation(schema[i].Usage.Collation)
			if err != nil {
				return nil, err
			}
			schema[i].Usage.Collation = capability
		}
		if schema[i].OriginTable != 0 {
			origin := remapAddress(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(schema[i].OriginTable)}, ref, m.addressMapper)
			schema[i].OriginTable = uint32(origin.ObjectID)
		}
	}
	var err error
	child := func(old plan.Node) plan.Node {
		if err != nil {
			return old
		}
		var next plan.Node
		next, err = m.Query(old)
		return next
	}
	expression := func(old expr.Expr) expr.Expr {
		if err != nil {
			return old
		}
		next, failure := remapScalar(old, ref, ref, nil, m.Nested, m.routineFormals, m.addressMapper)
		if failure != nil {
			err = failure
			return old
		}
		if !m.referencesOnly {
			// Nested owns both reference and type replacement. Its output is
			// opaque to this scalar-only type pass, even for routine formals.
			next, _, failure = RetagTypesWithQuery(m.Catalog, next, typ, nil, m.Collation)
			if failure != nil {
				err = failure
				return old
			}
		}
		return next
	}
	expressions := func(old []expr.Expr) []expr.Expr {
		next := make([]expr.Expr, len(old))
		for i, n := range old {
			next[i] = expression(n)
		}
		return next
	}
	var next plan.Node
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Calls = append([]expr.Expr(nil), n.Calls...)
		for i, call := range copy.Calls {
			next := expression(call)
			if err == nil {
				copy.Calls[i] = next
			}
		}
		next = &copy
	case *plan.SetProjectNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Scalars = expressions(n.Scalars)
		if n.Template != nil {
			template := *n.Template
			template.Expressions, template.Origins, template.Scalars = expressions(n.Template.Expressions), expressions(n.Template.Origins), expressions(n.Template.Scalars)
			copy.Template = &template
		}
		copy.Calls = append([]expr.Expr(nil), n.Calls...)
		for i, call := range copy.Calls {
			next := expression(call)
			if err == nil {
				copy.Calls[i] = next
			}
		}
		next = &copy
	case *plan.ValuesNode:
		rows := make([][]expr.Expr, len(n.Rows))
		for i, row := range n.Rows {
			rows[i] = expressions(row)
		}
		next = &plan.ValuesNode{QuerySchema: schema, Rows: rows, ViewOrdinal: n.ViewOrdinal, IdentityOrderDemand: append([]bool(nil), n.IdentityOrderDemand...), PresenceSlot: n.PresenceSlot}
	case *plan.LimitNode:
		copy := &plan.LimitNode{QuerySchema: schema, Child: child(n.Child), Offset: expression(n.Offset), Count: expression(n.Count), WithTies: n.WithTies, PresenceOnly: n.PresenceOnly}
		if err == nil {
			copy.FinishTies(copy.Child)
		}
		next = copy
	case *plan.EvaluationNode:
		next = &plan.EvaluationNode{QuerySchema: schema, Child: child(n.Child), Expressions: expressions(n.Expressions), Base: n.Base}
	case *plan.RowLockNode:
		next = &plan.RowLockNode{QuerySchema: schema, Child: child(n.Child), Marks: n.Marks, FirstMode: n.FirstMode}
	case *plan.SourceNode:
		next = &plan.SourceNode{QuerySchema: schema, Ref: ref(n.Ref), Occurrence: n.Occurrence, PresenceSlot: n.PresenceSlot}
	case *plan.JoinNode:
		next = &plan.JoinNode{QuerySchema: schema, Kind: n.Kind, Left: child(n.Left), Right: child(n.Right), Predicate: expression(n.Predicate), LeftWidth: n.LeftWidth, RightWidth: n.RightWidth, Merges: expressions(n.Merges), ViewOrdinal: n.ViewOrdinal, CorrelatedRight: n.CorrelatedRight, Frame: n.Frame, CorrelationPrefix: n.CorrelationPrefix, Captures: n.Captures, PresenceSlot: n.PresenceSlot}
	case *plan.SingletonNode:
		next = &plan.SingletonNode{QuerySchema: schema}
	case *plan.WorktableScanNode:
		copy := *n
		copy.QuerySchema = schema
		copy.CTE, err = m.CTE(n.CTE)
		next = &copy
	case *plan.DerivedNode:
		copy := *n
		copy.QuerySchema = schema
		if n.CTE != nil {
			copy.CTE, err = m.CTE(n.CTE)
		}
		if err == nil && n.Query != nil {
			query := *n.Query
			query.Root, err = m.Nested(query.Root)
			copy.Query = &query
		}
		next = &copy
	case *plan.ProjectNode:
		next = &plan.ProjectNode{QuerySchema: schema, Child: child(n.Child), Expressions: expressions(n.Expressions), AnalysisExpressions: expressions(n.AnalysisExpressions), FrozenValues: n.FrozenValues, Frame: n.Frame, Syntax: n.Syntax, CTEOwner: n.CTEOwner, HasTargetSet: n.HasTargetSet}
	case *plan.SetNode, *plan.RecursiveUnionNode:
		set := QuerySetNode(node)
		copy := *set
		copy.QuerySchema = schema
		var definition *plan.CTEDefinition
		if recursive, ok := node.(*plan.RecursiveUnionNode); ok {
			definition, err = m.CTE(recursive.Definition)
		}
		if err == nil {
			var left plan.Node
			left, err = m.Nested(set.Left)
			if err == nil {
				copy.Left = left.(*plan.ProjectNode)
			}
		}
		if err == nil {
			var right plan.Node
			right, err = m.Nested(set.Right)
			if err == nil {
				copy.Right = right.(*plan.ProjectNode)
			}
		}
		copy.LeftExpressions = expressions(set.LeftExpressions)
		copy.RightExpressions = expressions(set.RightExpressions)
		copy.Grouping = nil
		if !(copy.Operation == "union" && copy.All) {
			width := len(set.Grouping)
			copy.Grouping = make([]plan.Grouping, width)
			for i := range copy.Grouping {
				slot := i
				if definition != nil && definition.Cycle != nil && i == definition.UserWidth {
					slot = definition.Cycle.MarkSlot
				}
				copy.Grouping[i] = set.Grouping[i]
				copy.Grouping[i].Routine = ref(set.Grouping[i].Routine)
				if err == nil {
					copy.Grouping[i].Grouping, err = sqltype.BindGrouping(schema[slot].Usage)
				}
			}
		}
		if definition != nil {
			cycle := definition.Cycle
			if original := node.(*plan.RecursiveUnionNode).Cycle; original != nil && original != node.(*plan.RecursiveUnionNode).Definition.Cycle {
				cycle, err = m.cycle(original)
			}
			next = &plan.RecursiveUnionNode{SetNode: copy, Definition: definition, Search: definition.Search, Cycle: cycle}
		} else {
			next = &copy
		}
	case *plan.DistinctNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Expressions = expressions(n.Expressions)
		copy.Grouping = make([]plan.Grouping, len(copy.Expressions))
		for i, expression := range copy.Expressions {
			copy.Grouping[i] = n.Grouping[i]
			copy.Grouping[i].Routine = ref(n.Grouping[i].Routine)
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *plan.DistinctOnNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Expressions = expressions(n.Expressions)
		copy.Grouping = make([]plan.Grouping, len(copy.Expressions))
		for i, expression := range copy.Expressions {
			copy.Grouping[i] = n.Grouping[i]
			copy.Grouping[i].Routine = ref(n.Grouping[i].Routine)
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *plan.FilterNode:
		next = &plan.FilterNode{QuerySchema: schema, Child: child(n.Child), Predicate: expression(n.Predicate)}
	case *plan.SortNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Keys = append([]plan.SortKey(nil), n.Keys...)
		copy.Outputs = expressions(n.Outputs)
		for i, key := range copy.Keys {
			copy.Keys[i].Expression = expression(key.Expression)
			if err == nil && !copy.Keys[i].Expression.CollationIndeterminate() {
				copy.Keys[i].Comparison, err = sqltype.BindOrdering(copy.Keys[i].Expression.ResultUsage())
			}
		}
		next = &copy
	case *plan.WindowNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Specs = append([]plan.WindowSpec(nil), n.Specs...)
		for i, spec := range copy.Specs {
			spec.Partition = expressions(spec.Partition)
			spec.Grouping = make([]plan.Grouping, len(spec.Partition))
			for j, key := range spec.Partition {
				spec.Grouping[j] = n.Specs[i].Grouping[j]
				spec.Grouping[j].Routine = ref(n.Specs[i].Grouping[j].Routine)
				if err == nil {
					spec.Grouping[j].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
				}
			}
			spec.Order = append([]plan.SortKey(nil), spec.Order...)
			for j, key := range spec.Order {
				spec.Order[j].Expression = expression(key.Expression)
				spec.Order[j].EqualityRoutine = ref(key.EqualityRoutine)
				if err == nil {
					spec.Order[j].Comparison, err = sqltype.BindOrdering(spec.Order[j].Expression.ResultUsage())
				}
			}
			for _, bound := range []*plan.WindowBound{&spec.Frame.Start, &spec.Frame.End} {
				bound.Offset = expression(bound.Offset)
				if err == nil && spec.Frame.Unit == parser.FrameRange {
					bound.Support, err = bound.Support.Remap(m.Type)
				}
			}
			copy.Specs[i] = spec
		}
		copy.Calls = append([]plan.WindowCall(nil), n.Calls...)
		for i, call := range copy.Calls {
			copy.Calls[i].Routine = ref(call.Routine)
			copy.Calls[i].Expression = expression(call.Expression).(*plan.WindowExpr)
			copy.Calls[i].Argument = expression(call.Argument)
			copy.Calls[i].Ordinal = expression(call.Ordinal)
			copy.Calls[i].DefaultValue = expression(call.DefaultValue)
			if provider, ok := call.Provider.(functions.RemappableProvider); ok && err == nil {
				err = m.provider(&copy.Calls[i].Provider, provider, nil)
			}
		}
		next = &copy
	case *plan.AggregateNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Keys = expressions(n.Keys)
		copy.Grouping = make([]plan.Grouping, len(copy.Keys))
		for i, key := range copy.Keys {
			copy.Grouping[i] = n.Grouping[i]
			copy.Grouping[i].Routine = ref(n.Grouping[i].Routine)
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
			}
		}
		copy.Calls = make([]plan.AggregateCall, len(n.Calls))
		for i, call := range n.Calls {
			copy.Calls[i] = call
			if call.Procedure != nil && ref != nil {
				copy.Calls[i].Ref = ref(call.Ref)
				if !m.referencesOnly {
					proc, ok := m.Catalog.Procedure(copy.Calls[i].Ref.Address)
					if !ok {
						return nil, fmt.Errorf("stuntgres: missing remapped intagg aggregate")
					}
					copy.Calls[i].Procedure = proc
					if proc.Aggregate != nil && proc.DynamicCallable() {
						if failure := routine.ValidateAggregateDefinition(m.Catalog, proc); failure != nil {
							return nil, failure
						}
					} else if failure := extension.ValidateIntaggIdentity(m.Catalog, proc, copy.Calls[i].Ref, "int_array_aggregate"); failure != nil {
						return nil, failure
					}
				}
			}
			copy.Calls[i].Routine = ref(call.Routine)
			if len(call.RoutineAliases) != 0 {
				copy.Calls[i].RoutineAliases = make([]ids.ObjectRef, len(call.RoutineAliases))
				for j, alias := range call.RoutineAliases {
					copy.Calls[i].RoutineAliases[j] = ref(alias)
				}
			}
			provider, ok := call.Provider.(functions.RemappableProvider)
			if !ok {
				return nil, fmt.Errorf("stuntgres: unpersistable view aggregate")
			}
			if err == nil {
				var first expr.Expr
				if len(call.Arguments) > 0 {
					first = call.Arguments[0]
				}
				err = m.provider(&copy.Calls[i].Provider, provider, first)
			}
			copy.Calls[i].Arguments = expressions(call.Arguments)
			copy.Calls[i].Direct = expressions(call.Direct)
			if call.Filter != nil {
				copy.Calls[i].Filter = expression(call.Filter)
			}
			copy.Calls[i].Distinct = make([]plan.Grouping, len(call.Distinct))
			for k := range call.Distinct {
				copy.Calls[i].Distinct[k] = call.Distinct[k]
				copy.Calls[i].Distinct[k].Routine = ref(call.Distinct[k].Routine)
				arg := copy.Calls[i].Arguments[k]
				if err == nil {
					copy.Calls[i].Distinct[k].Grouping, err = sqltype.BindGrouping(arg.ResultUsage())
				}
				if err == nil {
					_, err = functions.AggregateOrdering(arg.ResultUsage(), arg.SourcePosition(), true)
				}
			}
			copy.Calls[i].Order = append([]plan.SortKey(nil), call.Order...)
			for k, key := range call.Order {
				copy.Calls[i].Order[k].Expression = expression(key.Expression)
				if err == nil {
					usage := copy.Calls[i].Order[k].Expression.ResultUsage()
					if key.Slot >= 0 && key.Slot < len(call.Arguments) {
						usage = copy.Calls[i].Arguments[key.Slot].ResultUsage()
					} else if key.Slot >= len(call.Arguments) {
						usage = copy.Calls[i].Order[key.Slot-len(call.Arguments)].Expression.ResultUsage()
					}
					copy.Calls[i].Order[k].Comparison, err = functions.AggregateOrdering(usage, key.Expression.SourcePosition(), false)
				}
			}
		}
		next = &copy
	default:
		return nil, fmt.Errorf("stuntgres: unpersistable view query %T", node)
	}
	return next, err
}

// usage maps a usage's type and collation to their remapped generations.
func (m *ViewQueryRemapper) usage(old sqltype.Usage) (sqltype.Usage, error) {
	old.Type = m.Type(old.Type)
	if m.Collation == nil {
		return old, nil
	}
	var err error
	old.Collation, err = m.Collation(old.Collation)
	return old, err
}

// provider replaces *slot with the remapped provider. On failure it keeps the
// slot unchanged and returns the error.
func (m *ViewQueryRemapper) provider(slot *functions.AggregateProvider, provider functions.RemappableProvider, argument expr.Expr) error {
	remap := functions.ProviderRemap{Type: m.Type, Usage: m.usage}
	remap.Routine = func(reference expr.RoutineReference) (expr.RoutineReference, error) {
		old, ok := reference.(*catalog.Procedure)
		if !ok || old == nil {
			return nil, fmt.Errorf("stuntgres: invalid transition routine reference")
		}
		if m.referencesOnly {
			return old, nil
		}
		address := remapAddress(old.Address(), m.Ref, m.addressMapper)
		next, ok := m.Catalog.Procedure(address)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing remapped aggregate support declaration")
		}
		return next, nil
	}
	remap.Expression = m.transitionExpression
	if argument != nil {
		remap.Argument = argument.ResultUsage()
	}
	next, err := provider.Remap(remap)
	if err != nil {
		return err
	}
	if transition, ok := next.(*exec.TransitionAggregate); ok && !m.referencesOnly {
		if err := validateTransitionFrame(m.Catalog, transition); err != nil {
			return err
		}
	}
	*slot = next
	return nil
}

// Transition support expressions own a separate, closed value frame. Its typed
// parameters are not the enclosing query/routine's external parameter namespace.
func (m *ViewQueryRemapper) transitionExpression(old expr.Expr, source, destination []sqltype.Usage) (expr.Expr, error) {
	if len(source) != len(destination) {
		return nil, fmt.Errorf("stuntgres: mismatched transition formal namespace")
	}
	for i := range source {
		if source[i].Type == nil || !source[i].Type.Concrete() || !source[i].Valid() || destination[i].Type == nil || !destination[i].Type.Concrete() || !destination[i].Valid() {
			return nil, fmt.Errorf("stuntgres: invalid transition formal usage")
		}
	}
	if err := validateRoutineScalar(old, source, catalog.Read{}); err != nil {
		return nil, err
	}
	next, err := remapScalar(old, m.Ref, m.Ref, nil, m.Nested, len(source), m.addressMapper)
	if err != nil {
		return nil, err
	}
	if !m.referencesOnly {
		next, _, err = RetagTypesWithQuery(m.Catalog, next, m.Type, nil, m.Collation)
		if err != nil {
			return nil, err
		}
	}
	if err := validateRoutineScalar(next, destination, catalog.Read{}); err != nil {
		return nil, err
	}
	return next, nil
}

func validateTransitionFrame(read catalog.Read, p *exec.TransitionAggregate) error {
	if err := routine.ValidateAggregateDefinition(read, p.Aggregate); err != nil {
		return err
	}
	invalid := func() error { return fmt.Errorf("stuntgres: mismatched transition frame specialization") }
	inputs, err := routine.AggregateSignature(read, p.Aggregate.Signature)
	if err != nil || len(p.Inputs) != len(inputs)+1 || p.Inputs[0] != p.Usage || p.Usage.Type == nil || !p.Usage.Valid() || p.Usage.Type.ID() != sqltype.ID(p.Aggregate.Result) {
		return invalid()
	}
	if p.Support.Address().ObjectID != p.Aggregate.Aggregate.Definition.Functions[0] {
		return invalid()
	}
	for i, input := range inputs {
		if p.Inputs[i+1].Type != input.Type {
			return invalid()
		}
	}
	formals, result, err := routine.AggregateSupportTypes(read, nil, p.Support, p.Aggregate.Aggregate, p.Inputs)
	if err != nil || result.Type != p.Usage.Type || len(formals) != len(p.Formals) {
		return invalid()
	}
	for i, formal := range formals {
		if formal != p.Formals[i] {
			return invalid()
		}
	}
	return nil
}

// Ordinary descriptor evolution reowns only consumers of changed type or
// callable carriers; replacement additionally remaps imported references.
func ExpressionDescriptorsChanged(view catalog.Read, d *catalog.Relation, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	typ = analyze.RecordTypeMapper(typ)
	changed := false
	expression := func(node expr.Expr) {
		analyze.WalkQueryArgumentExpression(node, func(child expr.Expr) {
			old := child.ResultUsage().Type
			changed = changed || old != typ(old)
			if view.State() != nil {
				captured, err := exec.ExpressionRoutineDescriptor(child)
				if err != nil {
					changed = true
				} else if captured != nil {
					current, _ := view.Procedure(captured.Address())
					changed = changed || !exec.SameRoutineDeclaration(current, captured)
				}
			}
		})
	}
	for _, definition := range d.ColumnMeta.Defaults {
		expression(definition.Expression)
	}
	for _, definition := range d.ColumnMeta.Generations {
		expression(definition.Expression)
		expression(definition.Planned)
	}
	for _, constraint := range d.Constraints.Check {
		expression(constraint.Expression)
	}
	for _, policy := range d.Security.Policies {
		for _, condition := range []*Policy{PolicyOf(policy.Using), PolicyOf(policy.Check)} {
			if condition != nil {
				expression(condition.expression)
				changed = changed || analyze.QueryDescriptorsChanged(condition.presentation.query.Root, typ) || analyze.QueryRoutineDeclarationsChanged(view, condition.presentation.query.Root)
				for _, usage := range condition.presentation.castUsages {
					changed = changed || usage.Type != typ(usage.Type)
				}
				for _, captured := range condition.presentation.composites {
					changed = changed || captured.Base.Type != typ(captured.Base.Type)
				}
			}
		}
	}
	if d.SQLIndex != nil {
		for _, key := range d.SQLIndex.Keys {
			if key.Derived != nil {
				expression(key.Derived.Definition)
				expression(key.Derived.Execution)
			}
		}
		if predicate := d.SQLIndex.Predicate; predicate != nil {
			expression(predicate.Definition)
			expression(predicate.Execution)
		}
	}
	if d.Partition != nil {
		for _, key := range d.Partition.Keys {
			changed = changed || key.Usage.Type != typ(key.Usage.Type)
			if key.Derived != nil {
				expression(key.Derived.Definition)
				expression(key.Derived.Execution)
			}
		}
	}
	if d.Membership != nil {
		for _, tuple := range [][]catalog.PartitionDatum{d.Membership.Lower, d.Membership.Upper, d.Membership.Values} {
			for _, datum := range tuple {
				if datum.Kind == 0 {
					changed = changed || datum.Value.Type() != typ(datum.Value.Type())
				}
			}
		}
	}
	if d.View != nil {
		for _, usage := range ViewOf(d).castUsages {
			changed = changed || usage.Type != typ(usage.Type)
		}
		for _, captured := range ViewOf(d).composites {
			changed = changed || captured.Base.Type != typ(captured.Base.Type)
		}
		for _, literal := range ViewOf(d).literals {
			expression(literal)
		}
		changed = changed || analyze.QueryDescriptorsChanged(ViewOf(d).query.Root, typ) || analyze.QueryRoutineDeclarationsChanged(view, ViewOf(d).query.Root)
	}
	return changed
}
func (m *ViewQueryRemapper) CTE(old *plan.CTEDefinition) (*plan.CTEDefinition, error) {
	if mapped := m.ctes[old]; mapped != nil {
		return mapped, nil
	}
	if m.ctes == nil {
		m.ctes = make(map[*plan.CTEDefinition]*plan.CTEDefinition)
	}
	copy := *old
	m.ctes[old] = &copy
	copy.Schema = append(plan.QuerySchema(nil), old.Schema...)
	for i := range copy.Schema {
		copy.Schema[i].Usage.Type = m.Type(copy.Schema[i].Usage.Type)
		if m.Collation != nil {
			collation, err := m.Collation(copy.Schema[i].Usage.Collation)
			if err != nil {
				return nil, err
			}
			copy.Schema[i].Usage.Collation = collation
		}
	}
	if old.Search != nil {
		var err error
		copy.Search, err = m.search(old.Search)
		if err != nil {
			return nil, err
		}
	}
	if old.Cycle != nil {
		var err error
		copy.Cycle, err = m.cycle(old.Cycle)
		if err != nil {
			return nil, err
		}
	}
	if old.Query != nil {
		query := *old.Query
		root, err := m.Nested(query.Root)
		if err != nil {
			return nil, err
		}
		query.Root = root
		copy.Query = &query
	}
	if old.Mutation != nil && m.Mutation != nil {
		body, err := m.Mutation(old)
		if err != nil {
			return nil, err
		}
		copy.Mutation = &body
	}
	return &copy, nil
}

func (m *ViewQueryRemapper) cycle(old *plan.CycleRecipe) (*plan.CycleRecipe, error) {
	copy := *old
	copy.Record, copy.Path = m.Type(old.Record), m.Type(old.Path)
	if !sqltype.OccurrenceRecordArray(copy.Path) || copy.Path.Element() != copy.Record {
		return nil, ids.ErrCatalogReplaced
	}
	expression := func(old expr.Expr) (expr.Expr, error) {
		next, err := remapScalar(old, m.Ref, m.Ref, nil, m.Nested, m.routineFormals, m.addressMapper)
		if err != nil {
			return nil, err
		}
		if !m.referencesOnly {
			next, _, err = RetagTypesWithQuery(m.Catalog, next, m.Type, nil, m.Collation)
		}
		return next, err
	}
	var err error
	copy.Mark, err = expression(old.Mark)
	if err != nil {
		return nil, err
	}
	copy.Fallback, err = expression(old.Fallback)
	if err != nil {
		return nil, err
	}
	if m.referencesOnly {
		return &copy, nil
	}
	for i, usage := range old.TypedConstants {
		if usage.Type == nil {
			continue
		}
		usage.Type = m.Type(usage.Type)
		if m.Collation != nil {
			usage.Collation, err = m.Collation(usage.Collation)
			if err != nil {
				return nil, err
			}
		}
		copy.TypedConstants[i] = usage
	}
	copy.Grouping = make([]sqltype.Grouping, len(old.Grouping))
	for i := range copy.Grouping {
		copy.Grouping[i], err = sqltype.BindGrouping(copy.Record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	boolean, ok := m.Catalog.LookupType(sqltype.Bool)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	usage := copy.Mark.ResultUsage()
	copy.Inequality, err = sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, usage.Type, usage.Type, boolean, usage.Collation, copy.Mark.CollationIndeterminate())
	if err != nil {
		return nil, err
	}
	return &copy, nil
}

func (m *ViewQueryRemapper) search(old *plan.SearchRecipe) (*plan.SearchRecipe, error) {
	copy := *old
	copy.Record, copy.Result, copy.Depth = m.Type(old.Record), m.Type(old.Result), m.Type(old.Depth)
	if copy.Record == nil || copy.Record.ID() != 2249 || !copy.Record.IsComposite() ||
		old.Syntax.Breadth && copy.Result != copy.Record ||
		!old.Syntax.Breadth && (!sqltype.OccurrenceRecordArray(copy.Result) || copy.Result.Element() != copy.Record) {
		return nil, ids.ErrCatalogReplaced
	}
	copy.Grouping = make([]sqltype.Grouping, len(old.Grouping))
	for i := range copy.Grouping {
		var err error
		copy.Grouping[i], err = sqltype.BindGrouping(copy.Record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	return &copy, nil
}

// remapQuoteOwner retargets the owner of a node's quote-capture origin, which
// the node carries as an opaque annotation.
func remapQuoteOwner(node expr.Expr, ref func(ids.ObjectRef) ids.ObjectRef) expr.Expr {
	annotated, ok := node.(expr.Annotated)
	if !ok {
		return node
	}
	origin, _ := annotated.Annotation().(exec.QuoteOrigin)
	if origin.Owner == (ids.ObjectRef{}) {
		return node
	}
	origin.Owner = ref(origin.Owner)
	return annotated.WithAnnotation(origin)
}

func RemapLikeExpression(in *analyze.LikeInput, node expr.Expr, destination *catalog.Relation, authority catalog.Read) (expr.Expr, error) {
	if in.RowRef == nil {
		in.RowRef, in.Slots = in.RowMapper(destination, authority)
	}
	return RemapScalar(node, nil, in.RowRef, in.Slots, nil)
}
