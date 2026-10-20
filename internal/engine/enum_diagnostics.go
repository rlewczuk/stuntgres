package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) diagnosticTypeName(typ *sqltype.Descriptor) string {
	if typ == nil {
		return "unknown"
	}
	if typ.IsArray() {
		return b.diagnosticTypeName(typ.Element()) + "[]"
	}
	if !typ.IsUserDefined() {
		return typ.DiagnosticName()
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return typ.DiagnosticName()
	}
	definition, _ := auth.definitionDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	descriptor, ok := definition.(*catalog.Type)
	if !ok {
		return typ.DiagnosticName()
	}
	path, err := b.engine.referencePath(auth)
	if err != nil {
		return typ.DiagnosticName()
	}
	return catalog.TypeDiagnosticNameAt(auth.Catalog, descriptor, path)
}

// captureTypeDiagnosticName freezes input and demanded-comparison naming against
// the catalog and reference path used for binding. Providers consult it on failure.
func (e *Engine) captureTypeDiagnosticName(auth AuthorizationView) (func(sqltype.ID) string, error) {
	path, err := e.referencePath(auth)
	if err != nil {
		return nil, err
	}
	catalogView := auth.Catalog
	return func(id sqltype.ID) string {
		descriptor, ok := catalogView.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		if !ok {
			return ""
		}
		if descriptor.SQLType.NamespaceName() == "pg_catalog" {
			// Released diagnostics use SQL aliases, not catalog spellings such
			// as int4, and do not identifier-quote builtin names such as json.
			return descriptor.SQLType.DiagnosticName()
		}
		return catalog.TypeDiagnosticNameAt(catalogView, descriptor, path)
	}, nil
}

// Safety belongs to admission, not discovery of an enum node. Scope inputs and
// standalone binders borrow the same immutable root; candidate mutations clone
// it. Inputs are never retained across admissions, so an existing callback is
// already the current scope's capture and can be shared without another closure.
func captureEnumOperationSafety(op *Operation, input *sqltype.InputEnvironment) {
	if input.EnumMemberSafe != nil || op == nil || op.session == nil || op.session.transactions == nil || op.session.transactions.catalog == nil {
		return
	}
	input.EnumMemberSafe = enumSafety(op.session.transactions.catalog.enum.unsafeMembers())
}

// Capture naming only after the caller supplies its coherent catalog authority.
// Alternate staged binders retain lazy naming through captureTypeDiagnosticInput.
func (e *Engine) captureEnumOperationInput(auth AuthorizationView, input *sqltype.InputEnvironment) error {
	if input.EnumMemberSafe == nil || input.TypeDiagnosticName != nil {
		return nil
	}
	var err error
	input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
	return err
}

