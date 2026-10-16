package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type visibilityInquiryKind uint8

const (
	tableVisibility visibilityInquiryKind = iota
	typeVisibility
	collationVisibility
	conversionVisibility
	collationVersionInquiry
	databaseCollationVersionInquiry
	importSystemCollationsInquiry
)

func (k visibilityInquiryKind) name() string {
	switch k {
	case tableVisibility:
		return "pg_table_is_visible"
	case typeVisibility:
		return "pg_type_is_visible"
	case collationVisibility:
		return "pg_collation_is_visible"
	case conversionVisibility:
		return "pg_conversion_is_visible"
	case collationVersionInquiry:
		return "pg_collation_actual_version"
	case databaseCollationVersionInquiry:
		return "pg_database_collation_actual_version"
	case importSystemCollationsInquiry:
		return "pg_import_system_collations"
	}
	return ""
}
func (k visibilityInquiryKind) class() OID {
	switch k {
	case tableVisibility:
		return RelationClassID
	case typeVisibility:
		return TypeClassID
	case collationVisibility, collationVersionInquiry:
		return CollationClassID
	case conversionVisibility:
		return ConversionClassID
	case databaseCollationVersionInquiry:
		return DatabaseClassID
	case importSystemCollationsInquiry:
		return NamespaceClassID
	}
	return 0
}

func (k visibilityInquiryKind) formal() sqltype.ID {
	if k == importSystemCollationsInquiry {
		return sqltype.Regnamespace
	}
	return sqltype.Oid
}

func (k visibilityInquiryKind) result() sqltype.ID {
	switch k {
	case collationVersionInquiry, databaseCollationVersionInquiry:
		return sqltype.Text
	case importSystemCollationsInquiry:
		return sqltype.Int4
	}
	return sqltype.Bool
}

func (k visibilityInquiryKind) volatility() Volatility {
	if k == collationVersionInquiry || k == databaseCollationVersionInquiry || k == importSystemCollationsInquiry {
		return Volatile
	}
	return Stable
}

type visibilityInquiryExpression struct {
	expressionInfo
	kind visibilityInquiryKind
	arg  BoundExpr
}

