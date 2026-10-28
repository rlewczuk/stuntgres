package stored

import (
	"fmt"
	"reflect"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Captured casts may name logical UNKNOWN or a source-authenticated literal
// pseudo cast. Neither grants executable pseudo Usage authority.
func (v *View) validCapturedCastUsage(view catalog.Read, path analyze.ViewLiteralPath, usage sqltype.Usage) bool {
	if usage.Type != nil && usage.Type.ID() == sqltype.Unknown {
		_, err := regobj.UnknownInputDeclaration(view, nil, usage)
		return err == nil
	}
	if ValidExpressionUsage(view, usage) {
		return true
	}
	if !expr.TransparentUnknownTarget(usage.Type) || usage != usage.Type.Usage() {
		return false
	}
	current, ok := catalog.DefinitionTypeAt(view, nil, usage.Type.ID())
	if !ok || current != usage.Type {
		return false
	}
	if marker, target := v.capturedUnknownLiteralCast(path); marker != nil {
		return target == usage.Type
	}
	return true // ValidateLiterals separately authenticates the source occurrence.
}

// Locate a cast through the authenticated chain's ordinal distance, not by
// peeling source spellings or assuming that its immediate child is a literal.
func (v *View) capturedUnknownLiteralCast(path analyze.ViewLiteralPath) (*expr.UnknownInput, *sqltype.Descriptor) {
	for literalPath, literal := range v.literals {
		distance := literalPath.Node - path.Node
		candidate := path
		candidate.Node = literalPath.Node
		if distance <= 0 || candidate != literalPath || literal.UnknownInput == nil {
			continue
		}
		marker := literal.UnknownInput
		if marker.Validate(literal.Usage, literal.Value) != nil {
			continue
		}
		if len(marker.Chain) != 0 {
			if distance <= len(marker.Chain) {
				return marker, marker.Chain[distance-1].Target
			}
		} else if distance == 1 && marker.CastType != nil {
			return marker, marker.CastType
		}
	}
	return nil, nil
}

// Pair a frozen occurrence with the already authenticated executable capture,
// not another entry in the presentation map. The raw traversal supplies the
// original ordinal; syntax (including source positions) identifies the carrier
// or transparent step within that occurrence's owning query frame.
func (v *View) capturedInputCastUsage(path analyze.ViewLiteralPath, raw parser.CastExpr) (sqltype.Usage, bool, error) {
	var usage sqltype.Usage
	found, transparent := false, false
	var failure error
	check := func(node expr.Expr) {
		if failure != nil {
			return
		}
		caster, ok := node.(expr.InputCaster)
		if !ok {
			return
		}
		capture, err := caster.LogicalInputCast()
		if err != nil {
			failure = err
			return
		}
		accept := func(candidate sqltype.Usage, step bool) {
			if found && (usage != candidate || transparent != step) {
				failure = fmt.Errorf("stuntgres: ambiguous frozen input cast source")
				return
			}
			usage, found, transparent = candidate, true, step
		}
		if capture.Target.Type != nil && reflect.DeepEqual(capture.Syntax, raw) {
			accept(capture.Target, false)
		}
		for _, step := range capture.Transparent {
			if reflect.DeepEqual(step.Syntax, raw) {
				accept(step.Target.Usage(), true)
			}
		}
	}
	var walk func(plan.Node)
	walk = func(node plan.Node) {
		if node == nil || failure != nil {
			return
		}
		if project, ok := node.(*plan.ProjectNode); ok && project.Frame != path.Query {
			return
		}
		plan.QueryExpressions(node, func(bound expr.Expr) { plan.WalkExpression(bound, check) })
		left, right := plan.QueryInputs(node)
		walk(left)
		walk(right)
	}
	walk(v.frames[path.Query])
	return usage, found, failure
}

// Pseudo-row validation is invocation-free and local to this traversal. Ordinary
// views retain their query-frame rules; trigger rows close over their owner.
type viewLiteralValidator struct {
	*View
	read        catalog.Read
	triggerRows *catalog.Relation
	events      TriggerEvent
}

func (v *View) ValidateLiterals(read catalog.Read) error {
	validator := viewLiteralValidator{View: v, read: read}
	return validator.validate(read)
}

func (v *View) validateTriggerLiterals(read catalog.Read, owner *catalog.Relation, events TriggerEvent) error {
	validator := viewLiteralValidator{View: v, read: read, triggerRows: owner, events: events}
	return validator.validate(read)
}

// LiteralRoots returns the original raw expression-root identities, checked
// against their bound sources. Node and Argument remain the recursive
// validator's authority, not independent presentation-frame ownership.
func (v *View) LiteralRoots() (map[analyze.ViewLiteralPath]bool, error) {
	roots := make(map[analyze.ViewLiteralPath]bool)
	record := func(path analyze.ViewLiteralPath) error {
		roots[path] = true
		return nil
	}
	err := v.walkLiteralRoots(func(raw parser.Expr, path analyze.ViewLiteralPath, _ *int) error {
		if raw != nil {
			return record(path)
		}
		return nil
	}, record)
	return roots, err
}

func (v *viewLiteralValidator) validate(catalog catalog.Read) error {
	count, columnCount, starCount, compositeCount, routineCount := 0, 0, 0, 0, 0
	err := v.walkLiteralRoots(func(raw parser.Expr, path analyze.ViewLiteralPath, ordinal *int) error {
		return v.validateLiteralTree(catalog, raw, path, ordinal, &count, &columnCount, &compositeCount, &routineCount)
	}, func(path analyze.ViewLiteralPath) error {
		starCount++
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
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(v.literals) || columnCount != len(v.columns) || starCount != len(v.stars) || compositeCount != len(v.composites) || routineCount != len(v.routineRefs) {
		return fmt.Errorf("stuntgres: invalid view literal occurrence path")
	}
	return nil
}

// walkLiteralRoots is the checked raw/source pairing shared by capture ownership
// and literal validation. Ordinals remain local to the original raw root.
func (v *View) walkLiteralRoots(visit func(parser.Expr, analyze.ViewLiteralPath, *int) error, star func(analyze.ViewLiteralPath) error) error {
	// Rendering starts from syntax, whereas occurrence traversal starts from
	// frames. They must name the same accepted root before either can grant
	// authority; otherwise replacement root syntax is never inspected here.
	if v.syntax != nil {
		project := v.queries[v.syntax]
		if project == nil || project.Syntax != v.syntax || v.frames[project.Frame] != project {
			return fmt.Errorf("stuntgres: mismatched frozen root syntax")
		}
	}
	for frame, project := range v.frames {
		s := project.Syntax
		if s == nil {
			if frame != 1 || v.expressionSyntax == nil {
				return fmt.Errorf("stuntgres: missing stored query syntax")
			}
			ordinal := 0
			if err := visit(v.expressionSyntax, analyze.ViewLiteralPath{Query: frame, Clause: analyze.ViewTargetLiteral}, &ordinal); err != nil {
				return err
			}
			continue
		}
		if s.Set != nil {
			set := QuerySetNode(analyze.ViewFromRoot(project))
			if set == nil || set.Left == nil || set.Right == nil || set.Left.Syntax != s.Set.Left || set.Right.Syntax != s.Set.Right || set.Operation != s.Set.Operator || set.All != s.Set.All {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
			left, right := v.queries[s.Set.Left], v.queries[s.Set.Right]
			if left == nil || right == nil || left.Frame != set.Left.Frame || right.Frame != set.Right.Frame {
				return fmt.Errorf("stuntgres: invalid frozen set syntax frames")
			}
		}
		validate := func(raw parser.Expr, clause analyze.ViewLiteralClause, target int) error {
			ordinal := 0
			return visit(raw, analyze.ViewLiteralPath{Query: frame, Clause: clause, Target: target}, &ordinal)
		}
		for i, raw := range s.Targets {
			if _, isStar := raw.(parser.StarExpr); isStar {
				if err := star(analyze.ViewLiteralPath{Query: frame, Clause: analyze.ViewTargetLiteral, Target: i}); err != nil {
					return err
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
			path := analyze.ViewLiteralPath{Query: frame, Clause: analyze.ViewWindowLiteral, Target: i}
			for _, key := range definition.Spec.Partition {
				if err := visit(key, path, &ordinal); err != nil {
					return err
				}
			}
			for _, key := range definition.Spec.Order {
				if err := visit(key.Expr, path, &ordinal); err != nil {
					return err
				}
			}
			for _, offset := range definition.Spec.Offsets() {
				if err := visit(offset, path, &ordinal); err != nil {
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
			case *parser.JSONTable:
				function, ok := analyze.ViewFromRoot(node).(*plan.FunctionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored JSON_TABLE frame")
				}
				for i, child := range raw.Expressions() {
					path := analyze.ViewLiteralPath{Query: frame, Clause: analyze.ViewFunctionLiteral, Source: function.ViewOrdinal, Target: i}
					ordinal := 0
					if err := visit(child, path, &ordinal); err != nil {
						return err
					}
				}
			case *parser.FunctionTable:
				table, ok := analyze.ViewFromRoot(node).(*plan.FunctionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored string function frame")
				}
				for i, call := range parser.NormalizedFunctionCalls(raw) {
					if table.RegexpNamespace {
						path := analyze.ViewLiteralPath{Query: frame, Clause: analyze.ViewFunctionLiteral, Source: table.ViewOrdinal, Target: i}
						ordinal := 0
						if err := visit(call, path, &ordinal); err != nil {
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
				return walkValuesLiteralRoots(raw, frame, values.ViewOrdinal, visit)
			case *parser.FunctionFrom:
				function, ok := analyze.ViewFromRoot(node).(*plan.FunctionFromNode)
				if !ok {
					return fmt.Errorf("stuntgres: invalid stored function frame")
				}
				for i, call := range raw.Calls {
					path := analyze.ViewLiteralPath{Query: frame, Clause: analyze.ViewFunctionLiteral, Source: function.ViewOrdinal, Target: i}
					ordinal := 0
					if err := visit(call, path, &ordinal); err != nil {
						return err
					}
				}
			}
			return nil
		}
		fromRoot, err := v.fromRoot(project)
		if err != nil {
			return err
		}
		if s.Values != nil {
			if err := from(s.Values, fromRoot); err != nil {
				return err
			}
		} else if err := from(s.From, fromRoot); err != nil {
			return err
		}
	}
	return nil
}

// Scalar stars and ordinary column syntax close over the same captured frame
// and slot, independently of composite expansion occurrences.
func (v *viewLiteralValidator) validateColumnOccurrence(path analyze.ViewLiteralPath) error {
	column, ok := v.columns[path]
	if column.TableOID || column.Owner != (ids.ObjectRef{}) || column.Scope != 0 || column.Occurrence != 0 {
		return fmt.Errorf("stuntgres: invalid ordinary view column mode")
	}
	if v.triggerRows != nil {
		relation := v.triggerRows
		address := column.RuleRef.Address
		address.SubID = 0
		if !ok || column.RuleSide != 1 && column.RuleSide != 2 || column.Frame != column.RuleSide || address != relation.Address() || !ReferenceEpochValid(v.read, relation, column.RuleRef) || column.Slot < 0 || column.Slot >= len(relation.Attributes) || relation.Attributes[column.Slot] != column.RuleRef.Address.SubID {
			return fmt.Errorf("stuntgres: invalid trigger column occurrence")
		}
		if column.RuleSide == 1 && v.events&TriggerInsert != 0 || column.RuleSide == 2 && v.events&TriggerDelete != 0 {
			return fmt.Errorf("stuntgres: unavailable trigger column occurrence")
		}
		return nil
	}
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

func (v *viewLiteralValidator) validateLiteralTree(catalog catalog.Read, raw parser.Expr, path analyze.ViewLiteralPath, ordinal, count, columnCount, compositeCount, routineCount *int) error {
	if raw == nil {
		return nil
	}
	path.Node = *ordinal
	(*ordinal)++
	provider := false
	switch call := raw.(type) {
	case parser.FunctionExpr:
		provider = call.StoredProviderRoutine
	case parser.AggregateExpr:
		provider = call.StoredProviderRoutine
	}
	if provider {
		if _, err := v.ProviderRecipeProcedure(catalog, path, raw); err != nil {
			return err
		}
		(*routineCount)++
	} else if call, ok := raw.(parser.FunctionExpr); ok && call.StoredSQLRoutine {
		if _, err := v.SQLRoutineRecipeProcedure(catalog, path, call); err != nil {
			return err
		}
		(*routineCount)++
	} else if call, ok := raw.(parser.AggregateExpr); ok && call.StoredAggregate {
		if _, err := v.AggregateRecipeProcedure(catalog, path, call); err != nil {
			return err
		}
		(*routineCount)++
	} else if tablefuncCrosstabRecipe(raw) {
		if _, err := v.tablefuncCrosstabRecipeProcedure(catalog, path); err != nil {
			return err
		}
		(*routineCount)++
	} else if pgrowlocksRecipe(raw) {
		if _, err := v.pgrowlocksRecipeProcedure(catalog, path); err != nil {
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
	case parser.XMLParseExpr:
		if _, err := v.capturedXML(path, r); err != nil {
			return err
		}
		return child(r.Child)
	case parser.XMLSerializeExpr:
		usage, err := v.capturedXML(path, r)
		if err != nil {
			return err
		}
		if captured, ok := v.castUsages[path]; !ok || captured != usage {
			return fmt.Errorf("stuntgres: mismatched frozen XMLSERIALIZE target")
		}
		return child(r.Child)
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
	case parser.SQLJSONQueryExpr:
		return args(r.Children())
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
		if err := ValidateCompositeOccurrence(captured, catalog); err != nil {
			return err
		}
		if err := v.ValidateCompositeSource(captured); err != nil {
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
			if err := literal.UnknownInput.Validate(literal.Usage, literal.Value); err != nil {
				return err
			}
			if source := literal.UnknownInput; source != nil {
				if !source.MatchesLiteral(raw) {
					return fmt.Errorf("stuntgres: mismatched frozen unknown literal source")
				}
				if source.CastType != nil {
					current, ok := catalog.LookupType(source.CastType.ID())
					if !ok || current != source.CastType {
						return fmt.Errorf("stuntgres: foreign unknown cast target")
					}
				}
			}
			if err := analyze.ValidateRegroleStoredLiteral(literal.Value); err != nil {
				return err
			}
			usage := literal.ResultUsage()
			if !usage.Valid() || literal.Value.Type() != usage.Type {
				return fmt.Errorf("stuntgres: invalid view literal usage")
			}
			if err := ValidateUsageCollation(catalog, usage); err != nil {
				return err
			}
			// Contextual RECORD literals own occurrence layouts, not the
			// catalog's lookup-only RECORD. Concrete leaves still must match.
			if !ValidExpressionUsage(catalog, usage) {
				return fmt.Errorf("stuntgres: foreign view literal type")
			}
			if ref := literal.RegclassRef; ref != (ids.ObjectRef{}) {
				consumer, _ := catalog.Relation(v.consumer)
				if !ViewReferenceValid(catalog, consumer, ref) || ref.Address.ClassID != ids.RelationClassID || ref.Address.SubID != 0 || usage.Type.ID() != sqltype.Regclass || literal.Value.IsNull() {
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
	case parser.ParameterExpr:
		if r.Number <= 0 || r.Number > len(v.routineInputs) || !ValidExpressionUsage(catalog, v.routineInputs[r.Number-1]) {
			return fmt.Errorf("stuntgres: invalid frozen routine parameter")
		}
		return nil
	case parser.ColumnExpr:
		if captured, whole := v.composites[path]; whole {
			if captured.Kind != analyze.CompositeWholeRow {
				return fmt.Errorf("stuntgres: invalid frozen scalar whole row")
			}
			if err := ValidateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.ValidateCompositeSource(captured); err != nil {
				return err
			}
			if v.namespaces[captured.Source.Namespace-1].Qualifiers[captured.Source.Qualifier].Name != r.Name.Parts[len(r.Name.Parts)-1].Text {
				return fmt.Errorf("stuntgres: mismatched frozen whole row qualifier")
			}
			(*compositeCount)++
			return nil
		}
		if captured, ok := v.columns[path]; ok && captured.TableOID {
			if err := v.validateTableOIDOccurrence(path, r.Pos); err != nil {
				return err
			}
			(*columnCount)++
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
			if err := ValidateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.ValidateCompositeSource(captured); err != nil {
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
		usage := v.castUsages[path]
		captured, logical, err := v.capturedInputCastUsage(path, r)
		if err != nil {
			return err
		}
		if logical {
			if usage != captured {
				return fmt.Errorf("stuntgres: mismatched frozen input cast occurrence")
			}
		} else if marker, target := v.capturedUnknownLiteralCast(path); marker != nil {
			if marker.CastTarget(r) != target || usage != target.Usage() {
				return fmt.Errorf("stuntgres: mismatched frozen unknown cast source")
			}
		} else if expr.TransparentUnknownTarget(usage.Type) {
			return fmt.Errorf("stuntgres: unbacked frozen unknown cast occurrence")
		}
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
			if err := ValidateCompositeOccurrence(captured, catalog); err != nil {
				return err
			}
			if err := v.ValidateCompositeSource(captured); err != nil {
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