func (b *expressionBinder) captureTypeDiagnosticInput(typ *sqltype.Descriptor) error {
	if sqltype.IsFTS(typ.BaseType().ID()) && b.input.FTS == nil {
		b.input.FTS = capturedFTSInput{op: b.op}
	}
	acl := typ.ContainsACLItem() && b.input.ACLItem == nil
	naming := b.input.TypeDiagnosticName == nil && needsTypeDiagnosticInput(typ)
	if !acl && !naming {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	return b.engine.captureTypeDiagnosticInput(auth, b.op, &b.input, typ)
}

func (e *Engine) captureTypeDiagnosticInput(auth AuthorizationView, op *Operation, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error {
	if sqltype.IsFTS(typ.BaseType().ID()) && input.FTS == nil {
		input.FTS = capturedFTSInput{op: op}
	}
	if typ.ContainsACLItem() && input.ACLItem == nil {
		input.ACLItem = e.captureACLItemInput(auth, op)
	}
	if input.TypeDiagnosticName == nil && needsTypeDiagnosticInput(typ) {
		var err error
		input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
		return err
	}
	return nil
}

// Comparisons need names, not input codecs or role-name resolution. Capture the
// existing naming authority once without acquiring unrelated ACL input state.
func (b *expressionBinder) captureComparisonDiagnosticInput(typ *sqltype.Descriptor) error {
	if b.input.TypeDiagnosticName != nil || !needsTypeDiagnosticInput(typ) {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	b.input.TypeDiagnosticName, err = b.engine.captureTypeDiagnosticName(auth)
	return err
}

// Input provenance and diagnostic naming are separate demands. Only enum
// inputs and checked domains retain the existing replacement fence here.
func hasEnumInput(typ *sqltype.Descriptor) bool {
	if typ == nil {
		return false
	}
	if typ.HasDomainConstraints() {
		return true
	}
	typ = typ.BaseType()
	if typ.IsEnum() {
		return true
	}
	if typ.IsArray() {
		return hasEnumInput(typ.Element())
	}
	if typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if hasEnumInput(typ.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}

func needsTypeDiagnosticInput(typ *sqltype.Descriptor) bool {
	if typ == nil {
		return false
	}
	if typ.IsUserDefined() || typ.HasDomainConstraints() {
		return true
	}
	typ = typ.BaseType()
	if typ.IsEnum() {
		return true
	}
	if typ.IsArray() {
		return needsTypeDiagnosticInput(typ.Element())
	}
	if typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if needsTypeDiagnosticInput(typ.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}

// Retained reads and writes have identical input demands, independent of the
// tree walker used by their consumer. Naming never suppresses catalog input.
func captureRetainedExpressionInput(node expr.Expr, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	// Stored columns and record constructors can demand a named nested provider
	// without any input conversion. Retain the same naming authority as fresh
	// comparison binding, including domain and array identity.
	if needsTypeDiagnosticInput(node.ResultUsage().Type) {
		if err := diagnostic(node.ResultUsage().Type); err != nil {
			return err
		}
	}
	switch node := node.(type) {
	case functions.Enum:
		return diagnostic(node.Plan.Enum())
	case functions.JSONBRecord:
		targets := [2]*sqltype.Descriptor{node.RecordUsage().Type}
		if len(node.Args) == 2 {
			targets[1] = node.Args[0].ResultUsage().Type
		}
		for i, target := range targets {
			if target == nil || i == 1 && target == targets[0] {
				continue
			}
			if target.ContainsCatalogIdentifier() {
				if err := element(); err != nil {
					return err
				}
			}
			if err := diagnostic(target); err != nil {
				return err
			}
		}
	case coercionExpression:
		target := node.ResultUsage().Type
		characterInput := sqltype.IsCharacter(node.child.ResultUsage().Type.BaseType().ID())
		if characterInput || sqltype.ContainsDomainConstraints(target) {
			if characterInput && target.ContainsCatalogIdentifier() {
				if err := element(); err != nil {
					return err
				}
			}
			return diagnostic(target)
		}
	}
	return nil
}

// Stored sources are opaque to the outer expression binder, but their
// planning-time conversions consume the same published input environment.
func (b *expressionBinder) captureRetainedViewInput(definition *viewDefinition) error {
	return captureRetainedDefinitionInput(b.query.auth.Catalog, definition, b.captureElementInput, b.captureTypeDiagnosticInput)
}

// Each frozen consumer supplies its own coherent catalog and input owners.
func captureRetainedDefinitionInput(view catalog.Read, definition *viewDefinition, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	seen := make(map[*viewDefinition]bool)
	var capture func(*viewDefinition) error
	capture = func(definition *viewDefinition) error {
		if seen[definition] {
			return nil
		}
		seen[definition] = true
		var failure error
		walkQueryArgumentNode(definition.query.Root, func(node expr.Expr) {
			if failure == nil {
				failure = captureRetainedExpressionInput(node, element, diagnostic)
			}
		})
		if failure != nil {
			return failure
		}
		for _, source := range definition.sources {
			relation, ok := view.Relation(source.ref.Address)
			if ok && relation.RelationKind == catalog.ViewRelation {
				if err := capture(viewOf(relation)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return capture(definition)
}

// Retained write expressions are not rebound with the consuming statement.
// Capture input authority and lazy naming without evaluating defaults or branches.
func (b *expressionBinder) captureRetainedWriteInput(defaults []expr.Expr, generations map[int32]catalog.ColumnGeneration, checks []catalog.CheckConstraint) error {
	var failure error
	visit := func(node expr.Expr) {
		if failure != nil {
			return
		}
		failure = captureRetainedExpressionInput(node, b.captureElementInput, b.captureTypeDiagnosticInput)
	}
	for _, node := range defaults {
		walkBoundExpression(node, visit)
		if failure != nil {
			return failure
		}
	}
	for _, definition := range generations {
		walkBoundExpression(definition.Planned, visit)
		if failure != nil {
			return failure
		}
	}
	for _, definition := range checks {
		walkBoundExpression(definition.Expression, visit)
		if failure != nil {
			return failure
		}
	}
	return nil
}
