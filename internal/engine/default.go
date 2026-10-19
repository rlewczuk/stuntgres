package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Raw and cooked DEFAULT consumers share the physical TEMP provider boundary.
// Cooked copies supply no position: their original call token is not in LIKE SQL.
func validateDefaultTemporaryProvider(provider, consumer ids.SessionToken, position parser.Position) error {
	if provider != (ids.SessionToken{}) && provider != consumer {
		return expr.ErrorAt("0A000", "temporary sequence DEFAULT is unsupported", position)
	}
	return nil
}

func validateCookedDefaultTemporary(node expr.Expr, consumer ids.SessionToken) error {
	var err error
	walkBoundExpression(node, func(node expr.Expr) {
		if err != nil {
			return
		}
		if call, ok := node.(sequenceCallExpression); ok {
			err = validateDefaultTemporaryProvider(call.temporary, consumer, parser.Position{})
		}
	})
	return err
}

func (b *expressionBinder) bindDefault(raw parser.Expr, column catalog.Column) (expr.Expr, error) {
	// Recursive binding carries this consumer policy through Boolean and
	// comparison children, preserving their ordinary diagnostic order.
	previous := b.defaultConsumer
	b.defaultConsumer = true
	defer func() { b.defaultConsumer = previous }()
	inputUsage := column.Usage.Type.Usage()
	if inputUsage.Type.ID() == sqltype.Interval {
		inputUsage = column.Usage
	}
	node, err := b.bind(raw, expressionPolicy{context: projectionContext, expected: inputUsage})
	if err != nil {
		return nil, err
	}
	if node.Properties().Volatility > expr.Stable && !closedSequenceDefault(node) && !closedUUIDDefault(node) && !closedTransactionTimeDefault(node) && !closedPgcryptoDefault(node) {
		return nil, expr.ErrorAt("0A000", "unsupported volatile DEFAULT expression", parser.ExpressionStart(raw))
	}
	if node.ResultUsage() != column.Usage {
		input, annotations := domainCoercionInput(node, column.Usage)
		info, err := storageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, input.Properties()), input)
		if err != nil {
			return nil, err
		}
		plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42804" {
				d := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
				d.Hint = "You will need to rewrite or cast the expression."
				return nil, d
			}
			return nil, err
		}
		info.Property = coercionProperties(input, plan)
		node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
	}
	return storageDestination(node, column.Usage, parser.Position{})
}

func bindCreateDefaults(b *expressionBinder, columns []catalog.Column, declarations []createColumnDeclaration) (map[int32]catalog.ColumnDefault, error) {
	var defaults map[int32]catalog.ColumnDefault
	auth, err := b.bindingAuthorization()
	if err != nil {
		return nil, err
	}
	view := auth.Catalog
	for slot := range declarations {
		raw := declarations[slot].defaultExpr
		if raw == nil {
			continue
		}
		node, err := b.bindDefault(raw, columns[slot])
		if err != nil {
			return nil, err
		}
		if structuralNull(node) {
			continue
		}
		definition := catalog.ColumnDefault{Expression: node}
		_, err = (scalarRenderer{definition: b.definitionReader(), aclOutput: capturedACLItemOutput{owner: b.engine, roles: auth.Roles}, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot, catalogAliasReferences: &definition.CatalogAliasReferences}).sql(b.ctx, view, node)
		if err != nil {
			return nil, err
		}
		if defaults == nil {
			defaults = make(map[int32]catalog.ColumnDefault)
		}
		defaults[int32(slot+1)] = definition
	}
	return defaults, nil
}

