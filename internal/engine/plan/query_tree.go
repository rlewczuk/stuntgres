package plan

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// AppendQueryTree writes the retained analyzed selections and occurrence
// associations of a logical query. Concrete syntax remains with its owner; this
// is neither an execution-plan dump nor a catalog lookup. QueryInputs owns the
// finite child edges, including opaque recursive worktable backedges.
func AppendQueryTree(ctx context.Context, out *strings.Builder, root Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if root == nil {
		return nil
	}
	fmt.Fprintf(out, " {query %T", root)
	var err error
	switch n := root.(type) {
	case *SourceNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		appendTreeAddress(out, "relation", n.Ref.Address)
		fmt.Fprintf(out, " occurrence %d", n.Occurrence)
	case *ProjectNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " frame %d", n.Frame)
	case *ValuesNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " occurrence %d", n.ViewOrdinal)
		for i, row := range n.Rows {
			fmt.Fprintf(out, " row %d width %d", i, len(row))
		}
	case *SingletonNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
	case *FilterNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
	case *SortNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		err = appendTreeSort(ctx, out, n.Keys)
	case *AggregateNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " grouping_sets %v", n.Sets)
		err = appendTreeGrouping(ctx, out, n.Grouping)
		for i, call := range n.Calls {
			if err != nil {
				break
			}
			fmt.Fprintf(out, " {aggregate call %d result_slot %d operation %d", i, len(n.Keys)+i, call.Operation)
			err = appendTreeProcedure(out, call.Routine, call.Procedure)
			appendTreeAddress(out, "installed", call.Ref.Address)
			for _, procedure := range call.PrimaryStringSupport {
				if procedure != nil {
					appendTreeAddress(out, "support", procedure.Address())
				}
			}
			if err == nil {
				err = appendTreeSort(ctx, out, call.Order)
			}
			if err == nil {
				err = appendTreeGrouping(ctx, out, call.Distinct)
			}
			fmt.Fprintf(out, " arguments %d direct %d", len(call.Arguments), len(call.Direct))
			out.WriteByte('}')
		}
	case *WindowNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		// QueryExpressions deliberately skips analysis-only calls and inactive
		// specs. Metadata owns both, so enumerate their retained roots here.
		fmt.Fprintf(out, " specs %v sorts %v", n.SpecIndices, n.SortIndices)
		for i, spec := range n.Specs {
			if err != nil {
				break
			}
			fmt.Fprintf(out, " {spec %d", i)
			err = appendTreeWindowSpec(ctx, out, spec)
			out.WriteByte('}')
		}
		for i, call := range n.Calls {
			if err != nil {
				break
			}
			if call.Expression == nil {
				return fmt.Errorf("stuntgres: missing structural window occurrence")
			}
			fmt.Fprintf(out, " {window call %d frame %d slot %d occurrence %d operation %d", i, call.Expression.Frame, call.Expression.Slot, call.Expression.Call, call.Operation)
			err = appendTreeProcedure(out, call.Routine, call.Procedure)
			if err == nil {
				err = appendTreeWindowSpec(ctx, out, call.Spec)
			}
			if err == nil {
				err = appendTreeExpressions(ctx, out, "arguments", call.Arguments)
			}
			if err == nil {
				err = appendTreeExpression(ctx, out, "argument", call.Argument)
			}
			if err == nil {
				err = appendTreeExpression(ctx, out, "ordinal", call.Ordinal)
			}
			if err == nil {
				err = appendTreeExpression(ctx, out, "default", call.DefaultValue)
			}
			out.WriteByte('}')
		}
	case *DistinctNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		err = appendTreeGrouping(ctx, out, n.Grouping)
	case *DistinctOnNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		err = appendTreeGrouping(ctx, out, n.Grouping)
	case *SetNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " operation %q all %t", n.Operation, n.All)
		err = appendTreeGrouping(ctx, out, n.Grouping)
	case *RecursiveUnionNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " operation %q all %t", n.Operation, n.All)
		appendTreeCTE(out, n.Definition)
		err = appendTreeGrouping(ctx, out, n.Grouping)
		if n.Search != nil {
			fmt.Fprintf(out, " search_slot %d keys %v", n.Search.Slot, n.Search.Keys)
			appendTreeDescriptor(out, "search_record", n.Search.Record)
			appendTreeDescriptor(out, "search_result", n.Search.Result)
			appendTreeDescriptor(out, "search_depth", n.Search.Depth)
		}
		if n.Search != nil {
			appendTreeScalarGrouping(out, n.Search.Grouping)
		}
		if n.Cycle != nil {
			fmt.Fprintf(out, " cycle_slot %d keys %v", n.Cycle.MarkSlot, n.Cycle.Keys)
			appendTreeDescriptor(out, "cycle_record", n.Cycle.Record)
			appendTreeDescriptor(out, "cycle_path", n.Cycle.Path)
			if err == nil {
				err = appendTreeExpression(ctx, out, "cycle_mark", n.Cycle.Mark)
			}
			if err == nil {
				err = appendTreeExpression(ctx, out, "cycle_fallback", n.Cycle.Fallback)
			}
		}
		if n.Cycle != nil {
			appendTreeScalarGrouping(out, n.Cycle.Grouping)
			for _, usage := range n.Cycle.TypedConstants {
				out.WriteString(" typed_mark")
				appendTreeUsage(out, usage)
			}
			collation, indeterminate := n.Cycle.Inequality.InputCollation()
			fmt.Fprintf(out, " cycle_inequality_collation %d derivation %d indeterminate %t", collation.ID, collation.Derivation, indeterminate)
		}
	case *DerivedNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " occurrence %d frame %d", n.Ordinal, n.ScanFrame)
		appendTreeCTE(out, n.CTE)
		// A materialized/nonnested CTE scan owns a definition rather than a
		// QueryInputs child. Its body is finite: recursive self references
		// are WorktableScanNode backedges, handled separately below.
		if n.Query == nil && n.CTE != nil && n.CTE.Query != nil {
			err = AppendQueryTree(ctx, out, n.CTE.Query.Root)
		}
	case *WorktableScanNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " backedge occurrence %d frame %d", n.Ordinal, n.ScanFrame)
		appendTreeCTE(out, n.CTE)
	case *JoinNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " occurrence %d frame %d kind %d correlation_prefix %d identity_prefix %t", n.ViewOrdinal, n.Frame, n.Kind, n.CorrelationPrefix, n.CorrelationIdentityPrefix)
		appendTreeCaptures(out, n.Captures)
	case *FunctionFromNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " occurrence %d frame %d", n.ViewOrdinal, n.Frame)
		appendTreeCaptures(out, n.Captures)
	case *LimitNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " with_ties %t", n.WithTies)
		err = appendTreeSort(ctx, out, n.PeerKeys)
	case *RowLockNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		for i, mark := range n.Marks {
			fmt.Fprintf(out, " {lock %d mode %d wait %d order %d}", i, mark.Mode, mark.Wait, mark.Order)
		}
	case *EvaluationNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " evaluation_base %d", n.Base)
	case *SetProjectNode:
		if n == nil {
			return fmt.Errorf("stuntgres: nil structural query node %T", root)
		}
		fmt.Fprintf(out, " frame %d call_slots %v scalar_slots %v steps %v", n.Frame, n.CallSlots, n.ScalarSlots, n.Steps)
		if n.Template != nil {
			if n.Template.Group == nil {
				return fmt.Errorf("stuntgres: missing structural set group")
			}
			fmt.Fprintf(out, " group %d scalar_requests %v template_steps %v", *n.Template.Group, n.Template.ScalarRequests, n.Template.Steps)
		}
	default:
		return fmt.Errorf("stuntgres: unsupported structural query node %T", root)
	}
	if err != nil {
		return err
	}
	for i, column := range root.Schema() {
		if column.Usage.Type == nil {
			return fmt.Errorf("stuntgres: missing structural query output type")
		}
		fmt.Fprintf(out, " {output %q ordinal %d", column.Name, i)
		appendTreeUsage(out, column.Usage)
		out.WriteByte('}')
	}
	if _, window := root.(*WindowNode); !window {
		i := 0
		QueryExpressions(root, func(expression expr.Expr) {
			if err != nil || expression == nil {
				return
			}
			fmt.Fprintf(out, " {expression %d", i)
			err = AppendBoundTree(ctx, out, expression)
			appendTreeBindings(out, expression)
			out.WriteByte('}')
			i++
		})
	}
	if err != nil {
		return err
	}
	left, right := QueryInputs(root)
	if err := AppendQueryTree(ctx, out, left); err != nil {
		return err
	}
	if err := AppendQueryTree(ctx, out, right); err != nil {
		return err
	}
	out.WriteByte('}')
	return ctx.Err()
}