func (b *expressionBinder) bindVisibilityInquiry(raw parser.FunctionExpr) (BoundExpr, error) {
	n, err := b.bindVisibilityInquiryStructure(raw)
	if err != nil {
		return nil, err
	}
	if literal, ok := n.arg.(literalExpression); ok && literal.value.IsNull() {
		typ := n.usage.Type
		return literalExpression{b.info(typ.Usage(), raw.Pos, ExprProperties{}), sqltype.Null(typ)}, nil
	}
	b.visibilitySnapshot = true
	return n, nil
}
func (b *expressionBinder) bindVisibilityInquiryStructure(raw parser.FunctionExpr) (visibilityInquiryExpression, error) {
	if b.defaultConsumer {
		return visibilityInquiryExpression{}, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	// Transform children before either the argument guard or namespace lookup.
	// Unknown literals/parameters retain the existing operand inference seam;
	// only the resolved signature supplies their OID input contract.
	var operand comparisonOperand
	var singleName [1]string
	names := singleName[:]
	if len(raw.Args) != 1 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.comparisonOperand(child)
		if err != nil {
			return visibilityInquiryExpression{}, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			names[i] = "unknown"
			if typ := arg.descriptor(); typ != nil {
				names[i] = typ.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return visibilityInquiryExpression{}, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return visibilityInquiryExpression{}, err
	}
	if !visible || len(raw.Args) != 1 {
		return visibilityInquiryExpression{}, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	var kind visibilityInquiryKind
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "pg_table_is_visible":
		kind = tableVisibility
	case "pg_type_is_visible":
		kind = typeVisibility
	case "pg_collation_is_visible":
		kind = collationVisibility
	case "pg_conversion_is_visible":
		kind = conversionVisibility
	case "pg_collation_actual_version":
		kind = collationVersionInquiry
	case "pg_database_collation_actual_version":
		kind = databaseCollationVersionInquiry
	case "pg_import_system_collations":
		kind = importSystemCollationsInquiry
	default:
		return visibilityInquiryExpression{}, fmt.Errorf("stuntgres: invalid visibility inquiry")
	}
	target := b.typ(kind.formal()).Usage()
	arg, err := b.finalizeOperand(operand, target.Type)
	if err != nil {
		return visibilityInquiryExpression{}, err
	}
	if arg.ResultUsage() != target {
		plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
				return visibilityInquiryExpression{}, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
			return visibilityInquiryExpression{}, err
		}
		arg = coercionExpression{b.info(target, raw.Args[0].SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit, false}
	}
	properties := arg.Properties()
	properties.Volatility = max(kind.volatility(), properties.Volatility)
	result := b.typ(kind.result()).Usage()
	info, err := b.builtinRoutineInfo(b.info(result, raw.Pos, properties), catalogRoutineProvider, kind.name(), kind.formal())
	if err != nil {
		return visibilityInquiryExpression{}, err
	}
	return visibilityInquiryExpression{info, kind, arg}, nil
}
func namedObjectVisible(view CatalogRead, path []ObjectAddress, d Descriptor) bool {
	return namedObjectVisibleAt(view, nil, path, d)
}

func namedObjectVisibleAt(view CatalogRead, reader definitionReader, path []ObjectAddress, d Descriptor) bool {
	for _, namespace := range path {
		key := descriptorNameKey(d)
		key.Namespace = namespace
		if found := definitionLookupAt(view, reader, key); found != (ObjectAddress{}) {
			return found == d.Address()
		}
	}
	return false
}
func (e visibilityInquiryExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.kind.class() == 0 || e.arg == nil || e.usage.Type == nil || e.usage.Type.ID() != e.kind.result() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid visibility inquiry")
	}
	value, err := e.arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.usage.Type), nil
	}
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statement authority")
	}
	var oid uint32
	if e.kind == importSystemCollationsInquiry {
		oid, err = value.ObjectOID()
	} else {
		oid, err = value.OID()
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.kind == importSystemCollationsInquiry {
		engine := scope.op.session.Engine()
		engine.mu.Lock()
		defer engine.mu.Unlock()
		auth, err := scope.namespaceAuthorizationLocked(engine)
		if err != nil {
			return sqltype.Value{}, err
		}
		actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !actor.Superuser {
			return sqltype.Value{}, sqlError("42501", "must be superuser to import system collations")
		}
		if _, exists := auth.catalog.Descriptor(ObjectAddress{ClassID: NamespaceClassID, ObjectID: OID(oid)}); !exists {
			return sqltype.Value{}, sqlError("3F000", fmt.Sprintf("schema with OID %d does not exist", oid))
		}
		return sqltype.Value{}, sqlError("0A000", "system collation import is unsupported in embedded mode")
	}
	if scope.visibilityCatalog.state == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing visibility snapshot")
	}
	d, exists := scope.visibilityCatalog.Descriptor(ObjectAddress{ClassID: e.kind.class(), ObjectID: OID(oid)})
	if !exists {
		if e.kind == collationVersionInquiry || e.kind == databaseCollationVersionInquiry {
			kind := "collation"
			if e.kind == databaseCollationVersionInquiry {
				kind = "database"
			}
			return sqltype.Value{}, sqlError("42704", fmt.Sprintf("%s with OID %d does not exist", kind, oid))
		}
		return sqltype.Null(e.usage.Type), nil
	}
	if e.kind == collationVersionInquiry || e.kind == databaseCollationVersionInquiry {
		var version *string
		if e.kind == databaseCollationVersionInquiry {
			version, err = databaseCollationActualVersion()
		} else {
			version, err = collationActualVersion(d.(*collationDescriptor))
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		if version == nil {
			return sqltype.Null(e.usage.Type), nil
		}
		return sqltype.String(e.usage.Type, *version), nil
	}
	path := scope.visibilityRelationPath
	if e.kind == collationVisibility || e.kind == conversionVisibility {
		path = scope.visibilityOtherPath
	}
	visible := false
	if e.kind == collationVisibility {
		visible = visibleCollation(scope.visibilityCatalog, path, d)
	} else {
		visible = namedObjectVisible(scope.visibilityCatalog, path, d)
	}
	return sqltype.Boolean(e.usage.Type, visible), nil
}

// Engine.mu is held. The execution frontier owns catalog and path, never mutable
// session settings or an authorization view subsequently consulted by Eval.
func (e *Engine) captureVisibilitySnapshotLocked(scope *StatementScope) error {
	if scope.visibilityCatalog.state != nil {
		return nil
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return err
	}
	explicit, _, err := e.explicitPath(auth)
	if err != nil {
		return err
	}
	// effectivePath filters TEMP in place for conversion lookup. Give that
	// derived path its own backing store; the relation path remains immutable.
	relations := e.effectivePath(auth, explicit, relationLookup)
	otherExplicit := make([]ObjectAddress, len(explicit))
	copy(otherExplicit, explicit)
	others := e.effectivePath(auth, otherExplicit, conversionLookup)
	scope.visibilityCatalog = auth.catalog
	scope.visibilityRelationPath, scope.visibilityOtherPath = relations, others
	return nil
}
