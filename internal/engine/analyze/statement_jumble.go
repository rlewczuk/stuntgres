package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StatementJumble streams selected analyzed structure before any folding. It
// owns normalization locations, not a canonical SQL string or retained graph.
type StatementJumble struct {
	hash               uint64
	Locations          []int
	Lists              [][2]int
	HighestParameter   int
	frames             []uint32
	UtilityLocations   []int
	parameterLocations []int
	utilityChild       parser.Statement
	utilityBinding     *Binding
}

func (j *StatementJumble) number(n uint64) {
	for range 8 {
		j.hash ^= n & 255
		j.hash *= 1099511628211
		n >>= 8
	}
}
func (j *StatementJumble) text(s string) {
	j.number(uint64(len(s)))
	for i := range len(s) {
		j.hash ^= uint64(s[i])
		j.hash *= 1099511628211
	}
}
func (j *StatementJumble) flag(b bool) {
	if b {
		j.number(1)
	} else {
		j.number(0)
	}
}
func (j StatementJumble) QueryID() int64 {
	if j.hash == 0 {
		return 1
	}
	return int64(j.hash)
}
func JumbleStatement(statement parser.Statement, b *Binding) StatementJumble {
	var frames [16]uint32
	var parameters [16]int
	j := StatementJumble{hash: 14695981039346656037, frames: frames[:0], parameterLocations: parameters[:0]}
	switch statement.(type) {
	case *parser.Select:
		j.text("*parser.Select")
	case *parser.Insert:
		j.text("*parser.Insert")
	case *parser.Update:
		j.text("*parser.Update")
	case *parser.Delete:
		j.text("*parser.Delete")
	case *parser.Merge:
		j.text("*parser.Merge")
	case *parser.Call:
		j.text("*parser.Call")
	default:
		panic(fmt.Sprintf("stuntgres: unclassified analyzed statement %T", statement))
	}
	j.binding(statement, b)
	j.finish()
	return j
}

