package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (v *viewDefinition) validateLiterals(catalog catalog.Read) error {
	count, columnCount, starCount, compositeCount, routineCount := 0, 0, 0, 0, 0
	for frame, project := range v.frames {
		s := project.Syntax
		if s == nil {
			if frame != 1 || v.expressionSyntax == nil {
				return fmt.Errorf("stuntgres: missing stored query syntax")
			}
			ordinal := 0
			if err := v.validateLiteralTree(catalog, v.expressionSyntax, viewLiteralPath{Query: frame, Clause: analyze.ViewTargetLiteral}, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
				return err
			}
			continue
		}
		if s.Set != nil {
			set := querySetNode(analyze.ViewFromRoot(project))
			if set == nil || set.Left == nil || set.Right == nil || set.Left.Syntax != s.Set.Left || set.Right.Syntax != s.Set.Right || set.Operation != s.Set.Operator || set.All != s.Set.All {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
			left, right := v.queries[s.Set.Left], v.queries[s.Set.Right]
			if left == nil || right == nil || left.Frame != set.Left.Frame || right.Frame != set.Right.Frame {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
		}
		validate := func(raw parser.Expr, clause viewLiteralClause, target int) error {
			ordinal := 0
			return v.validateLiteralTree(catalog, raw, viewLiteralPath{Query: frame, Clause: clause, Target: target}, &ordinal, &count, &columnCount, &compositeCount, &routineCount)
		}
		for i, raw := range s.Targets {
			if _, star := raw.(parser.StarExpr); star {
				starCount++
				path := viewLiteralPath{Query: frame, Clause: analyze.ViewTargetLiteral, Target: i}
				columns, ok := v.stars[path]
				if !ok {
					return fmt.Errorf("stuntgres: missing frozen view star")
				}
				for _, column := range columns {
					owner := v.frames[column.Frame]
					if owner == nil {
						return fmt.Errorf("stuntgres: missing frozen star frame")
					}
					root := analyze.ViewFromRoot(owner)
					if column.Slot < 0 || column.Slot >= len(root.Schema()) {
						return fmt.Errorf("stuntgres: invalid frozen view star slot")
					}
				}
			}
			if err := validate(raw, analyze.ViewTargetLiteral, i); err != nil {
				return err
			}
		}
		if s.Distinct != nil {
			for i, raw := range s.Distinct.On {
				if err := validate(raw, analyze.ViewDistinctOnLiteral, i); err != nil {
					return err
				}
			}
		}
		if err := validate(s.Where, analyze.ViewWhereLiteral, 0); err != nil {
			return err
		}
		i := 0
		if err := s.GroupBy.VisitExpressions(func(raw parser.Expr) error {
			err := validate(raw, analyze.ViewGroupLiteral, i)
			i++
			return err
		}); err != nil {
			return err
		}
		if err := validate(s.Having, analyze.ViewHavingLiteral, 0); err != nil {
			return err
		}
		for i, definition := range s.Windows {
			ordinal := 0
			path := viewLiteralPath{Query: frame, Clause: analyze.ViewWindowLiteral, Target: i}
			for _, key := range definition.Spec.Partition {
				if err := v.validateLiteralTree(catalog, key, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
					return err
				}
			}
			for _, key := range definition.Spec.Order {
				if err := v.validateLiteralTree(catalog, key.Expr, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
					return err
				}
			}
			for _, offset := range definition.Spec.Offsets() {
				if err := v.validateLiteralTree(catalog, offset, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
					return err
				}
			}
		}
		// VALUES owns its decorations' source-ordinal literal paths even when
		// the SELECT syntax wrapper also exposes those same tail pointers.
		if s.Values == nil && s.Order != nil {
			for i, key := range s.Order.Keys {
				if err := validate(key.Expr, analyze.ViewOrderLiteral, i); err != nil {
					return err
				}
			}
		}
		if s.Values == nil && s.Bounds != nil {
			if err := validate(s.Bounds.Offset, analyze.ViewOffsetLiteral, 0); err != nil {
				return err
			}
			if err := validate(s.Bounds.Count, analyze.ViewLimitLiteral, 0); err != nil {
				return err
			}
		}
		var from func(parser.FromItem, plan.Node) error
		from = func(item parser.FromItem, node plan.Node) error {
			switch raw := item.(type) {
			case *parser.FunctionTable:
				table, ok := analyze.ViewFromRoot(node).(*plan.FunctionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored string function frame")
				}
				for i, call := range parser.NormalizedFunctionCalls(raw) {
					if table.RegexpNamespace {
						path := viewLiteralPath{Query: frame, Clause: analyze.ViewFunctionLiteral, Source: table.ViewOrdinal, Target: i}
						ordinal := 0
						if err := v.validateLiteralTree(catalog, call, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
							return err
						}
					} else if err := validate(call, analyze.ViewFunctionLiteral, table.ViewOrdinal+i); err != nil {
						return err
					}
				}
				return nil
			case *parser.AliasReference:
				return from(raw.Input, node)
			case *parser.JoinReference:
				join, ok := node.(*plan.JoinNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored join frame")
				}
				if err := validate(raw.On, analyze.ViewJoinLiteral, join.ViewOrdinal); err != nil {
					return err
				}
				if err := from(raw.Left, join.Left); err != nil {
					return err
				}
				return from(raw.Right, join.Right)
			case *parser.ValuesQuery:
				values, ok := analyze.ViewFromRoot(node).(*plan.ValuesNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored VALUES frame")
				}
				return v.validateValuesLiterals(catalog, raw, frame, values.ViewOrdinal, &count, &columnCount, &compositeCount, &routineCount)
			case *parser.FunctionFrom:
				function, ok := analyze.ViewFromRoot(node).(*plan.FunctionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored function frame")
				}
				for i, call := range raw.Calls {
					path := viewLiteralPath{Query: frame, Clause: analyze.ViewFunctionLiteral, Source: function.ViewOrdinal, Target: i}
					ordinal := 0
					if err := v.validateLiteralTree(catalog, call, path, &ordinal, &count, &columnCount, &compositeCount, &routineCount); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if s.Values != nil {
			if err := from(s.Values, analyze.ViewFromRoot(project)); err != nil {
				return err
			}
		} else if err := from(s.From, analyze.ViewFromRoot(project)); err != nil {
			return err
		}
	}
	if count != len(v.literals) || columnCount != len(v.columns) || starCount != len(v.stars) || compositeCount != len(v.composites) || routineCount != len(v.routineRefs) {
		return fmt.Errorf("stuntgres: invalid view literal occurrence path")
	}
	return nil
}

// Scalar stars and ordinary column syntax close over the same captured frame
// and slot, independently of composite expansion occurrences.
func (v *viewDefinition) validateColumnOccurrence(path viewLiteralPath) error {
	column, ok := v.columns[path]
	owner := v.frames[column.Frame]
	if !ok || owner == nil {
		return fmt.Errorf("stuntgres: invalid view column frame")
	}
	root := analyze.ViewFromRoot(owner)
	if path.Clause == analyze.ViewJoinLiteral && column.Frame == path.Query {
		root = viewJoinAt(root, path.Target)
	}
	if path.Clause == analyze.ViewValuesOrderLiteral && column.Frame == path.Query {
		root = viewValuesAt(owner, path.Source)
	}
	if root == nil || column.Slot < 0 || column.Slot >= len(root.Schema()) {
		return fmt.Errorf("stuntgres: invalid view column occurrence")
	}
	return nil
}

func (v *viewDefinition) validateLiteralTree(catalog catalog.Read, raw parser.Expr, path viewLiteralPath, ordinal, count, columnCount, compositeCount, routineCount *int) error {
	if raw == nil {
		return nil
	}
	path.Node = *ordinal
	(*ordinal)++
	if call, ok := raw.(parser.FunctionExpr); ok && call.StoredSQLRoutine {
		if _, err := v.sqlRoutineRecipeProcedure(catalog, path, call); err != nil {
			return err
		}
		(*routineCount)++
	} else if name := extension.IntaggRecipeFunction(raw); name != "" {
		if _, err := v.intaggRecipeProcedure(catalog, path, name); err != nil {
			return err
		}
		(*routineCount)++
	} else if _, exists := v.routineRefs[path]; exists {
		return fmt.Errorf("stuntgres: mismatched frozen routine occurrence")
	}
	child := func(raw parser.Expr) error {
		return v.validateLiteralTree(catalog, raw, path, ordinal, count, columnCount, compositeCount, routineCount)
	}
	args := func(raw []parser.Expr) error {
		for _, arg := range raw {
			if err := child(arg); err != nil {
				return err
			}
		}
		return nil
	}
	switch r := raw.(type) {
	case parser.GroupingExpr:
		return args(r.Args)
	case parser.InListExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return args(r.Items)
	case parser.ArrayComparisonExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return child(r.Right)
	case parser.SQLJSONExpr:
		if r.Query != nil {
			if v.queries[r.Query] == nil {
				return fmt.Errorf("stuntgres: missing frozen SQL/JSON query syntax")
			}
			return nil
		}
		return args(r.Args)
	case parser.JSONPredicateExpr:
		return child(r.Child)
	case parser.SubqueryExpr:
		if v.queries[r.Query] == nil {
			return fmt.Errorf("stuntgres: missing frozen subquery syntax")
		}
		return child(r.Left)
	case parser.FieldExpr:
		captured, ok := v.composites[path]
		if !ok || r.Expansion && captured.Kind != analyze.CompositeExpansion || !r.Expansion && captured.Kind != analyze.CompositeSelection && captured.Kind != analyze.CompositeDotBuiltin {
			return fmt.Errorf("stuntgres: missing frozen field interpretation")
		}
		if captured.Kind == analyze.CompositeDotBuiltin && captured.Function != functions.NullCountFunctionKind(r.Field.Text) {
			return fmt.Errorf("stuntgres: invalid frozen dot provider")
		}
		if err := validateCompositeOccurrence(captured, catalog); err != nil {
			return err
		}
		if err := v.validateCompositeSource(captured); err != nil {
			return err
		}
		(*compositeCount)++
		return child(r.Base)
	case parser.RowExpr:
		return args(r.Fields)
	case parser.IdentityExpr:
		return nil
	case parser.CurrentDateTimeExpr:
		return nil
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		if literal, ok := v.literals[path]; ok {
			if err := analyze.ValidateRegroleStoredLiteral(literal.Value); err != nil {
				return err
			}
			usage := literal.ResultUsage()
			if !usage.Valid() || literal.Value.Type() != usage.Type {
				return fmt.Errorf("stuntgres: invalid view literal usage")
			}
			if err := validateUsageCollation(catalog, usage); err != nil {
				return err
			}
			// Contextual RECORD literals own occurrence layouts, not the
			// catalog's lookup-only RECORD. Concrete leaves still must match.
			if !validExpressionUsage(catalog, usage) {
				return fmt.Errorf("stuntgres: foreign view literal type")
			}
			if ref := literal.RegclassRef; ref != (ids.ObjectRef{}) {
				consumer, _ := catalog.Relation(v.consumer)
				if !storedViewReferenceValid(catalog, consumer, ref) || ref.Address.ClassID != ids.RelationClassID || ref.Address.SubID != 0 || usage.Type.ID() != sqltype.Regclass || literal.Value.IsNull() {
					return fmt.Errorf("stuntgres: invalid view regclass literal authority")
				}
			}
			(*count)++
			return nil
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && path.Clause == analyze.ViewValuesOrderLiteral && path.Node == 0 && integer.Value.Kind == parser.IntegerValue {
			root := viewValuesAt(v.frames[path.Query], path.Source)
			if root == nil {
				return fmt.Errorf("stuntgres: missing VALUES order frame")
			}
			_, _, err := analyze.OrderOrdinal(raw, len(root.Schema()), "ORDER BY")
			return err
		}
		if integer, ok := raw.(parser.LiteralExpr); ok && (path.Clause == analyze.ViewGroupLiteral || path.Clause == analyze.ViewOrderLiteral || path.Clause == analyze.ViewDistinctOnLiteral) && path.Node == 0 && integer.Value.Kind == parser.IntegerValue {
			if path.Clause != analyze.ViewGroupLiteral {
				clause := "ORDER BY"
				if path.Clause == analyze.ViewDistinctOnLiteral {
					clause = "DISTINCT ON"
				}
				_, _, err := analyze.OrderOrdinal(raw, len(v.frames[path.Query].Schema()), clause)
				return err
			}
			_, _, err := analyze.GroupOrdinal(raw, len(v.frames[path.Query].Schema()))
			return err
		}
		return fmt.Errorf("stuntgres: missing view literal occurrence")
	case parser.ColumnExpr:
		if captured, whole := v.composites[path]; whole {
			if captured.Kind != analyze.CompositeWholeRow {
				return fmt.Errorf("stuntgres: invalid frozen scalar whole row")
			}
			if err := validateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			if v.namespaces[captured.Source.Namespace-1].Qualifiers[captured.Source.Qualifier].Name != r.Name.Parts[len(r.Name.Parts)-1].Text {
				return fmt.Errorf("stuntgres: mismatched frozen whole row qualifier")
			}
			(*compositeCount)++
			return nil
		}
		if err := v.validateColumnOccurrence(path); err != nil {
			return err
		}
		(*columnCount)++
		return nil
	case parser.StarExpr:
		if _, scalar := v.columns[path]; scalar {
			if err := v.validateColumnOccurrence(path); err != nil {
				return err
			}
			(*columnCount)++
			return nil
		}
		if _, relationStar := v.stars[path]; !relationStar {
			captured, ok := v.composites[path]
			if !ok || captured.Kind != analyze.CompositeExpansion && captured.Kind != analyze.CompositeWholeRow {
				return fmt.Errorf("stuntgres: missing frozen whole row interpretation")
			}
			if err := validateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			if captured.Source.Namespace == 0 || v.namespaces[captured.Source.Namespace-1].Qualifiers[captured.Source.Qualifier].Name != r.Qualifier.Parts[len(r.Qualifier.Parts)-1].Text {
				return fmt.Errorf("stuntgres: mismatched frozen whole row star")
			}
			(*compositeCount)++
		}
		return nil
	case parser.AggregateExpr:
		for _, arg := range r.Args {
			if err := child(arg); err != nil {
				return err
			}
		}
		for _, key := range r.Order {
			if err := child(key.Expr); err != nil {
				return err
			}
		}
		if r.Window != nil {
			for _, key := range r.Window.Partition {
				if err := child(key); err != nil {
					return err
				}
			}
			for _, key := range r.Window.Order {
				if err := child(key.Expr); err != nil {
					return err
				}
			}
			for _, offset := range r.Window.Offsets() {
				if err := child(offset); err != nil {
					return err
				}
			}
		}
		if r.Filter != nil {
			return child(r.Filter)
		}
		return nil
	case parser.TimeZoneExpr:
		if err := child(r.Value); err != nil {
			return err
		}
		if !r.Local {
			return child(r.Zone)
		}
		return nil
	case parser.CollateExpr:
		id, ok := v.collations[path]
		if !ok {
			return fmt.Errorf("stuntgres: missing frozen view collation")
		}
		d, ok := catalog.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)})
		if !ok {
			return fmt.Errorf("stuntgres: missing view collation identity")
		}
		if _, err := d.Usage(); err != nil {
			return fmt.Errorf("stuntgres: invalid view collation provider")
		}
		return child(r.Child)
	case parser.CastExpr:
		return child(r.Child)
	case parser.RegclassExpr:
		return child(r.Child)
	case parser.ArrayExpr:
		return args(r.Elements)
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		return args(r.Children(&storage))
	case parser.OperatorExpr:
		return args(r.Args)
	case parser.LogicalExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return child(r.Right)
	case parser.PredicateExpr:
		if err := child(r.Left); err != nil {
			return err
		}
		return child(r.Right)
	case parser.ConditionalExpr:
		return args(r.Args)
	case parser.CaseExpr:
		if err := child(r.Test); err != nil {
			return err
		}
		for _, arm := range r.Arms {
			if err := child(arm.Condition); err != nil {
				return err
			}
			if err := child(arm.Result); err != nil {
				return err
			}
		}
		return child(r.Else)
	case parser.FunctionExpr:
		if captured, projected := v.composites[path]; projected {
			if captured.Kind != analyze.CompositeSelection || len(r.Args) != 1 {
				return fmt.Errorf("stuntgres: invalid frozen functional projection")
			}
			if err := validateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.validateCompositeSource(captured); err != nil {
				return err
			}
			(*compositeCount)++
		}
		return args(r.Args)
	case parser.CallExpr:
		return args(r.Args)
	}
	return fmt.Errorf("stuntgres: invalid view literal syntax %T", raw)
}
