package analyze

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

func (b *Binder) Bind(raw parser.Expr, policy ExpressionPolicy) (boundResult expr.Expr, bindError error) {
	// Binding can run under publication locks, including nested execution
	// reentry. Preserve the host's request, never its detached wait permission.
	b.Ctx = util.DeactivateExecutionPause(workadmission.Suspend(b.Ctx))
	previousSource := b.foldSource
	if raw != nil {
		key := b.storedExpressionSyntaxKey(raw)
		b.foldSource = expr.FoldSourceOccurrence{Frame: key.Frame, Position: key.Position, Input: key.Input}
	}
	defer func() { b.foldSource = previousSource }()
	// Only the selected gateway may inherit its already-bound FILTER. Any
	// ordinary child starts a separate occurrence, even when it returns early.
	if filter := b.Clause.selectedFilter; filter != nil && !b.Clause.selectedCallReentry {
		b.Clause.selectedFilter = nil
		defer func() { b.Clause.selectedFilter = filter }()
	}
	if policy.Context == FilterContext && !policy.operand && policy.booleanConstruct == "" {
		return b.BindBoolean(raw, policy.Context, "WHERE")
	}
	if err := b.Ctx.Err(); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("stuntgres: missing scalar expression")
	}
	if slot, ok := raw.(parser.ParameterExpr); ok && slot.Number < 0 && -slot.Number <= len(b.Clause.SelectedOperatorArgs) {
		node := b.Clause.SelectedOperatorArgs[-slot.Number-1]
		if b.Grouping.ArgumentOwner.Active && !b.Grouping.ArgumentOwner.Found {
			// Selection analyzed this operand before the winning argument
			// owner existed. Preserve its lexical occurrence even if the
			// signature does not retain an executable argument.
			if nested := WindowOccurrence(node); nested != nil && nested.Frame == b.FrameID() {
				b.Grouping.ArgumentOwner.First, b.Grouping.ArgumentOwner.Found = nested.Pos, true
			}
		}
		return node, nil
	}
	defer func() {
		if bindError != nil || boundResult == nil {
			return
		}
		if policy.Context == AssignmentContext && !policy.deferAssignment {
			bindError = rejectRegexpSets(boundResult, "UPDATE", false)
		}
		if policy.Context == FilterContext {
			clause := b.Clause.AggregateClause
			if clause == "" || clause == "SELECT" {
				clause = "WHERE"
			}
			bindError = rejectRegexpSets(boundResult, clause, false)
		}
		if bindError != nil {
			boundResult = nil
		}
	}()
	if b.Consumer.PublicationConsumer {
		detail := ""
		switch r := raw.(type) {
		case parser.ColumnExpr:
			parts := r.Name.Parts
			if len(parts) != 0 && catalog.IsSystemColumn(parts[len(parts)-1].Text) {
				owner := b.Target.Relation
				if owner != nil && (len(parts) == 1 || len(parts) == 2 && parts[0].Text == owner.Name() || len(parts) == 3 && parts[1].Text == owner.Name() && parts[0].Text == b.Query.Namespace.Qualifiers[0].Schema) {
					detail = "System columns are not allowed."
				}
			}
		case parser.SubqueryExpr, parser.IndexSubqueryExpr:
			detail = "Only columns, constants, built-in operators, built-in data types, built-in collations, and immutable built-in functions are allowed."
		}
		if detail != "" {
			diagnostic := expr.ErrorAt("0A000", "invalid publication WHERE expression", raw.SourcePosition()).(*sqlstate.Error)
			diagnostic.Detail = detail
			return nil, diagnostic
		}
	}
	if b.Consumer.StatisticsConsumer {
		switch r := raw.(type) {
		case parser.ColumnExpr:
			name := r.Name.Parts[len(r.Name.Parts)-1].Text
			switch name {
			case "tableoid", "ctid", "xmin", "xmax", "cmin", "cmax":
				return nil, expr.ErrorAt("0A000", "statistics creation on system columns is not supported", parser.Position{})
			}
		case parser.SubqueryExpr:
			return nil, expr.ErrorAt("0A000", "cannot use subquery in statistics expression", r.Pos)
		case parser.IndexSubqueryExpr:
			return nil, expr.ErrorAt("0A000", "cannot use subquery in statistics expression", r.Pos)
		case parser.GroupingExpr:
			return nil, expr.ErrorAt("42803", "grouping operations are not allowed in statistics expressions", r.Pos)
		}
	}
	if b.Consumer.CallArguments {
		switch r := raw.(type) {
		case parser.SubqueryExpr:
			return nil, expr.ErrorAt("0A000", "cannot use subquery in CALL argument", r.Pos)
		case parser.IndexSubqueryExpr:
			return nil, expr.ErrorAt("0A000", "cannot use subquery in CALL argument", r.Pos)
		case parser.AggregateExpr:
			if r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in CALL arguments", r.Pos)
			}
			return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in CALL arguments", r.Pos)
		case parser.FunctionExpr:
			if r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in CALL arguments", r.Pos)
			}
		}
	}
	if b.Consumer.CopyWhere {
		switch r := raw.(type) {
		case parser.SubqueryExpr:
			return nil, expr.ErrorAt("0A000", "cannot use subquery in COPY FROM WHERE condition", r.Pos)
		case parser.IndexSubqueryExpr:
			return nil, expr.ErrorAt("0A000", "cannot use subquery in COPY FROM WHERE condition", r.Pos)
		case parser.AggregateExpr:
			if r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in COPY FROM WHERE conditions", r.Pos)
			}
			for _, arg := range r.Args {
				if _, err := b.Bind(arg, ExpressionPolicy{Context: ProjectionContext}); err != nil {
					return nil, err
				}
			}
			return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in COPY FROM WHERE conditions", r.Pos)
		case parser.FunctionExpr:
			if r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in COPY FROM WHERE conditions", r.Pos)
			}
		}
	}
	var node expr.Expr
	if selected, handled, err := b.BindSQLRoutineSyntax(raw, policy); handled {
		if err != nil {
			return nil, err
		}
		node = selected
	}
	if node == nil && b.Consumer.PartitionConsumer != "" {
		switch r := raw.(type) {
		case parser.ColumnExpr:
			if b.Consumer.PartitionConsumer == "partition bound" {
				return nil, expr.ErrorAt("0A000", "cannot use column reference in partition bound expression", r.Pos)
			}
		case parser.IndexSubqueryExpr:
			return nil, b.Consumer.partitionExpressionError("0A000", "cannot use subquery in "+b.Consumer.PartitionConsumer, r.Pos)
		case parser.SubqueryExpr:
			return nil, b.Consumer.partitionExpressionError("0A000", "cannot use subquery in "+b.Consumer.PartitionConsumer, r.Pos)
		case parser.AggregateExpr:
			if r.Window == nil && r.Name != "row_number" && r.Name != "ntile" && !functions.WindowValueName(r.Name) && !functions.WindowOffsetName(r.Name) {
				for _, arg := range r.Args {
					if _, err := b.Bind(arg, ExpressionPolicy{Context: ProjectionContext}); err != nil {
						return nil, err
					}
				}
				return nil, b.Consumer.partitionExpressionError("42803", "aggregate functions are not allowed in "+b.Consumer.partitionContextPlural(), r.Pos)
			}
		}
	}
	if node == nil && b.Clause.FunctionProjection.matches(raw) {
		node = b.Clause.FunctionProjection.Operand
	}
	if node == nil {
		switch r := raw.(type) {
		case parser.MergeActionExpr:
			var err error
			node, err = b.bindMergeAction(r)
			if err != nil {
				return nil, err
			}
		case parser.IndexSubqueryExpr:
			if b.Consumer.IndexPredicateConsumer {
				return nil, expr.ErrorAt("0A000", "cannot use subquery in index predicate", r.Pos)
			}
			return nil, expr.ErrorAt("0A000", "cannot use subquery in index expression", r.Pos)
		case parser.SubqueryExpr:
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "cannot use subquery in EXECUTE parameter", r.Pos)
			}
			var err error
			node, err = b.bindSubquery(r)
			if err != nil {
				return nil, err
			}
		case parser.RowExpr:
			var err error
			node, err = b.BindRow(r, policy.Expected, policy.Mode, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.FieldExpr:
			var err error
			node, err = b.bindField(r)
			if err != nil {
				return nil, err
			}
		case parser.StarExpr:
			var err error
			node, err = b.wholeRow(r.Qualifier, r.Pos, true)
			if err != nil {
				return nil, err
			}
		case parser.IdentityExpr:
			var err error
			node, err = functions.BindIdentity(b, r.Name, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.CurrentDateTimeExpr:
			var err error
			var warning *sqlstate.Error
			node, warning, err = functions.BindCurrentDateTime(b, r)
			if err != nil {
				return nil, err
			}
			// A held description already analyzed this syntax. Execute must
			// still bind fresh descriptors and evaluate fresh values.
			if warning != nil && !b.Expected.Held {
				b.Host.EmitWarning(*warning)
			}
		case parser.ForbiddenGenerationExpr:
			return nil, expr.ErrorAt(r.Code, r.Message, r.Pos)
		case parser.GroupingExpr:
			var err error
			node, err = b.bindGrouping(r)
			if err != nil {
				return nil, err
			}
		case parser.AggregateExpr:
			ownedAggregate := b.Clause.selectedAggregate
			b.Clause.selectedAggregate = nil
			if r.Window == nil && b.cte.recursiveFrames[b.FrameID()] {
				return nil, expr.ErrorAt("42P19", "aggregate functions are not allowed in a recursive query's recursive term", r.Pos)
			}
			if b.Params.ExecuteParameters {
				if r.Window != nil {
					return nil, expr.ErrorAt("42P20", "window functions are not allowed in EXECUTE parameters", r.Pos)
				}
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in EXECUTE parameters", r.Pos)
			}
			name := parser.ObjectName{Parts: []parser.Identifier{{Text: r.Name, Pos: r.Pos}}}
			if r.Schema != nil {
				name.Parts = []parser.Identifier{*r.Schema, {Text: r.Name, Pos: r.Pos}}
			}
			diagnosticName := name
			if len(b.Clause.selectedWindowCallee.Parts) != 0 {
				// Consume before Signature binds any children. Nested calls
				// must retain their own lexical diagnostic spelling.
				diagnosticName = b.Clause.selectedWindowCallee
				b.Clause.selectedWindowCallee = parser.ObjectName{}
			}
			parentAggregates, parentWindows := b.Grouping.Aggregates, b.Grouping.WindowCalls
			defer func() {
				if bindError != nil {
					b.Grouping.Aggregates, b.Grouping.WindowCalls = parentAggregates, parentWindows
				}
			}()
			if ownedAggregate == nil && functions.HypotheticalSetName(r.Name) && !r.Within && len(r.Args) == 0 && r.Window != nil {
				var err error
				node, err = b.bindWindow(r, name)
				if err != nil {
					return nil, err
				}
				break
			}
			if r.Within && ownedAggregate == nil && !functions.OrderedSetName(r.Name) {
				return b.rejectOrdinaryWithin(r)
			}
			if ownedAggregate != nil && r.Window != nil {
				var err error
				node, err = b.bindOwnedAggregate(r, ownedAggregate, diagnosticName)
				if err != nil {
					return nil, err
				}
				break
			}
			if r.Window != nil && !functions.OrderedSetName(r.Name) {
				var err error
				node, err = b.bindWindow(r, name)
				if err != nil {
					return nil, err
				}
				break
			}
			// A window function without OVER resolves its signature first.
			if function, ok := functions.LookupWindowFunction(r.Name); ownedAggregate == nil && ok && (!functions.HypotheticalSetName(r.Name) || !r.Within && len(r.Args) == 0) {
				if _, err := function.Signature(b, r, name); err != nil {
					return nil, err
				}
				return nil, expr.ErrorAt("42809", "window function "+catalog.ObjectNameText(diagnosticName)+" requires an OVER clause", r.Pos)
			}
			parentOwner := b.Grouping.ArgumentOwner
			b.Grouping.ArgumentOwner = ArgumentWindowOwner{Active: true}
			defer func() { b.Grouping.ArgumentOwner = parentOwner }()
			if b.Target.GeneratedOwner != nil {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in column generation expressions", r.Pos)
			}
			if b.Consumer.StatisticsConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in statistics expressions", r.Pos)
			}
			if b.Consumer.IndexPredicateConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index predicates", r.Pos)
			}
			if b.Consumer.IndexConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index expressions", r.Pos)
			}
			if b.Clause.AggregateClause == "VALUES" || b.Clause.AggregateClause == "INSERT VALUES" && (r.Name == "string_agg" || r.Name == "array_agg") {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in VALUES", r.Pos)
			}
			if b.Consumer.CheckConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in check constraints", r.Pos)
			}
			if b.Consumer.RoutineDefault {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in DEFAULT expressions", r.Pos)
			}
			var err error
			var returning sqltype.Usage
			if ownedAggregate != nil {
				node, err = b.bindOwnedAggregate(r, ownedAggregate, diagnosticName)
			} else if definition, ok := functions.LookupAggregate(r.Name); ok {
				node, returning, err = b.BindRegisteredAggregate(r, name, definition)
			} else if r.Name == "int_array_aggregate" {
				node, err = b.bindIntaggAggregate(r, name)
			} else {
				// Every other name binds as count, for its 42883 diagnostics.
				node, err = functions.BindCount(b, r, name)
			}
			if err != nil {
				return nil, err
			}
			node, err = b.ownAggregate(node)
			if err != nil {
				return nil, err
			}
			if returning.Type != nil {
				node, err = b.aggregateReturning(r, node, returning)
				if err != nil {
					return nil, err
				}
			}
		case parser.CaseExpr:
			var err error
			node, err = b.bindCase(r)
			if err != nil {
				return nil, err
			}
		case parser.ConditionalExpr:
			var err error
			node, err = b.bindConditional(r)
			if err != nil {
				return nil, err
			}
		case parser.XMLDocumentPredicateExpr:
			var err error
			node, err = b.bindXMLDocumentPredicate(r)
			if err != nil {
				return nil, err
			}
		case parser.XMLConstructExpr:
			var err error
			node,err=b.bindXMLConstruct(r)
			if err!=nil{return nil,err}
		case parser.XMLParseExpr:
			var err error
			node, err = b.bindXMLParse(r)
			if err != nil {
				return nil, err
			}
		case parser.XMLSerializeExpr:
			var err error
			node, err = b.bindXMLSerialize(r)
			if err != nil {
				return nil, err
			}
		case parser.SQLJSONQueryExpr:
			var err error
			node, err = functions.BindSQLJSONQuery(b, r)
			if err != nil {
				return nil, err
			}
		case parser.SQLJSONExpr:
			var err error
			node, err = b.bindSQLJSON(r)
			if err != nil {
				return nil, err
			}
		case parser.JSONPredicateExpr:
			var err error
			node, err = functions.BindJSONPredicate(b, r)
			if err != nil {
				return nil, err
			}
		case parser.FunctionExpr:
			if b.Params.ExecuteParameters && r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in EXECUTE parameters", r.Pos)
			}
			var err error
			node, err = b.BindFunction(r)
			if err != nil {
				return nil, err
			}
			switch inquiry := node.(type) {
			case plan.ExtensionPathsExpr:
				b.Consumer.CatalogSnapshot = true
			case expr.SnapshotDemander:
				demand, _, _ := inquiry.SnapshotDemand(plan.NullLiteral)
				b.Consumer.CatalogSnapshot = b.Consumer.CatalogSnapshot || demand
			}
			if r.Window != nil {
				// Live catalog selection can rebind ordinary FunctionExpr
				// syntax to a window provider through its immutable symbol.
				// Keep that bound shape; scalar providers still reject OVER.
				if _, window := node.(*plan.WindowExpr); !window {
					return nil, expr.ErrorAt("42809", "OVER specified, but "+catalog.ObjectNameText(r.Name)+" is not a window function nor an aggregate function", r.Pos)
				}
			}
		case parser.CallExpr:
			var err error
			node, err = functions.BindSequenceCall(b, r)
			if err != nil {
				return nil, err
			}
		case parser.ArrayExpr:
			var err error
			var target sqltype.Usage
			if policy.Mode == sqltype.Explicit {
				target = policy.Expected
			}
			node, err = b.BindArray(r, target)
			if err != nil {
				return nil, err
			}
		case parser.SubscriptExpr:
			var err error
			node, err = b.BindSubscript(r)
			if err != nil {
				return nil, err
			}
		case parser.RegclassExpr:
			var err error
			node, err = b.Cast(parser.CastExpr{Child: r.Child, TypeName: parser.TypeName{Name: "regclass", Pos: r.Pos}, Syntax: parser.StandardCast, Pos: r.Pos}, policy)
			if err != nil {
				return nil, err
			}
		case parser.ColumnExpr:
			if pseudo, matched, err := b.RuleColumn(r); matched {
				if err != nil {
					return nil, err
				}
				node = pseudo
				break
			}
			if b.Consumer.DefaultConsumer && !b.Consumer.RoutineDefault {
				return nil, expr.ErrorAt("0A000", "cannot use column reference in DEFAULT expression", r.Pos)
			}
			var err error
			node, err = b.Column(r)
			if err != nil {
				return nil, err
			}
		case parser.ParameterExpr:
			if b.PL != nil {
				var err error
				node, err = b.plParameter(r)
				if err != nil {
					return nil, err
				}
				break
			}
			if b.Stored.CheckLiterals != nil {
				if r.Number <= 0 || r.Number > len(b.Stored.CheckLiterals) {
					return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
				}
				node = b.Stored.CheckLiterals[r.Number-1]
				break
			}
			if b.Params.NoParameters || b.Consumer.DefaultConsumer || r.Number <= 0 || b.Params.ClosedRoutineFormals && r.Number > len(b.Params.formalParameters) {
				return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
			}
			u := policy.Expected
			if established := b.Params.updateInputParameters[r.Number]; established.Type != nil {
				u = established
			}
			retained := r.Number <= len(b.Params.formalParameters) && b.Params.formalParameters[r.Number-1].Type != nil
			if retained {
				u = b.Params.formalParameters[r.Number-1]
			}
			// Retained parameter contracts keep their logical input type, but their
			// codec must come from the admitted catalog, including private generations.
			if b.Expected.Held && r.Number <= len(b.Expected.Parameters.Types) {
				u = b.Expected.Parameters.Types[r.Number-1]
				retained = u.Type != nil
			}
			if retained {
				catalog := b.BindingCatalog()
				if b.Expected.Held && StatementCatalogType(u.Type) && b.Expected.CatalogOwner != nil &&
					(b.Expected.CatalogOwner != catalog.Owner() || b.Expected.CatalogEpoch != catalog.Epoch()) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				current, _ := catalog.LookupType(u.Type.ID())
				if u.Type.IsDeferredRecord() || u.Type.ID() == 2249 && u.Type.IsComposite() {
					// Anonymous shapes and deferred RECORD are formal occurrence
					// authorities, never catalog's lookup-only RECORD identity.
					current = b.Params.Parameters[r.Number].Type
				}
				if current == nil && StatementCatalogType(u.Type) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				u.Type = current
				b.Params.Parameters[r.Number] = u
			}
			if u.Type == nil {
				u = b.Params.Parameters[r.Number]
			}
			if u.Type == nil {
				b.Params.Parameters[r.Number] = sqltype.Usage{}
				node = plan.ParameterExpr{Info: expr.Info{Pos: r.Pos, Property: expr.Properties{ParameterDependent: true}}, Number: r.Number}
			} else {
				if err := b.Constrain(r.Number, u, r.Pos); err != nil {
					return nil, err
				}
				info := b.Info(u.Type.Usage(), r.Pos, expr.Properties{ParameterDependent: true})
				if b.Params.ClosedRoutineFormals && b.Params.routineInputCollation.ID != 0 && info.Usage.Collation.ID != 0 {
					// Function parameters inherit the effective input collation
					// implicitly; the caller's COLLATE origin never crosses here.
					info.Usage.Collation = b.Params.routineInputCollation
				}
				node = plan.ParameterExpr{Info: info, Number: r.Number}
			}
		case parser.LiteralExpr:
			v := r.Value
			u := policy.Expected
			var value sqltype.Value
			var err error
			switch v.Kind {
			case parser.NullValue:
				if u.Type == nil {
					u = b.Type(sqltype.Text).Usage()
				}
				if u.Type.IsDomain() {
					// Fresh NULL is an unconstrained physical input. The outer
					// assignment owns inherited typmods and domain constraints.
					u = u.BaseUsage().Type.Usage()
				}
				value = sqltype.Null(u.Type)
			case parser.BitStringValue:
				value, err = b.InputConstant(v.Text, b.Type(sqltype.Bit).Usage(), v.Pos)
			case parser.StringValue:
				if u.Type == nil {
					u = b.Type(sqltype.Text).Usage()
				}
				inputUsage := u.Type.Usage()
				if u.Type.ID() == sqltype.Interval {
					inputUsage = u
				}
				if u.Type.IsDomain() {
					inputUsage = u.BaseUsage()
					if inputUsage.Type.ID() != sqltype.Interval {
						inputUsage = inputUsage.Type.Usage()
					}
				}
				value, err = b.InputConstant(v.Text, inputUsage, v.Pos)
			case parser.NumericValue:
				value, err = b.InputConstant(v.Text, b.Type(sqltype.Numeric).Usage(), v.Pos)
			case parser.IntegerValue:
				id := sqltype.Int4
				if v.Integer < -2147483648 || v.Integer > 2147483647 {
					id = sqltype.Int8
				}
				value, err = sqltype.Integer(b.Type(id), v.Integer)
			case parser.BooleanValue:
				value = sqltype.Boolean(b.Type(sqltype.Bool), v.Boolean)
			default:
				return nil, fmt.Errorf("stuntgres: invalid scalar literal")
			}
			if err != nil {
				return nil, literalInputError(err, r)
			}
			literalUsage := value.Type().Usage()
			if v.Kind == parser.StringValue && u.Type.BaseType().ID() == sqltype.Interval {
				literalUsage = u.BaseUsage()
			}
			info := b.Info(literalUsage, v.Pos, expr.Properties{})
			node = plan.LiteralExpr{Info: info, Value: value}
			if literalUsage.Type.ID() == sqltype.Regclass && !value.IsNull() {
				text, _ := value.Text()
				oid, err := strconv.ParseUint(text, 10, 32)
				if err != nil {
					return nil, err
				}
				auth, err := b.BindingAuthorization()
				if err != nil {
					return nil, err
				}
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
				if descriptor, exists := auth.ReadCatalog().Descriptor(address); exists {
					b.dependency(b.Host.CatalogOwner().Domain.Ref(auth.ReadCatalog().Epoch(), address), descriptor.Revision())
				}
			}
		case parser.TypedLiteralExpr:
			rawType := r.TypeName
			saved, found, err := b.CaptureAt(r.Pos.Offset)
			if err != nil {
				return nil, err
			}
			if found {
				rawType.Modifiers = nil
			}
			u, err := b.Host.ResolveType(rawType)
			if err != nil {
				return nil, err
			}
			if r.RequiresDomain && u.Type.BaseType().ID() != sqltype.XML && !u.Type.IsDomain() && !u.Type.IsLquery() && !u.Type.IsLtree() && !u.Type.IsLtxtquery() {
				return nil, expr.ErrorAt("0A000", "typed literals are unsupported", rawType.Pos)
			}
			if found {
				u.Typmod = saved.Typmod
				if saved.Typed {
					u.Typmod = saved.TypedTypmod
				}
			}
			inputUsage := u.BaseUsage()
			if inputUsage.Type.ID() != sqltype.Interval {
				inputUsage = inputUsage.Type.Usage()
			}
			value, err := b.InputConstant(r.Text, inputUsage, r.Pos)
			if err != nil {
				return nil, InputErrorAt(err, r.Pos)
			}
			if b.collectTemporalConstants() {
				for i := range b.Prepared.Temporals {
					if b.Prepared.Temporals[i].Offset == r.Pos.Offset && b.Prepared.Temporals[i].TypeID == u.Type.ID() {
						b.Prepared.Temporals[i].Typed = true
						b.Prepared.Temporals[i].TypedTypmod = u.Typmod
					}
				}
			}
			literalInfo := b.Info(inputUsage, r.Pos, expr.Properties{})
			node = plan.LiteralExpr{Info: literalInfo, Value: value}
			if inputUsage != u {
				coercionPlan, err := sqltype.PlanCoercion(inputUsage, u, sqltype.Explicit)
				if err != nil {
					return nil, err
				}
				info, err := b.coercionInfo(b.Info(u, r.Pos, CoercionProperties(node, coercionPlan)), node)
				if err != nil {
					return nil, err
				}
				node = plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Explicit}
			}
		case parser.CollateExpr:
			child, err := b.Bind(r.Child, policy)
			if err != nil {
				return nil, err
			}
			name := r.Name
			name.Parts = append([]parser.Identifier(nil), name.Parts...)
			name.Parts[0].Pos = r.Pos
			usage, err := b.Host.ResolveColumnCollation(child.ResultUsage(), &name)
			if err != nil {
				return nil, err
			}
			info := b.Info(usage, r.Pos, child.Properties())
			info.CollationExplicit = r.Pos
			node = plan.CollationExpr{Info: info, Child: child}
		case parser.CastExpr:
			var err error
			node, err = b.Cast(r, policy)
			if err != nil {
				return nil, err
			}
		case parser.TimeZoneExpr:
			var err error
			node, err = functions.BindTimeZone(b, r)
			if err != nil {
				return nil, err
			}
		case parser.LogicalExpr:
			var err error
			node, err = b.bindLogical(r, policy.Context)
			if err != nil {
				return nil, err
			}
		case parser.PredicateExpr:
			if policy.Context == AssignmentContext {
				return nil, fmt.Errorf("stuntgres: predicate in assignment scalar")
			}
			var err error
			node, err = b.bindPredicate(r, policy.Context)
			if err != nil {
				return nil, err
			}
		case parser.InListExpr:
			var err error
			node, err = b.bindInList(r)
			if err != nil {
				return nil, err
			}
		case parser.ArrayComparisonExpr:
			var err error
			node, err = b.bindArrayComparison(r)
			if err != nil {
				return nil, err
			}
		case parser.OperatorExpr:
			var err error
			node, err = b.BindCatalogOperator(r, policy)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("stuntgres: unsupported scalar node")
		}
	}
	if b.Consumer.StatisticsConsumer && exec.SourceReturnsSet(node) {
		return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in statistics expressions", node.SourcePosition())
	}
	if b.Consumer.CopyWhere && exec.SourceReturnsSet(node) {
		return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in COPY FROM WHERE conditions", node.SourcePosition())
	}
	if b.Clause.FunctionProjection.matches(raw) && b.Clause.FunctionProjection.Operand == nil && node.ResultUsage().Type != nil && node.ResultUsage().Type.Concrete() && node.ResultUsage().Type.BaseType().IsComposite() {
		b.Clause.FunctionProjection.Operand = node
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.ID() != sqltype.Bool {
		var err error
		node, err = b.coerceAssignmentForConstruct(node, b.Type(sqltype.Bool).Usage(), policy.booleanConstruct, raw)
		if err != nil {
			return nil, err
		}
	}
	// Logical children already passed this policy independently. Do not walk
	// every prefix again when binding a long AND/OR chain.
	if _, logical := node.(plan.LogicalExpr); policy.booleanConstruct != "" && !logical {
		returnsSet := false
		plan.WalkExpression(node, func(child expr.Expr) {
			returnsSet = returnsSet || exec.SourceReturnsSet(child)
		})
		if returnsSet {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must not return a set", policy.booleanConstruct), parser.ExpressionStart(raw))
		}
	}
	if !policy.deferAssignment && (policy.Context == AssignmentContext || policy.Context == FunctionArgumentContext) && node.ResultUsage() != policy.Expected {
		if err := b.CaptureTypeDiagnosticInput(policy.Expected.Type); err != nil {
			return nil, err
		}
		if policy.Expected.Type.ContainsCatalogIdentifier() {
			if err := b.captureElementInput(); err != nil {
				return nil, err
			}
		}
		input, annotations := DomainCoercionInput(node, policy.Expected)
		info := b.Info(policy.Expected, raw.SourcePosition(), input.Properties())
		var err error
		if policy.Context == AssignmentContext {
			info, err = StorageCoercionInfo(b.BindingCatalog(), info, input)
		} else {
			info, err = b.coercionInfo(info, input)
		}
		if err != nil {
			return nil, err
		}
		if selected, handled, selectionErr := b.declaredCoercion(input, info.Usage, policy.Mode, raw.SourcePosition(), raw); handled {
			if selectionErr != nil {
				if diagnostic, ok := selectionErr.(*sqlstate.Error); ok && diagnostic.Code == "42804" && policy.Column != "" {
					copy := *diagnostic
					copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", policy.Column, b.DiagnosticTypeName(policy.Expected.Type), b.DiagnosticTypeName(node.ResultUsage().Type))
					copy.Hint = "You will need to rewrite or cast the expression."
					copy.Position = strconv.Itoa(parser.ExpressionStart(raw).Character)
					return node, &copy
				}
				return node, selectionErr
			}
			return RestoreDomainCastCollations(annotations, selected), nil
		}
		coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, policy.Mode)
		if err != nil {
			// Retain the resolved source for the assignment caller's diagnostic.
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" && policy.Column != "" {
				copy := *d
				copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", policy.Column, b.DiagnosticTypeName(policy.Expected.Type), b.DiagnosticTypeName(node.ResultUsage().Type))
				copy.Hint = "You will need to rewrite or cast the expression."
				copy.Position = strconv.Itoa(parser.ExpressionStart(raw).Character)
				return node, &copy
			}
			return node, err
		}
		if coercionPlan.RequiresCatalogOutput() {
			b.Prepared.RegclassConcat = true
		}
		info.Property = CoercionProperties(input, coercionPlan)
		if folded, ok, err := b.foldRegtypeConstant(input, coercionPlan, info); ok || err != nil {
			if err != nil {
				return nil, err
			}
			node = RestoreDomainCastCollations(annotations, folded)
		} else {
			node = RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: policy.Mode})
		}
		if policy.Context == AssignmentContext {
			var err error
			node, err = StorageDestination(node, policy.Expected, raw.SourcePosition())
			if err != nil {
				return nil, err
			}
		}
	}
	return node, nil
}