func (j *StatementJumble) finish() {
	if len(j.Lists) > 0 {
		j.HighestParameter = 0
		j.Locations = append(j.Locations, j.parameterLocations...)
	}
	j.parameterLocations = nil
	j.frames = nil
	j.utilityChild = nil
	j.utilityBinding = nil
}
func (j *StatementJumble) query(node plan.Node) {
	if node == nil {
		j.number(0)
		return
	}
	switch n := node.(type) {
	case *plan.SourceNode:
		j.text("*plan.SourceNode")
		j.text(n.RangeName)
		j.flag(n.Sample != nil)
		if n.Sample != nil {
			j.number(uint64(n.Sample.Handler.Address.ObjectID))
		}
	case *plan.ProjectNode:
		j.text("*plan.ProjectNode")
		if len(j.frames) == 0 || j.frames[len(j.frames)-1] != n.Frame {
			j.frames = append(j.frames, n.Frame)
			defer func() { j.frames = j.frames[:len(j.frames)-1] }()
		}
	case *plan.NamedRelationScanNode:
		j.text("*plan.NamedRelationScanNode")
		j.text(n.RangeName)
	case *plan.FunctionFromNode:
		j.text("*plan.FunctionFromNode")
		j.text(n.RangeName)
		j.flag(n.Ordinality)
	case *plan.ValuesNode:
		j.text("*plan.ValuesNode")
		j.number(uint64(len(n.Rows)))
		for _, row := range n.Rows {
			j.number(uint64(len(row)))
		}
	case *plan.AggregateNode:
		j.text("*plan.AggregateNode")
		j.number(uint64(len(n.Keys)))
		for _, set := range n.Sets {
			j.number(uint64(len(set)))
			for _, key := range set {
				j.number(uint64(key))
			}
		}
		for _, call := range n.Calls {
			j.number(uint64(call.Routine.Address.ObjectID))
			j.number(uint64(call.Operation))
			j.number(uint64(len(call.Distinct)))
			for _, key := range call.Order {
				j.flag(key.Desc)
				j.flag(key.NullsFirst)
			}
		}
	case *plan.WindowNode:
		j.text("*plan.WindowNode")
		for i, call := range n.Calls {
			j.number(uint64(call.Routine.Address.ObjectID))
			j.number(uint64(call.Operation))
			if i < len(n.SpecIndices) {
				j.number(uint64(n.SpecIndices[i]))
			}
			j.expressions(call.Arguments)
			j.expression(call.Argument)
			j.expression(call.Ordinal)
			j.expression(call.DefaultValue)
		}
		for _, spec := range n.Specs {
			j.expressions(spec.Partition)
			for _, key := range spec.Order {
				j.flag(key.Desc)
				j.flag(key.NullsFirst)
				j.expression(key.Expression)
			}
			j.number(uint64(spec.Frame.Unit))
			j.number(uint64(spec.Frame.Exclusion))
			j.number(uint64(spec.Frame.Start.Kind))
			j.number(uint64(spec.Frame.End.Kind))
			j.expression(spec.Frame.Start.Offset)
			j.expression(spec.Frame.End.Offset)
		}
		left, _ := plan.QueryInputs(node)
		j.query(left)
		return
	case *plan.DerivedNode:
		j.text("*plan.DerivedNode")
		j.text(n.RangeName)
		if n.Query != nil {
			j.query(n.Query.Root)
		}
	case *plan.WorktableScanNode:
		j.text("*plan.WorktableScanNode")
		j.text(n.RangeName)
	case *plan.JoinNode:
		j.text("*plan.JoinNode")
		j.number(uint64(n.Kind))
		j.flag(n.Comma)
	case *plan.SortNode:
		j.text("*plan.SortNode")
		for _, key := range n.Keys {
			j.flag(key.Desc)
			j.flag(key.NullsFirst)
			j.number(uint64(key.OutputOrdinal))
		}
	case *plan.LimitNode:
		j.text("*plan.LimitNode")
		j.flag(n.WithTies)
	case *plan.SetNode:
		j.text("*plan.SetNode")
		j.text(n.Operation)
		j.flag(n.All)
	case *plan.RecursiveUnionNode:
		j.text("*plan.RecursiveUnionNode")
		j.text(n.Operation)
		j.flag(n.All)
	case *plan.RowLockNode:
		j.text("*plan.RowLockNode")
		for _, mark := range n.Marks {
			j.number(uint64(mark.Mode))
			j.number(uint64(mark.Wait))
		}
	case *plan.SingletonNode:
		j.text("*plan.SingletonNode")
	case *plan.FilterNode:
		j.text("*plan.FilterNode")
	case *plan.DistinctNode:
		j.text("*plan.DistinctNode")
	case *plan.DistinctOnNode:
		j.text("*plan.DistinctOnNode")
	case *plan.EvaluationNode:
		j.text("*plan.EvaluationNode")
	case *plan.SetProjectNode:
		j.text("*plan.SetProjectNode")
	default:
		panic(fmt.Sprintf("stuntgres: unclassified statement query node %T", node))
	}
	plan.QueryExpressions(node, j.expression)
	left, right := plan.QueryInputs(node)
	if left != nil {
		j.query(left)
	}
	if right != nil {
		j.query(right)
	}
}
func (j *StatementJumble) expression(node expr.Expr) {
	if node == nil {
		j.number(0)
		return
	}
	// Native constructors apply RETURNING with an enclosing assignment cast.
	// PostgreSQL retains that clause inside JsonConstructorExpr instead.
	if cast, ok := node.(plan.CoercionExpr); ok && cast.Plan.IsJSONReturning() {
		if constructor, ok := cast.Child.(functions.SQLJSON); ok {
			j.jsonConstructor(constructor, cast.ResultUsage())
			return
		}
	}
	if constructor, ok := node.(functions.SQLJSON); ok {
		j.jsonConstructor(constructor, constructor.ResultUsage())
		return
	}
	selected, ok := node.(expr.StatementJumbler)
	if !ok {
		panic(fmt.Sprintf("stuntgres: unclassified statement expression %T", node))
	}
	selected.JumbleFields(j)
	info := expr.InfoOf(node)
	// A plain Var deliberately excludes its type, relation OID and label.
	if n, ok := node.(plan.ColumnExpr); ok {
		j.column(n)
		return
	}
	switch n := node.(type) {
	case plan.LiteralExpr:
		j.number(uint64(n.ResultUsage().Type.ID()))
		if n.Pos.Character > 0 {
			j.Locations = append(j.Locations, n.Pos.Offset)
		}
		return
	case plan.ParameterExpr:
		j.number(uint64(n.ResultUsage().Type.ID()))
		j.number(uint64(n.Number))
		j.HighestParameter = max(j.HighestParameter, n.Number)
		if n.Pos.Character > 0 {
			j.parameterLocations = append(j.parameterLocations, n.Pos.Offset)
		}
		return
	case plan.ReturningWholeRowExpr:
		j.number(j.outerDepth(n.Frame))
		j.flag(n.Old)
		j.number(uint64(n.Side))
		return
	case plan.ReturningColumnExpr:
		j.column(n.ColumnExpr)
		j.flag(n.Old)
		j.number(uint64(n.Side))
		return
	case plan.TableOIDExpr:
		j.number(uint64(n.QueryOccurrence))
		j.number(j.outerDepth(n.Frame))
		return
	case plan.AggregateExpr:
		j.number(uint64(n.Slot))
		j.number(j.outerDepth(n.Frame))
		return
	case plan.GroupExpr:
		j.number(uint64(n.Slot))
		j.number(j.outerDepth(n.Frame))
		return
	case *plan.WindowExpr:
		j.number(uint64(n.Call))
		j.number(j.outerDepth(n.Frame))
		return
	case plan.ArrayExpr:
		j.number(uint64(n.ResultUsage().Type.ID()))
		j.flag(n.Multidimensional)
		if j.squashList(n.Elements) {
			return
		}
	case plan.RowExpr:
		if n.WholeName != "" {
			j.number(uint64(n.WholeSource.Namespace))
			j.number(uint64(n.WholeSource.Qualifier))
			j.number(j.outerDepth(n.WholeSource.Frame))
			return
		}
		j.number(uint64(n.ResultUsage().Type.ID()))
	case plan.CoercionExpr:
		j.number(uint64(n.ResultUsage().Type.ID()))
		j.number(uint64(n.Mode))
	case plan.CollationExpr:
		j.number(uint64(n.ResultUsage().Collation.ID))
	case plan.SelectedOperatorExpr:
		j.number(uint64(n.Ref.Address.ObjectID))
	case plan.ArithmeticExpr:
		j.number(uint64(n.Operator))
		j.text(n.Namespace)
		j.number(uint64(n.Left.ResultUsage().Type.ID()))
		if n.Right != nil {
			j.number(uint64(n.Right.ResultUsage().Type.ID()))
		}
	case plan.ComparisonExpr:
		j.number(uint64(n.RoutineRef.Address.ObjectID))
		j.text(n.Operator)
		j.text(n.Namespace)
	case plan.LogicalExpr:
		j.number(uint64(n.Op))
	case plan.NullTestExpr:
		j.flag(n.Not)
		j.flag(n.Whole)
	case plan.TruthTestExpr:
		j.number(uint64(n.Truth))
		j.flag(n.Not)
	case plan.DistinctExpr:
		j.flag(n.Not)
	case plan.ConditionalExpr:
		j.number(uint64(n.Kind))
	case plan.FieldExpr:
		j.number(uint64(n.Attribute))
	case plan.SubscriptExpr:
		j.flag(n.Slice)
	case plan.SubqueryExpr:
		j.number(uint64(n.Kind))
		j.text(n.Operator)
		j.flag(n.Negate)
		if n.QueryPlan != nil {
			j.query(n.QueryPlan.Root)
		}
	case plan.ScalarArrayComparisonExpr:
		j.number(uint64(n.RoutineRef.Address.ObjectID))
		j.text(n.Operator)
		j.text(n.Namespace)
		j.flag(n.All)
		j.expression(n.Left)
		if n.Array != nil {
			j.expression(n.Array)
			return
		}
		if len(n.Items) > 0 {
			j.number(uint64(n.Items[0].ResultUsage().Type.ID()))
		}
		if j.squashList(n.Items) {
			return
		}
		for _, item := range n.Items {
			j.expression(item)
		}
		return
	}
	if routine := info.CallRoutineReference(); routine != nil {
		j.number(uint64(routine.RoutineOID()))
	}
	if selected, ok := node.(expr.StatementJumbleChildren); ok {
		if _, xml := node.(functions.XMLConstruct); xml {
			selected.JumbleChildren(j.xmlSourceExpression, j)
		} else {
			selected.JumbleChildren(j.expression, j)
		}
		return
	}
	if composite, ok := node.(expr.Composite); ok {
		children := composite.Children()
		j.number(uint64(len(children)))
		for _, child := range children {
			j.expression(child)
		}
		return
	}
	// Plan expressions use the shared lexical walker, not expr.Composite.
	// In particular, residual singleton IN comparisons and serializer result
	// conversions must retain their actual operands and source locations.
	plan.ExpressionChildren(node, j.expression)
}