func appendTreeAddress(out *strings.Builder, role string, address ids.ObjectAddress) {
	fmt.Fprintf(out, " %s %d/%d/%d", role, address.ClassID, address.ObjectID, address.SubID)
}

func appendTreeProcedure(out *strings.Builder, routine ids.ObjectRef, procedure *catalog.Procedure) error {
	if procedure != nil && procedure.Address() != routine.Address {
		return fmt.Errorf("stuntgres: mismatched structural selected declaration")
	}
	appendTreeAddress(out, "routine", routine.Address)
	return nil
}

func appendTreeUsage(out *strings.Builder, usage sqltype.Usage) {
	if usage.Type != nil {
		fmt.Fprintf(out, " type %d typmod %d collation %d derivation %d", usage.Type.ID(), usage.Typmod, usage.Collation.ID, usage.Collation.Derivation)
	}
}

func appendTreeDescriptor(out *strings.Builder, role string, descriptor *sqltype.Descriptor) {
	if descriptor != nil {
		fmt.Fprintf(out, " %s %d", role, descriptor.ID())
	}
}

func appendTreeExpression(ctx context.Context, out *strings.Builder, role string, expression expr.Expr) error {
	if expression == nil {
		return nil
	}
	fmt.Fprintf(out, " {%s ", role)
	if err := AppendBoundTree(ctx, out, expression); err != nil {
		return err
	}
	appendTreeBindings(out, expression)
	out.WriteByte('}')
	return nil
}