// One recursive serializer validates persisted scalar trees without Eval.
// DEFAULT admits closed Immutable/Stable trees, selected UUID/time generation
// trees, genuine installed gen_salt recipes and fixed nextval leaves with
// coercion wrappers. CHECK also admits its owner row.
type scalarRenderer struct {
	// A cooking-only reader preserves authentic catalog identity while resolving
	// exact preceding metadata effects; ordinary serialization leaves it nil.
	definition definitionReader
	aclOutput  sqltype.ACLItemFormatter
	// Only provider validation may discard identity-only text. Real SQL output
	// and construction labels require explicit captured role presentation.
	validation             bool
	owner                  *catalog.Relation
	domainValue            *sqltype.Usage
	dependencies           *[]ids.ObjectRef
	commentSnapshot        *bool
	visibilitySnapshot     *bool
	catalogAliasReferences *bool
	provenance             *catalog.Relation
	capturedManifest       []ids.ObjectRef
	capturedChecks         []catalog.ExpressionDependency
	// TYPE reparsing transports concrete literals privately, without reinferring
	// unknown input or changing ordinary DEFAULT/CHECK serialization.
	checkLiterals *[]literalExpression
	// Final CHECK replay may compare a retained REAL literal with a widened
	// column. Preserve its exact promoted value, not shortest float32 input.
	checkReplayPrecision bool
	// The selected UUID shift's formal interval type must survive all-NULL
	// compositions. This context belongs only to its rendered child subtree.
	uuidShiftIntervalNulls bool
}

func defaultSQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	return (scalarRenderer{}).sql(ctx, view, node)
}

func renderColumnDefault(ctx context.Context, view catalog.Read, node expr.Expr) (string, []ids.ObjectRef, error) {
	var dependencies []ids.ObjectRef
	text, err := (scalarRenderer{dependencies: &dependencies}).sql(ctx, view, node)
	return text, dependencies, err
}

