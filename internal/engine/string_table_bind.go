package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func isStringSet(expr expr.Expr) bool {
	e, ok := expr.(functions.String)
	return ok && e.Plan.Name() == "string_to_table"
}
func isQuerySet(expr expr.Expr) bool {
	if _, ok := expr.(catalogForeignKeysExpression); ok {
		return true
	}
	if call, ok := expr.(transactionIdentityExpression); ok {
		return call.kind == identityMembers
	}
	if call, ok := expr.(aclValueExpression); ok {
		return call.kind == aclExplode
	}
	if record, ok := expr.(functions.JSONRecord); ok {
		return record.Plan.ReturnsSet()
	}
	if call, ok := expr.(functions.JSONBRecord); ok {
		return call.ReturnsSet()
	}
	switch expr.(type) {
	case functions.JSONBPathQuery:
		return true
	case functions.RegexpSet, functions.JSONKeys, functions.JSONEach, functions.JSONElements, functions.JSONBElements, seriesExpression, subscriptsExpression, unnestExpression:
		return true
	}
	return isStringSet(expr)
}
func validFunctionSourceCall(expr expr.Expr) bool {
	switch call := expr.(type) {
	case functions.JSONBPathQuery, catalogUserNameExpression, catalogForeignKeysExpression:
		return true
	case aclValueExpression:
		return call.kind != aclContains
	case objectAddressExpression, functions.JSONBRecord:
		return true
	case transactionIdentityExpression:
		return true
	case commitTimestampExpression, functions.JSONRecord:
		return true
	case functions.String:
		return isStringSet(call)
	case functions.RegexpSet:
		return call.Plan.ReturnsSet()
	case functions.JSONKeys, functions.JSONEach, functions.JSONElements, functions.JSONBElements, seriesExpression, subscriptsExpression, unnestExpression:
		return true
	case functions.RangeInspect:
		return call.Plan.Operation() == sqltype.RangeConstruct
	case functions.Binary:
		return parser.JSONFunctionName(call.Plan.Name())
	case functions.Regexp:
		return !call.Plan.ReturnsSet()
	default:
		return false
	}
}

// Selected rendering and binding share a lane; typed admission remains owned
// by validFunctionSourceCall after ordinary function resolution.
func selectedFunctionSourceName(name string) bool {
	return name == "jsonb_path_query" || parser.RegexpFunctionName(name) || parser.JSONFunctionName(name) ||
		parser.CatalogRelationFunctionName(name) ||
		parser.ObjectAddressFunctionName(name) || parser.CommitTimestampFunctionName(name) || parser.TransactionIdentityFunctionName(name) ||
		name == "age" || name == "aclexplode" || name == "acldefault" || name == "makeaclitem" || name == "int_array_enum" || name == "generate_series" || name == "generate_subscripts" || name == "unnest" || name == "multirange"
}

// A selected scalar OUT name controls the column label, not the range alias.
func functionScalarOutName(expr expr.Expr) string {
	switch expr.(type) {
	case functions.JSONElements, functions.JSONBElements:
		return "value"
	}
	return ""
}

// FROM placement follows successful call completion, not lexical offsets.
// enterQuery isolates this observation from expression-owned child queries.
type fromSetObservation struct {
	generation uint64
	pos        parser.Position
}

func nestedFromSetError(pos parser.Position) error {
	return expr.ErrorAt("0A000", "set-returning functions must appear at top level of FROM", pos)
}