func appendTreeExpressions(ctx context.Context, out *strings.Builder, role string, expressions []expr.Expr) error {
	fmt.Fprintf(out, " {%s", role)
	for _, expression := range expressions {
		if err := appendTreeExpression(ctx, out, "operand", expression); err != nil {
			return err
		}
	}
	out.WriteByte('}')
	return nil
}

func appendTreeComparison(out *strings.Builder, comparison sqltype.Comparison) {
	collation, indeterminate := comparison.InputCollation()
	fmt.Fprintf(out, " class %d collation %d derivation %d indeterminate %t", comparison.DefaultBtreeOpclass(), collation.ID, collation.Derivation, indeterminate)
}

func appendTreeGrouping(ctx context.Context, out *strings.Builder, groupings []Grouping) error {
	for i, grouping := range groupings {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintf(out, " {grouping %d", i)
		usage, comparison := grouping.Grouping.Selection()
		appendTreeUsage(out, usage)
		appendTreeComparison(out, comparison)
		if err := appendTreeProcedure(out, grouping.Routine, grouping.Procedure); err != nil {
			return err
		}
		if grouping.Convert {
			source, target := grouping.Conversion.Usages()
			out.WriteString(" convert_source")
			appendTreeUsage(out, source)
			out.WriteString(" convert_target")
			appendTreeUsage(out, target)
			usage, comparison = grouping.Converted.Selection()
			out.WriteString(" converted")
			appendTreeUsage(out, usage)
			appendTreeComparison(out, comparison)
		}
		out.WriteByte('}')
	}
	return nil
}

func appendTreeSort(ctx context.Context, out *strings.Builder, keys []SortKey) error {
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintf(out, " {key %d output %d target %d slot %d cache %d desc %t nulls_first %t", i, key.OutputOrdinal, key.TargetIdentity, key.Slot, key.Cache, key.Desc, key.NullsFirst)
		appendTreeComparison(out, key.Comparison)
		if err := appendTreeProcedure(out, key.EqualityRoutine, key.EqualityProcedure); err != nil {
			return err
		}
		if key.Ordering != nil {
			for _, ref := range key.Ordering.References() {
				appendTreeAddress(out, "ordering", ref.Address)
			}
		}
		if err := appendTreeExpression(ctx, out, "operand", key.Expression); err != nil {
			return err
		}
		out.WriteByte('}')
	}
	return nil
}

func appendTreeWindowSpec(ctx context.Context, out *strings.Builder, spec WindowSpec) error {
	fmt.Fprintf(out, " definition %d order_map %v frame %d exclusion %d", spec.Definition, spec.OrderMap, spec.Frame.Unit, spec.Frame.Exclusion)
	if err := appendTreeExpressions(ctx, out, "partition", spec.Partition); err != nil {
		return err
	}
	if err := appendTreeGrouping(ctx, out, spec.Grouping); err != nil {
		return err
	}
	if err := appendTreeSort(ctx, out, spec.Order); err != nil {
		return err
	}
	for i, bound := range [2]WindowBound{spec.Frame.Start, spec.Frame.End} {
		key, offset, class := bound.Support.Selection()
		fmt.Fprintf(out, " {bound %d kind %d key %d offset %d class %d", i, bound.Kind, key, offset, class)
		if err := appendTreeExpression(ctx, out, "offset", bound.Offset); err != nil {
			return err
		}
		out.WriteByte('}')
	}
	return nil
}

func appendTreeCTE(out *strings.Builder, definition *CTEDefinition) {
	if definition != nil {
		fmt.Fprintf(out, " {cte owner %d name %q}", definition.Owner, definition.Name)
	}
}

func appendTreeCaptures(out *strings.Builder, captures []FunctionCorrelation) {
	for _, capture := range captures {
		fmt.Fprintf(out, " {capture frame %d slot %d occurrence %d}", capture.Frame, capture.Slot, capture.QueryOccurrence)
	}
}

func appendTreeScalarGrouping(out *strings.Builder, groupings []sqltype.Grouping) {
	for i, grouping := range groupings {
		fmt.Fprintf(out, " {equality %d", i)
		usage, comparison := grouping.Selection()
		appendTreeUsage(out, usage)
		appendTreeComparison(out, comparison)
		out.WriteByte('}')
	}
}

// Ordinary Var bytes are a longstanding routine format. The query projection
// additionally owns their frame/source-slot association, including window roots.
func appendTreeBindings(out *strings.Builder, expression expr.Expr) {
	WalkExpression(expression, func(child expr.Expr) {
		if column, ok := child.(ColumnExpr); ok {
			fmt.Fprintf(out, " {binding scope %d frame %d slot %d}", column.Scope, column.Frame, column.Slot)
			appendTreeAddress(out, "origin", column.Origin.Address)
		}
	})
}