func (r scalarRenderer) sql(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil {
		return "", fmt.Errorf("stuntgres: missing default node")
	}
	u, p := node.ResultUsage(), node.Properties()
	if u.Type == nil || !u.Valid() || !u.Type.Concrete() || r.owner == nil && (r.domainValue == nil && p.Volatility > expr.Stable && !closedSequenceDefault(node) && !closedUUIDDefault(node) && !closedTransactionTimeDefault(node) && !closedPgcryptoDefault(node) || p.RowDependent) || p.ParameterDependent || p.StageDependent || p.DomainDependent && r.domainValue == nil {
		return "", fmt.Errorf("stuntgres: invalid stored default properties")
	}
	if err := validateUsageCollationAt(view, r.definition, u); err != nil {
		return "", err
	}
	if u.Collation.ID != 0 {
		r.recordStoredCollation(view, view.Owner().Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)}), u.Collation)
	}
	if !validExpressionUsageAt(view, r.definition, u) {
		return "", fmt.Errorf("stuntgres: foreign default type")
	}
	if err := r.recordStoredRoutine(view, node); err != nil {
		return "", err
	}
	r.recordStoredType(view, view.Owner().Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}))
	switch n := node.(type) {
	case domainValueExpression:
		if r.domainValue == nil || *r.domainValue != n.Usage {
			return "", fmt.Errorf("stuntgres: foreign stored domain VALUE")
		}
		return "VALUE", nil
	case rowExpression:
		parts := make([]string, len(n.fields))
		for i, field := range n.fields {
			text, err := r.sql(ctx, view, field)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		text := "ROW(" + strings.Join(parts, ", ") + ")"
		if u.Type.ID() != 2249 {
			declaration, err := catalogTypeDeclarationAt(view, r.definition, u)
			if err != nil {
				return "", err
			}
			text = "(" + text + ")::" + declaration
		}
		return text, nil
	case fieldExpression:
		text, err := r.sql(ctx, view, n.base)
		if err != nil {
			return "", err
		}
		index, ok := n.base.ResultUsage().Type.BaseType().CompositeAttributeIndex(n.attribute)
		if !ok {
			return "", fmt.Errorf("stuntgres: retired field in stored expression")
		}
		return "(" + text + ")." + pgIdentifier(n.base.ResultUsage().Type.BaseType().CompositeMember(index).Name), nil
	case arrayExpression:
		return r.arraySQL(ctx, view, n)
	case subscriptExpression:
		return r.subscriptSQL(ctx, view, n)
	case indexPropertyExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: index property inquiries are unsupported in stored defaults")
		}
		if err := validateStoredIndexPropertyExpression(n); err != nil {
			return "", err
		}
		if n.kind != accessMethodProperty && r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), n.children())
	case deparseExpression:
		if err := validateDeparseExpression(n); err != nil {
			return "", err
		}
		if r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		return r.functionSQL(ctx, view, "pg_catalog."+deparseSignatures[n.signature].name, n.args)
	case tableOIDExpression:
		if r.owner == nil || !storedReferenceEpochValid(view, r.owner, n.owner) || n.owner.Address != r.owner.Address() || u.Type.ID() != sqltype.Oid {
			return "", fmt.Errorf("stuntgres: invalid generated tableoid owner")
		}
		r.record(n.owner)
		return "tableoid", nil
	case columnExpression:
		if r.owner == nil || !storedReferenceEpochValid(view, r.owner, n.origin) || n.origin.Address.ClassID != r.owner.Address().ClassID || n.origin.Address.ObjectID != r.owner.Address().ObjectID || n.scope != 1 || n.frame != 1 || n.slot < 0 || n.slot >= len(r.owner.Columns) || r.owner.Attributes[n.slot] != n.origin.Address.SubID || u != r.owner.Columns[n.slot].Usage {
			return "", fmt.Errorf("stuntgres: invalid stored row origin")
		}
		r.record(n.origin)
		return catalog.QuoteIdentifier(r.owner.Columns[n.slot].Name), nil
	case advisoryExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: volatile stored default")
		}
		if !n.validStored() {
			return "", fmt.Errorf("stuntgres: invalid stored advisory signature")
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.name, n.args)
	case catalogUserNameExpression, catalogForeignKeysExpression, transactionIdentityExpression, commitTimestampExpression, installedFunctionExpression, installedUUIDOsspFunctionExpression, installedCryptoFunctionExpression, installedSoundexFunctionExpression, installedMetaphoneFunctionExpression, installedLevenshteinFunctionExpression:
		return r.scalarFunctionSQL(ctx, view, node)
	case visibilityInquiryExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: visibility inquiries are unsupported in stored defaults")
		}
		if n.kind.class() == 0 || u.Type == nil || u != u.Type.Usage() || u.Type.ID() != n.kind.result() || n.arg == nil {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry signature")
		}
		argument := n.arg.ResultUsage()
		if argument.Type == nil || argument != argument.Type.Usage() || argument.Type.ID() != n.kind.formal() {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry argument")
		}
		properties := n.arg.Properties()
		properties.Volatility = max(n.kind.volatility(), properties.Volatility)
		if p != properties {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry properties")
		}
		if r.visibilitySnapshot != nil {
			*r.visibilitySnapshot = true
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), []expr.Expr{n.arg})
	case commentInquiryExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: comment inquiries are unsupported in stored defaults")
		}
		name, arity, second := "obj_description", 2, sqltype.Name
		switch n.kind {
		case columnDescription:
			name, second = "col_description", sqltype.Int4
		case objectDescription:
		case legacyDescription:
			arity = 1
		case sharedDescription:
			name = "shobj_description"
		default:
			return "", fmt.Errorf("stuntgres: invalid stored comment inquiry kind")
		}
		if len(n.args) != arity || u != u.Type.Usage() || u.Type.ID() != sqltype.Text {
			return "", fmt.Errorf("stuntgres: invalid stored comment inquiry signature")
		}
		properties := expr.Properties{Volatility: expr.Stable}
		for i, arg := range n.args {
			id := sqltype.Oid
			if i == 1 {
				id = second
			}
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != id {
				return "", fmt.Errorf("stuntgres: invalid stored comment inquiry argument")
			}
			child := arg.Properties()
			properties.Volatility = max(properties.Volatility, child.Volatility)
			properties.RowDependent = properties.RowDependent || child.RowDependent
			properties.ParameterDependent = properties.ParameterDependent || child.ParameterDependent
			properties.StageDependent = properties.StageDependent || child.StageDependent
			properties.DomainDependent = properties.DomainDependent || child.DomainDependent
			properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || child.RuntimeDomainConversion
		}
		if p != properties {
			return "", fmt.Errorf("stuntgres: invalid stored comment inquiry properties")
		}
		if r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		return r.functionSQL(ctx, view, "pg_catalog."+name, n.args)
	case currentSchemasExpression:
		if n.arg == nil || n.arg.ResultUsage().Type == nil || n.arg.ResultUsage().Type.ID() != sqltype.Bool || u.Type.ID() != sqltype.NameArray {
			return "", fmt.Errorf("stuntgres: invalid stored current_schemas signature")
		}
		return r.functionSQL(ctx, view, "pg_catalog.current_schemas", []expr.Expr{n.arg})
	case objectAddressExpression:
		if r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		return n.sql("pg_catalog."+n.op.name(), func(arg expr.Expr) (string, error) { return r.sql(ctx, view, arg) })
	case functions.ObjectACL:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: object ACL inquiries are unsupported in stored defaults")
		}
		return r.scalarFunctionSQL(ctx, view, n)
	case functions.AccessInquiry:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: access inquiries are unsupported in stored defaults")
		}
		return r.scalarFunctionSQL(ctx, view, n)
	case namespaceInquiryExpression:
		if err := n.validateStored(); err != nil {
			return "", err
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), n.arguments())
	case blockingInquiryExpression:
		if err := n.validateStored(); err != nil {
			return "", err
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), n.arguments())
	case sequenceCallExpression:
		if r.owner == nil && !closedSequenceDefault(n) {
			return "", fmt.Errorf("stuntgres: invalid stored sequence default")
		}
		args := n.args
		want := 1
		switch n.name {
		case "nextval", "currval":
		case "lastval":
			want = 0
		case "setval":
			want = 2
		default:
			return "", fmt.Errorf("stuntgres: invalid stored sequence function")
		}
		if len(args) != want && !(n.name == "setval" && len(args) == 3) || u.Type.ID() != sqltype.Int8 {
			return "", fmt.Errorf("stuntgres: invalid stored sequence signature")
		}
		for i, arg := range args {
			id := sqltype.Regclass
			if i == 1 {
				id = sqltype.Int8
			}
			if i == 2 {
				id = sqltype.Bool
			}
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
				return "", fmt.Errorf("stuntgres: invalid stored sequence argument")
			}
		}
		if n.ref != (ids.ObjectRef{}) {
			if len(args) == 0 {
				return "", fmt.Errorf("stuntgres: stored sequence identity lacks argument")
			}
			if _, err := r.sql(ctx, view, args[0]); err != nil {
				return "", err
			}
			target, ok := view.Descriptor(n.ref.Address)
			if !ok || !storedSequenceEpochValid(view, r.retainedConsumer(), n.ref, n.temporary) {
				return "", fmt.Errorf("stuntgres: invalid stored sequence reference")
			}
			sequence, ok := target.(*catalog.Relation)
			if !ok || sequence.RelationKind != catalog.SequenceRelation {
				return "", fmt.Errorf("stuntgres: invalid stored sequence target")
			}
			r.record(n.ref)
			namespace, ok := view.Descriptor(sequence.Namespace())
			if !ok {
				return "", fmt.Errorf("stuntgres: missing stored sequence namespace")
			}
			name := catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(sequence.Name())
			parts := []string{"CAST('" + strings.ReplaceAll(name, "'", "''") + "' AS regclass)"}
			for _, arg := range args[1:] {
				text, err := r.sql(ctx, view, arg)
				if err != nil {
					return "", err
				}
				parts = append(parts, text)
			}
			return "pg_catalog." + n.name + "(" + strings.Join(parts, ", ") + ")", nil
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.name, args)
	case caseExpression, conditionalExpression:
		return r.conditionalSQL(ctx, view, node)
	case identityExpression:
		if n.ordinaryCall {
			return "pg_catalog." + n.kind.spelling() + "()", nil
		}
		return n.kind.spelling(), nil
	case literalExpression:
		if err := validateRegroleStoredLiteral(n.value); err != nil {
			return "", err
		}
		if n.RegclassRef != (ids.ObjectRef{}) {
			ref := n.RegclassRef
			target, ok := definitionDescriptorAt(view, r.definition, ref.Address)
			_, relationOK := target.(*catalog.Relation)
			if !ok || !relationOK || !storedReferenceEpochValid(view, r.retainedConsumer(), ref) || ref.Address.SubID != 0 || u.Type.ID() != sqltype.Regclass || n.value.IsNull() {
				return "", fmt.Errorf("stuntgres: invalid stored regclass reference")
			}
			r.record(ref)
		} else if n.value.Type().ID() == sqltype.Regclass && !n.value.IsNull() {
			// Actual scalar input datums retain relation dependencies; numeric
			// coercion trees are not materialized into these constants.
			text, err := n.value.Text()
			if err != nil {
				return "", err
			}
			oid, err := strconv.ParseUint(text, 10, 32)
			if err != nil {
				return "", err
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
			if _, exists := definitionDescriptorAt(view, r.definition, address); exists {
				r.record(view.Owner().Domain.Ref(view.Epoch(), address))
			}
		}
		if err := r.recordCatalogAliasDependencies(view, n.value); err != nil {
			return "", err
		}
		if err := r.recordRoutineReferences(view, n.value); err != nil {
			return "", err
		}
		if r.catalogAliasReferences != nil && (n.value.Type().ContainsRegtype() || n.value.Type().ContainsRegObject() || n.value.Type().ContainsRegTS()) && !n.value.IsNull() {
			*r.catalogAliasReferences = true
		}
		if r.checkLiterals != nil {
			*r.checkLiterals = append(*r.checkLiterals, n)
			return fmt.Sprintf("$%d", len(*r.checkLiterals)), nil
		}
		if r.checkReplayPrecision && u.Type.ID() == sqltype.Float4 && !n.value.IsNull() {
			value, err := n.value.Float()
			if err != nil {
				return "", err
			}
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				return "'" + strconv.FormatFloat(value, 'g', -1, 64) + "'", nil
			}
		}
		if r.uuidShiftIntervalNulls && u.Type.ID() == sqltype.Interval && n.value.IsNull() {
			declaration, err := u.Declaration()
			if err != nil {
				return "", err
			}
			return "CAST(NULL AS " + declaration + ")", nil
		}
		aclOutput := r.aclOutput
		if r.validation {
			aclOutput = aclItemValidationOutput{owner: view.Owner()}
		} else if aclOutput == nil {
			aclOutput = aclItemContextFormatter(ctx)
		}
		return catalogLiteralWithACLAt(ctx, view, r.definition, n, aclOutput)
	case collationExpression:
		text, err := r.sql(ctx, view, n.child)
		if err != nil {
			return "", err
		}
		if name := collationSQLName(view, n.Usage.Collation.ID); name != "" {
			return "(" + text + " COLLATE " + name + ")", nil
		}
		return "", fmt.Errorf("stuntgres: invalid default collation")
	case coercionExpression:
		if n.child == nil {
			return "", fmt.Errorf("stuntgres: missing default coercion child")
		}
		switch n.mode {
		case sqltype.Implicit, sqltype.Assignment, sqltype.Explicit:
		default:
			return "", fmt.Errorf("stuntgres: invalid default coercion mode")
		}
		plan, err := n.plan.Rebind(n.child.ResultUsage(), u, n.mode)
		if err != nil || !plan.Equal(n.plan) {
			return "", fmt.Errorf("stuntgres: invalid default coercion")
		}
		if r.commentSnapshot != nil && (plan.RequiresCatalogOutput() || (u.Type.ContainsRegrole() || u.Type.ContainsRegObject()) && plan.SessionDependent()) {
			*r.commentSnapshot = true
		}
		if constructor, ok := n.child.(functions.SQLJSON); ok && n.plan.IsJSONReturning() {
			return r.sqlJSON(ctx, view, constructor, u)
		}
		child, err := r.sql(ctx, view, n.child)
		if err != nil || n.mode != sqltype.Explicit && !sqlJSONResult(n.child) {
			return child, err
		}
		// Identifier literals already render with their scalar authority. A
		// transparent relabel must not grow another CAST on each Restore.
		if r.checkLiterals == nil && sqltype.IsTransactionIdentifier(u.Type.ID()) && n.child.ResultUsage() == u {
			return child, nil
		}
		// Bare quoted TEXT becomes unknown on replay and can move a deferred
		// conversion into CREATE-time input decoding. Keep that child typed.
		if n.child.ResultUsage().Type.ID() == sqltype.Text {
			inner, explicit := n.child.(coercionExpression)
			if !explicit || inner.mode != sqltype.Explicit {
				declaration, err := catalogTypeDeclarationAt(view, r.definition, n.child.ResultUsage())
				if err != nil {
					return "", err
				}
				child = "CAST(" + child + " AS " + declaration + ")"
			}
		}
		declaration, err := catalogTypeDeclarationAt(view, r.definition, u)
		if err != nil {
			return "", err
		}
		return "CAST(" + child + " AS " + declaration + ")", nil
	case bitwiseExpression:
		if u != u.Type.Usage() || n.left == nil || n.left.ResultUsage() != u {
			return "", fmt.Errorf("stuntgres: invalid default bitwise operands")
		}
		var rightType *sqltype.Descriptor
		if n.right != nil {
			rightType = n.right.ResultUsage().Type
			if rightType == nil || n.right.ResultUsage() != rightType.Usage() {
				return "", fmt.Errorf("stuntgres: invalid default bitwise right usage")
			}
		}
		if u.Type.ID() == sqltype.Inet {
			provider, err := sqltype.BindNetworkBitwise(n.operator, u.Type, rightType)
			if err != nil || provider != n.network || n.provider != (sqltype.Bitwise{}) || n.bits != (sqltype.BitStringBitwise{}) {
				return "", fmt.Errorf("stuntgres: invalid default bitwise binding")
			}
		} else if u.Type.ID() == sqltype.Bit {
			provider, err := sqltype.BindBitStringBitwise(n.operator, u.Type, rightType)
			if err != nil || provider != n.bits || n.provider != (sqltype.Bitwise{}) || n.network != (sqltype.NetworkBitwise{}) {
				return "", fmt.Errorf("stuntgres: invalid default bitwise binding")
			}
		} else {
			provider, err := sqltype.BindBitwise(n.operator, u.Type, rightType)
			if err != nil || provider != n.provider || n.bits != (sqltype.BitStringBitwise{}) || n.network != (sqltype.NetworkBitwise{}) {
				return "", fmt.Errorf("stuntgres: invalid default bitwise binding")
			}
		}
		operator := [...]string{"&", "|", "#", "~", "<<", ">>"}[n.operator]
		if n.namespace != "" {
			operator = "OPERATOR(" + catalog.QuoteIdentifier(n.namespace) + "." + operator + ")"
		}
		operandSQL := func(child expr.Expr) (string, error) {
			text, err := r.sql(ctx, view, child)
			if err != nil {
				return "", err
			}
			cu := child.ResultUsage()
			if literal, ok := child.(literalExpression); ok && cu.Type.ID() != sqltype.Bit && (cu.Type.ID() != sqltype.Int4 || literal.value.IsNull()) {
				declaration, err := catalogTypeDeclaration(view, cu)
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			return text, nil
		}
		left, err := operandSQL(n.left)
		if err != nil {
			return "", err
		}
		if n.right == nil {
			return "(" + operator + " " + left + ")", nil
		}
		right, err := operandSQL(n.right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case selectedOperatorExpression:
		r.record(n.ref)
		d, ok := view.Operator(n.ref.Address)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing default operator")
		}
		namespace := view.Get(d.Namespace()).Name()
		op := "OPERATOR(" + catalog.QuoteIdentifier(namespace) + "." + d.Name() + ")"
		var operands []expr.Expr
		boundExpressionChildren(n.provider, func(arg expr.Expr) {
			if arg != nil {
				operands = append(operands, arg)
			}
		})
		text := make([]string, len(operands))
		for i, arg := range operands {
			var err error
			text[i], err = r.sql(ctx, view, arg)
			if err != nil {
				return "", err
			}
		}
		if d.Left == 0 && len(text) == 1 {
			return "(" + op + " " + text[0] + ")", nil
		}
		if d.Left != 0 && len(text) == 2 {
			return "(" + text[0] + " " + op + " " + text[1] + ")", nil
		}
		return "", fmt.Errorf("stuntgres: invalid default operator operands")
	case arithmeticExpression:
		if u != u.Type.Usage() || n.left == nil {
			return "", fmt.Errorf("stuntgres: invalid default arithmetic operands")
		}
		var rightType *sqltype.Descriptor
		if n.right != nil {
			rightType = n.right.ResultUsage().Type
		}
		provider, err := sqltype.BindArithmetic(n.operator, n.left.ResultUsage().Type, rightType, u.Type)
		if err != nil || provider != n.provider || provider.ResultType() != u.Type {
			return "", fmt.Errorf("stuntgres: invalid default arithmetic binding")
		}
		var operator string
		switch n.operator {
		case sqltype.ArithmeticAdd:
			operator = "+"
		case sqltype.ArithmeticSubtract:
			operator = "-"
		case sqltype.ArithmeticMultiply:
			operator = "*"
		case sqltype.ArithmeticDivide:
			operator = "/"
		case sqltype.ArithmeticRemainder:
			operator = "%"
		case sqltype.ArithmeticPower:
			operator = "^"
		default:
			return "", fmt.Errorf("stuntgres: invalid default arithmetic operator")
		}
		if n.namespace != "" {
			operator = "OPERATOR(" + catalog.QuoteIdentifier(n.namespace) + "." + operator + ")"
		}
		operandSQL := func(child expr.Expr) (string, error) {
			text, err := r.sql(ctx, view, child)
			if err != nil {
				return "", err
			}
			// Preserve each leaf's own width, not its parent's result width.
			cu := child.ResultUsage()
			if literal, ok := child.(literalExpression); ok && (cu.Type.ID() != sqltype.Int4 || literal.value.IsNull()) {
				declaration, err := catalogTypeDeclaration(view, cu)
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			return text, nil
		}
		left, err := operandSQL(n.left)
		if err != nil {
			return "", err
		}
		if n.right == nil {
			return "(" + operator + " " + left + ")", nil
		}
		right, err := operandSQL(n.right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case likeExpression:
		left, err := r.sql(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		right, err := r.sql(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		operator := n.operator
		if n.namespace != "" {
			operator = "OPERATOR(" + pgIdentifier(n.namespace) + "." + operator + ")"
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case rowComparisonExpression:
		for _, pair := range n.comparisons {
			if _, err := r.sql(ctx, view, pair); err != nil {
				return "", err
			}
		}
		return renderRowComparison(n, func(child expr.Expr) (string, error) { return r.sql(ctx, view, child) }, n.operator)
	case scalarArrayComparisonExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || (n.array == nil) == (len(n.items) == 0) {
			return "", fmt.Errorf("stuntgres: invalid scalar-array predicate")
		}
		operator, ok := comparisonOperator(n.operator)
		if !ok {
			return "", fmt.Errorf("stuntgres: invalid scalar-array operator")
		}
		ref, conflict := n.predicate.InputCollation()
		predicate, err := sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.elementType(), u.Type, ref, conflict)
		if err != nil || !predicate.Equal(n.predicate) {
			return "", fmt.Errorf("stuntgres: invalid scalar-array predicate binding")
		}
		return renderScalarArrayComparison(n, func(child expr.Expr) (string, error) { return r.sql(ctx, view, child) }, catalog.QuoteIdentifier)
	case comparisonExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.right == nil {
			return "", fmt.Errorf("stuntgres: invalid default predicate")
		}
		var operator sqltype.ComparisonOperator
		switch n.operator {
		case "=":
			operator = sqltype.CompareEqual
		case "<>":
			operator = sqltype.CompareNotEqual
		case "<":
			operator = sqltype.CompareLess
		case "<=":
			operator = sqltype.CompareLessEqual
		case ">":
			operator = sqltype.CompareGreater
		case ">=":
			operator = sqltype.CompareGreaterEqual
		default:
			return "", fmt.Errorf("stuntgres: invalid default comparison")
		}
		ref, conflict := n.predicate.InputCollation()
		predicate, err := sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.right.ResultUsage().Type, u.Type, ref, conflict)
		if err != nil || !predicate.Equal(n.predicate) {
			return "", fmt.Errorf("stuntgres: invalid default predicate binding")
		}
		left, err := r.sql(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		right, err := r.sql(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		spelling := n.operator
		if n.namespace != "" {
			spelling = "OPERATOR(" + catalog.QuoteIdentifier(n.namespace) + "." + spelling + ")"
		}
		return "(" + left + " " + spelling + " " + right + ")", nil
	case nullTestExpression:
		if u.Type.ID() != sqltype.Bool || n.child == nil {
			return "", fmt.Errorf("stuntgres: invalid default null test")
		}
		child, err := r.sql(ctx, view, n.child)
		if err != nil {
			return "", err
		}
		operator := "IS NULL"
		if n.not {
			operator = "IS NOT NULL"
		}
		if n.whole && n.child.ResultUsage().Type.BaseType().IsComposite() {
			operator = "IS NOT DISTINCT FROM NULL"
			if n.not {
				operator = "IS DISTINCT FROM NULL"
			}
		}
		return "(" + child + " " + operator + ")", nil
	case truthTestExpression:
		if u.Type.ID() != sqltype.Bool || n.child == nil || n.child.ResultUsage().Type != u.Type {
			return "", fmt.Errorf("stuntgres: invalid default truth test")
		}
		var truth string
		switch n.truth {
		case True:
			truth = "TRUE"
		case False:
			truth = "FALSE"
		case Unknown:
			truth = "UNKNOWN"
		default:
			return "", fmt.Errorf("stuntgres: invalid default truth value")
		}
		child, err := r.sql(ctx, view, n.child)
		if err != nil {
			return "", err
		}
		operator := "IS"
		if n.not {
			operator = "IS NOT"
		}
		return "(" + child + " " + operator + " " + truth + ")", nil
	case distinctExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.right == nil {
			return "", fmt.Errorf("stuntgres: invalid default distinct test")
		}
		ref, conflict := n.equality.InputCollation()
		predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.left.ResultUsage().Type, n.right.ResultUsage().Type, u.Type, ref, conflict)
		if err != nil || !predicate.Equal(n.equality) {
			return "", fmt.Errorf("stuntgres: invalid default distinct binding")
		}
		left, err := r.sql(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		right, err := r.sql(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		operator := "IS DISTINCT FROM"
		if n.not {
			operator = "IS NOT DISTINCT FROM"
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case logicalExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.left.ResultUsage().Type.ID() != sqltype.Bool || n.op == parser.LogicalNot && n.right != nil || n.op != parser.LogicalNot && (n.right == nil || n.right.ResultUsage().Type.ID() != sqltype.Bool) {
			return "", fmt.Errorf("stuntgres: invalid default logical binding")
		}
		left, err := r.sql(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		if n.op == parser.LogicalNot {
			return "(NOT " + left + ")", nil
		}
		right, err := r.sql(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		operator := "AND"
		if n.op == parser.LogicalOr {
			operator = "OR"
		} else if n.op != parser.LogicalAnd {
			return "", fmt.Errorf("stuntgres: invalid default logical operator")
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case expr.Renderable:
		// Function nodes describe their stored form; engine wrappers that embed
		// one keep their explicit cases above.
		return r.scalarFunctionSQL(ctx, view, node)
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}

func (r scalarRenderer) functionSQL(ctx context.Context, view catalog.Read, name string, args []expr.Expr) (string, error) {
	parts := make([]string, len(args))
	for i, arg := range args {
		text, err := r.sql(ctx, view, arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

func (r scalarRenderer) record(ref ids.ObjectRef) {
	if r.dependencies == nil {
		return
	}
	for _, old := range *r.dependencies {
		if old == ref {
			return
		}
	}
	*r.dependencies = append(*r.dependencies, ref)
}