func (j *StatementJumble) xmlSourceExpression(node expr.Expr) {
	// Scalar XML binding retains assignment carriers even for exact identity.
	// They are not released expression nodes: keep real conversions, but let
	// an explicit typed constant match its implicit XMLROOT counterpart.
	for {
		cast, ok := node.(plan.CoercionExpr)
		if !ok || cast.Mode != sqltype.Assignment || cast.Child.ResultUsage() != cast.ResultUsage() {
			break
		}
		node = cast.Child
	}
	j.expression(node)
}

func (j *StatementJumble) jsonConstructor(n functions.SQLJSON, returning sqltype.Usage) {
	n.JumbleReturning(j, returning)
	// Only actual formatted input conversions acquire JsonValueExpr in
	// PostgreSQL. FORMAT JSON on an already JSON-valued input is discarded.
	if n.Raw.Kind != "json" || !n.Raw.Unique {
		for i, arg := range n.Args {
			if conversion, ok := arg.(plan.CoercionExpr); ok && conversion.Plan.IsJSONFormattedInput() {
				format, encoding := "default", ""
				if i < len(n.Raw.Formats) && n.Raw.Formats[i] != nil {
					format, encoding = "json", n.Raw.Formats[i].Encoding
				}
				j.text("JsonValueExpr")
				j.text(format)
				j.text(encoding)
			}
		}
	}
	j.expressions(n.Args)
}

func (j *StatementJumble) squashList(items []expr.Expr) bool {
	if len(items) < 2 {
		return false
	}
	for _, item := range items {
		for {
			cast, ok := item.(plan.CoercionExpr)
			if !ok {
				break
			}
			item = cast.Child
		}
		switch item.(type) {
		case plan.LiteralExpr, plan.ParameterExpr:
		default:
			return false
		}
	}
	first, last := items[0].SourcePosition(), items[len(items)-1].SourcePosition()
	if first.Character == 0 || last.Character == 0 {
		return false
	}
	// Released ArrayExpr omits the entire squashable element list from the
	// jumble. Parameter identities inside that list do not reserve placeholders.
	j.Lists = append(j.Lists, [2]int{first.Offset, last.Offset})
	return true
}

func (j *StatementJumble) JumbleUint64(value uint64) { j.number(value) }
func (j *StatementJumble) JumbleText(value string)   { j.text(value) }
func (j *StatementJumble) JumbleBool(value bool)     { j.flag(value) }

func (j *StatementJumble) JumbleConst(id sqltype.ID) {
	j.text("plan.LiteralExpr")
	j.number(uint64(id))
}
