package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Inherited columns and cooked definitions carry admitted catalog identities.
// Re-parsing their qualified names would add schema/cast/function privileges
// PostgreSQL does not demand and could resolve a different provider by name.
func (e *Engine) schemaPartitionChildLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, syntax *parser.CreateTable, namespace ObjectAddress) (*relationDescriptor, error) {
	op := scope.op
	name := syntax.Name.Parts[len(syntax.Name.Parts)-1].Text
	available, err := e.displaceAssociatedArrayLocked(stage, namespace, name)
	if err != nil {
		return nil, err
	}
	if !available {
		d := sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name)).(*sqlstate.Error)
		d.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
		return nil, d
	}
	for _, column := range parent.columns {
		typ := column.Usage.Type
		if typ.IsUserDefined() || typ.IsArray() && typ.Element().IsDomain() {
			ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())}}
			if err := auth.CanUse(ref); err != nil {
				return nil, err
			}
			stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[ref.Address])
		}
	}
	d, err := e.newTableDescriptorLocked(namespace, name, auth.state.CurrentUser, slices.Clone(parent.columns), nil)
	if err != nil {
		return nil, err
	}
	if err = e.stageRowTypeLocked(stage, d); err != nil {
		return nil, err
	}
	declarations := make([]createColumnDeclaration, len(d.columns))
	var seenColumns []bool
	for _, element := range syntax.Elements {
		if element.Column == nil {
			continue
		}
		slot, ok := d.index[element.Column.Name]
		if !ok {
			return nil, expressionError("42703", fmt.Sprintf(`column "%s" does not exist`, element.Column.Name), element.Column.Pos)
		}
		if seenColumns == nil {
			seenColumns = make([]bool, len(d.columns))
		}
		if seenColumns[slot] {
			return nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, element.Column.Name))
		}
		seenColumns[slot] = true
		declaration, err := bindCreateColumnDeclaration(element.Column, name)
		if err != nil {
			return nil, err
		}
		declarations[slot] = declaration
	}
	uniques, err := bindCreateUniques(syntax, d.index, nil, auth.catalog)
	if err != nil {
		return nil, err
	}
	nullSyntax := *syntax
	nullSyntax.Elements = make([]parser.TableElement, 0, len(parent.notNullConstraints)+len(syntax.Elements))
	for _, constraint := range parent.notNullConstraints {
		slot := parent.indexForAttribute(constraint.Attribute)
		nullSyntax.Elements = append(nullSyntax.Elements, parser.TableElement{NotNull: &parser.NotNullConstraint{Column: parser.Identifier{Text: d.columns[slot].Name}, Name: parser.Identifier{Text: constraint.Name}}})
	}
	nullSyntax.Elements = append(nullSyntax.Elements, syntax.Elements...)
	d.notNullConstraints, err = bindCreateNotNull(&nullSyntax, d.columns, declarations, nil, uniques, d.index)
	if err != nil {
		return nil, err
	}
	if err = nameCreateNotNull(d, auth.catalog); err != nil {
		return nil, err
	}
	if err = e.stageCreateIdentities(auth, stage, d, syntax); err != nil {
		return nil, err
	}
	auth.catalog = e.stageRead(stage)
	binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
	binder.queryAuth = &auth
	localDefaults, err := bindCreateDefaults(binder, d.columns, declarations)
	if err != nil {
		return nil, err
	}
	if len(parent.defaults) != 0 || len(localDefaults) != 0 {
		d.defaults = make(map[int32]columnDefault, len(parent.defaults)+len(localDefaults))
		for i, attribute := range parent.attributes {
			if declarations[i].defaultExpr != nil || declarations[i].identity != nil || declarations[i].generation != nil {
				continue
			}
			if definition, ok := parent.defaults[attribute]; ok {
				definition.OID = 0
				d.defaults[d.attributes[i]] = definition
			}
		}
		for attribute, definition := range localDefaults {
			d.defaults[attribute] = definition
		}
	}
	if err = e.bindCreateGenerations(op, auth, d, declarations, scope.input); err != nil {
		return nil, err
	}
	inherited := make(map[string]checkConstraint, len(parent.checkConstraints))
	if len(parent.checkConstraints) != 0 {
		slots := make(map[int32]int, len(d.attributes))
		for i, attribute := range d.attributes {
			slots[attribute] = i
		}
		rowRef := func(old ObjectRef) ObjectRef {
			if old.Address.ClassID == parent.address.ClassID && old.Address.ObjectID == parent.address.ObjectID {
				attribute := old.Address.SubID
				old.Address = d.address
				if attribute != 0 {
					old.Address.SubID = d.attributes[parent.indexForAttribute(attribute)]
				}
				old.owner = e.owner
				old.Epoch = auth.catalog.Epoch()
			}
			return old
		}
		for _, original := range parent.checkConstraints {
			if original.NoInherit {
				continue
			}
			definition := original
			definition.OID = 0
			definition.expression, err = remapBoundExpressionWithQuery(original.expression, func(ref ObjectRef) ObjectRef { return ref }, slots, nil, rowRef)
			if err != nil {
				return nil, err
			}
			var refs []ObjectRef
			_, err = (scalarRenderer{owner: d, dependencies: &refs, commentSnapshot: &definition.commentSnapshot, visibilitySnapshot: &definition.visibilitySnapshot}).sql(op.Context(), auth.catalog, definition.expression)
			if err != nil {
				return nil, err
			}
			definition.dependencies = make([]expressionDependency, 0, len(refs))
			for _, ref := range refs {
				address := ref.Address
				address.SubID = 0
				revision := d.revision
				if object, ok := auth.catalog.Descriptor(address); ok {
					revision = object.Revision()
				}
				definition.dependencies = append(definition.dependencies, expressionDependency{ref: ref, revision: revision})
			}
			d.checkConstraints = append(d.checkConstraints, definition)
			inherited[definition.Name] = definition
		}
	}
	if err = e.bindCreateChecks(op, auth, syntax, d, scope.input, inherited); err != nil {
		return nil, err
	}
	if err = e.assignDefinitionOIDsLocked(d); err != nil {
		return nil, err
	}
	if err = e.stageCreateUniques(stage, d, uniques); err != nil {
		return nil, err
	}
	if err = e.stageCreateExclusions(op, scope.input, auth, stage, d, syntax); err != nil {
		return nil, err
	}
	return d, nil
}