func (b *expressionBinder) bindFunctionSource(raw parser.FunctionExpr) (expr.Expr, error) {
	before := b.fromSetObservation
	expr, err := b.bindFunction(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := expr.(objectAddressExpression); ok {
		b.catalogSnapshot = true
	}
	if !isQuerySet(expr) && b.fromSetObservation != before {
		return nil, nestedFromSetError(b.fromSetObservation.pos)
	}
	return expr, nil
}
func functionCallArguments(bound expr.Expr) []expr.Expr {
	switch call := bound.(type) {
	case functions.JSONBPathQuery:
		return call.Arguments()
	case catalogUserNameExpression:
		return []expr.Expr{call.arg}
	case catalogForeignKeysExpression:
		return nil
	case aclValueExpression:
		return call.arguments()
	case objectAddressExpression:
		return call.args[:]
	case functions.JSONRecord:
		return call.Args
	case functions.JSONBRecord:
		return call.Args
	case transactionIdentityExpression:
		if call.arg != nil {
			return []expr.Expr{call.arg}
		}
		return nil
	case commitTimestampExpression:
		if call.arg != nil {
			return []expr.Expr{call.arg}
		}
		return nil
	case seriesExpression:
		return call.args
	case subscriptsExpression:
		return call.args
	case unnestExpression:
		return call.args
	case functions.RangeInspect:
		return call.Args
	case functions.String:
		return call.Args
	case functions.RegexpSet:
		return call.Args
	case functions.JSONKeys:
		return call.Args
	case functions.JSONBElements:
		return call.Args
	case functions.JSONEach:
		return call.Args
	case functions.JSONElements:
		return call.Args
	case functions.Binary:
		return call.Args
	case functions.Regexp:
		return call.Args
	default:
		return nil
	}
}
func withFunctionCallArguments(bound expr.Expr, args []expr.Expr) (expr.Expr, error) {
	switch call := bound.(type) {
	case functions.JSONBPathQuery:
		if len(args) != 4 {
			return nil, fmt.Errorf("invalid JSONPath query arguments")
		}
		return call.Reconstruct(call.Info, [4]expr.Expr{args[0], args[1], args[2], args[3]})
	case catalogUserNameExpression:
		if len(args) != 1 {
			return nil, fmt.Errorf("stuntgres: invalid catalog role-name arguments")
		}
		call.arg = args[0]
		return call, nil
	case catalogForeignKeysExpression:
		if len(args) != 0 {
			return nil, fmt.Errorf("stuntgres: invalid catalog foreign-key arguments")
		}
		return call, nil
	case aclValueExpression:
		if len(args) != len(call.arguments()) {
			return nil, fmt.Errorf("stuntgres: invalid ACL call arguments")
		}
		copy(call.args[:], args)
		return call, nil
	case objectAddressExpression:
		if len(args) != 3 {
			return nil, fmt.Errorf("stuntgres: invalid object address arguments")
		}
		copy(call.args[:], args)
		return call, nil
	case functions.JSONRecord:
		return call.Reconstruct(call.Info, args)
	case functions.JSONBRecord:
		return call.Reconstruct(call.Info, args)
	case transactionIdentityExpression:
		if (call.arg == nil) != (len(args) == 0) || len(args) > 1 {
			return nil, fmt.Errorf("stuntgres: invalid transaction identity arguments")
		}
		if len(args) == 1 {
			call.arg = args[0]
		}
		return call, nil
	case commitTimestampExpression:
		if len(args) != 0 {
			if call.arg == nil || len(args) != 1 {
				return nil, fmt.Errorf("stuntgres: invalid commit timestamp arguments")
			}
			call.arg = args[0]
		} else if call.arg != nil {
			return nil, fmt.Errorf("stuntgres: missing commit timestamp argument")
		}
		return call, nil
	case seriesExpression:
		if len(args) != len(call.args) {
			return nil, fmt.Errorf("stuntgres: invalid series arguments")
		}
		call.args = args
		return call, nil
	case subscriptsExpression:
		if len(args) != len(call.args) {
			return nil, fmt.Errorf("stuntgres: invalid subscripts arguments")
		}
		call.args = args
		return call, nil
	case unnestExpression:
		if len(args) != 1 {
			return nil, fmt.Errorf("stuntgres: invalid unnest arguments")
		}
		call.args = args
		return call, nil
	case functions.RangeInspect:
		return call.Reconstruct(call.Info, args)
	case functions.String:
		call.Args = args
		return call, nil
	case functions.RegexpSet:
		call.Args = args
		return call, nil
	case functions.JSONKeys:
		if len(args) != 1 {
			return nil, fmt.Errorf("stuntgres: invalid JSON keys arguments")
		}
		call.Args = args
		return call, nil
	case functions.JSONBElements:
		return call.Reconstruct(call.Info, args)
	case functions.JSONEach:
		if len(args) != 1 {
			return nil, fmt.Errorf("stuntgres: invalid JSON each arguments")
		}
		call.Args = args
		return call, nil
	case functions.JSONElements:
		if len(args) != 1 {
			return nil, fmt.Errorf("stuntgres: invalid JSON elements arguments")
		}
		call.Args = args
		return call, nil
	case functions.Binary:
		call.Args = args
		return call, nil
	case functions.Regexp:
		call.Args = args
		return call, nil
	default:
		return nil, fmt.Errorf("stuntgres: invalid closed function call")
	}
}

func (b *expressionBinder) stringSetAllowed(pos parser.Position) error {
	kind := b.aggregateClause
	if b.defaultConsumer {
		kind = "DEFAULT expressions"
	}
	if b.checkConsumer {
		kind = "check constraints"
	}
	if b.generatedOwner != nil {
		kind = "column generation expressions"
	}
	if b.indexConsumer {
		kind = "index expressions"
	}
	if b.indexPredicateConsumer {
		kind = "index predicates"
	}
	hint := ""
	if b.stringSetGuard != "" {
		kind = b.stringSetGuard
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	}
	message := "set-returning functions are not allowed in " + kind
	if b.argumentOwner.active && kind != "FILTER" {
		message = "aggregate function calls cannot contain set-returning function calls"
		if b.argumentOwner.window {
			message = "window function calls cannot contain set-returning function calls"
		}
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	} else if b.stringSetGuard == "" {
		switch kind {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions", "FROM":
			return nil
		case "window RANGE", "window GROUPS", "window ROWS":
			message = "set-returning functions are not allowed in window definitions"
		case "INSERT VALUES":
			if b.stringSingleInsert {
				return nil
			}
			message = "set-returning functions are not allowed in VALUES"
		}
	}
	err := expr.ErrorAt("0A000", message, pos).(*sqlstate.Error)
	err.Hint = hint
	return err
}
func stringLateralError(q queryQualifier, pos parser.Position, target bool) error {
	err := expr.ErrorAt("42P10", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, q.name), pos).(*sqlstate.Error)
	if target {
		err.Hint = fmt.Sprintf(`There is an entry for table "%s", but it cannot be referenced from this part of the query.`, q.name)
	} else {
		err.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
	}
	return err
}
func (b *expressionBinder) bindStringTable(raw *parser.FunctionTable) (LogicalNode, queryNamespace, error) {
	old, clause := b.queryNamespace, b.aggregateClause
	if b.queryNamespace == nil {
		b.queryNamespace = &queryNamespace{}
	}
	b.aggregateClause = "FROM"
	defer func() { b.queryNamespace, b.aggregateClause = old, clause }()
	calls := make([]expr.Expr, len(raw.Calls))
	schema := make(querySchema, len(calls))
	ordinal := b.viewFunctionCount
	b.viewFunctionCount += len(raw.Calls)
	for i, call := range raw.Calls {
		bound, err := b.bindFunctionSource(call)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		if len(call.ColumnDefinitions) != 0 {
			return nil, queryNamespace{}, expr.ErrorAt("42601", functionColumnDefinitionMessage(bound), call.ColumnDefinitions[0].Name.Pos)
		}
		if !isStringSet(bound) {
			return nil, queryNamespace{}, expr.ErrorAt("0A000", "function table source is unsupported", call.Pos)
		}
		calls[i] = bound
		schema[i] = catalog.Column{Name: "string_to_table", Usage: bound.ResultUsage()}
		if b.noParameters || b.materializedDefinition {
			if err := b.captureViewLiterals(call, bound, viewFunctionLiteral, ordinal+i); err != nil {
				return nil, queryNamespace{}, err
			}
		}
	}
	if raw.Ordinality {
		schema = append(schema, catalog.Column{Name: "ordinality", Usage: b.typ(sqltype.Int8).Usage()})
	}
	columns := make([]queryColumn, len(schema))
	for i, col := range schema {
		columns[i] = queryColumn{descriptor: col, slot: i, leaf: -1, physical: -1}
	}
	name := raw.Calls[0].Name.Parts[len(raw.Calls[0].Name.Parts)-1].Text
	// One scalar call remains scalar even in ROWS FROM; ordinality and
	// multiple calls declare tuples independently of their exposed labels.
	qualifier := queryQualifier{name: name, original: name, columns: columns, scalarAlias: len(calls) == 1 && !raw.Ordinality}
	b.queryHidden = append(b.queryHidden, qualifier)
	captures := b.functionCaptures(calls)
	node := &functionFromNode{querySchema: schema, calls: calls, ordinality: raw.Ordinality, correlated: len(captures) != 0, frame: b.frameID(), viewOrdinal: ordinal, captures: captures}
	return node, queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: schema}, nil
}

