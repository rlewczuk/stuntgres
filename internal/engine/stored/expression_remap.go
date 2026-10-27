package stored

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
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
	case functions.HstoreJSON:
		n.Arg = child(n.Arg)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case functions.HstoreRecord:
		for i := range n.Arity() {
			n.Args[i] = child(n.Args[i])
		}
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case functions.HstoreConstruct:
		for i := range n.Arity() {
			n.Args[i] = child(n.Args[i])
		}
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case functions.HstoreRows:
		n.Arg = child(n.Arg)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case functions.HstoreArray:
		n.Arg = child(n.Arg)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case functions.String:
		n.Args = args(n.Args)
		if n.Citext != nil && ref != nil {
			capture := *n.Citext
			capture.Ref = ref(capture.Ref)
			n.Citext = &capture
		}
		next = n
		if ref != nil {
			next = remapFoldOwner(next, ref)
		}
	case functions.Regexp:
		n.Args = args(n.Args)
		n.RegexpCall = remapCitextRegexpReference(n.RegexpCall, ref)
		next = n
	case functions.RegexpSet:
		n.Args = args(n.Args)
		n.RegexpCall = remapCitextRegexpReference(n.RegexpCall, ref)
		next = n
	case plan.RangeConstructorExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.SQLRoutineExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.CitextComparisonExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.CitextPatternExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.HstoreComparisonExpr:
		args := make([]expr.Expr, len(n.Args))
		for i, a := range n.Args {
			args[i] = child(a)
		}
		n.Args = args
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.CubeComparisonExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
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
		if ref != nil && n.RoutineRef != (ids.ObjectRef{}) {
			n.RoutineRef = ref(n.RoutineRef)
		}
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
		if ref != nil && n.RoutineRef != (ids.ObjectRef{}) {
			n.RoutineRef = ref(n.RoutineRef)
		}
		next = n
	case plan.NullTestExpr:
		n.Child = child(n.Child)
		next = n
	case plan.TruthTestExpr:
		n.Child = child(n.Child)
		next = n
	case plan.DistinctExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		if ref != nil && n.RoutineRef != (ids.ObjectRef{}) {
			n.RoutineRef = ref(n.RoutineRef)
		}
		next = n
	case plan.LogicalExpr:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case plan.ConditionalExpr:
		n.Args = args(n.Args)
		if ref != nil && n.RoutineRef != (ids.ObjectRef{}) {
			n.RoutineRef = ref(n.RoutineRef)
		}
		next = n
	case plan.ExtensionPathsExpr:
		n.Arg = child(n.Arg)
		next = n
	case plan.InstalledPageinspectExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.InstalledAmcheckExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.InstalledEarthExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
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
	case plan.InstalledEarthPointExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		if err == nil {
			n.EarthPoint, err = functions.NewEarthPoint(n.Info, n.Args)
		}
		next = n
	case plan.InstalledCubeSpatialExpr:
		n.Args = [2]expr.Expr{child(n.Args[0]), child(n.Args[1])}
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		if err == nil {
			n.CubeSpatial, err = functions.NewCubeSpatial(n.Info, n.Plan.Operation(), n.Args)
		}
		next = n
	case plan.InstalledTextExpr:
		n.Arg = child(n.Arg)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.InstalledConnectbyExpr:
		args := make([]expr.Expr, len(n.Args))
		for i, arg := range n.Args {
			args[i] = child(arg)
		}
		n.Args = args
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.InstalledTablefuncNormalExpr:
		for i, arg := range n.Args {
			n.Args[i] = child(arg)
		}
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
	case plan.InstalledTablefuncCrosstabExpr:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case plan.InstalledVerifyHeapamExpr:
		for i, arg := range n.Args {
			n.Args[i] = child(arg)
		}
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
			if ref != nil && n.Arms[i].RoutineRef != (ids.ObjectRef{}) {
				n.Arms[i].RoutineRef = ref(n.Arms[i].RoutineRef)
			}
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
			next = remapFoldOwner(next, ref)
		}
	default:
		return nil, fmt.Errorf("stuntgres: unpersistable scalar expression %T", node)
	}
	return next, err
}