// Only local expressions are rewritten; expression-owned queries retain their
// own stage/frame ownership through the existing mapper.
func (b *expressionBinder) mapQuerySetStage(bound expr.Expr, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error) {
	mapped, _, err := mapBoundExpression(b.bindingCatalog(), bound, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, func(n LogicalNode) (LogicalNode, error) { return n, nil }, nil, func(e expr.Expr) (expr.Expr, bool, error) {
		// Window slots are assigned through shared binder-owned pointers. A
		// local SRF rewrite must not clone them before their final slot shift.
		if _, ok := e.(*windowExpression); ok {
			return e, true, nil
		}
		return leaf(e)
	})
	return mapped, err
}

func functionFrameReference(bound expr.Expr, frame uint32) bool {
	found := false
	walkQueryArgumentExpression(bound, func(node expr.Expr) {
		if column, ok := node.(columnExpression); ok && column.frame == frame {
			found = true
		}
	})
	return found
}
func stringForbiddenColumn(ns queryNamespace, slot int, pos parser.Position) error {
	for _, qualifier := range ns.qualifiers {
		for _, column := range qualifier.columns {
			if column.slot == slot {
				return stringLateralError(qualifier, pos, false)
			}
		}
	}
	return fmt.Errorf("stuntgres: missing forbidden function predecessor namespace")
}

func (b *expressionBinder) stringFromContext() bool {
	if b.aggregateClause == "FROM" {
		return true
	}
	for _, scope := range b.queryScopes {
		if scope.clause == "FROM" {
			return true
		}
	}
	return false
}

func (b *expressionBinder) functionCaptures(calls []expr.Expr) []functionCorrelation {
	var captures []functionCorrelation
	for _, call := range calls {
		for _, arg := range functionCallArguments(call) {
			walkQueryArgumentExpression(arg, func(expr expr.Expr) {
				column, ok := expr.(columnExpression)
				if !ok || column.frame != b.frameID() {
					return
				}
				name := ""
				for _, qualifier := range b.queryNamespace.qualifiers {
					for _, candidate := range qualifier.columns {
						if candidate.slot == column.slot {
							name = qualifier.name
							break
						}
					}
					if name != "" {
						break
					}
				}
				captures = append(captures, functionCorrelation{frame: column.frame, slot: column.slot, name: name, pos: column.Pos})
			})
		}
	}
	return captures
}
