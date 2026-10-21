package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Inherited columns and cooked definitions carry admitted catalog identities.
// Re-parsing their qualified names would add schema/cast/function privileges
// PostgreSQL does not demand and could resolve a different provider by name.
func (e *Engine) schemaPartitionChildLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, syntax *parser.CreateTable, namespace ids.ObjectAddress) (*catalog.Relation, error) {
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
	for _, column := range parent.Columns {
		typ := column.Usage.Type
		if typ.IsUserDefined() || typ.IsArray() && typ.Element().IsDomain() {
			ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
			if err := auth.CanUse(ref); err != nil {
				return nil, err
			}
			stage.domainBases = append(stage.domainBases, auth.Catalog.Get(ref.Address))
		}
	}
	d, err := table.NewRelation(ddlHost{e: e}, namespace, name, auth.State.CurrentUser, slices.Clone(parent.Columns), nil)
	if err != nil {
		return nil, err
	}
	d.Security.ACL = stage.creationACL(d.Owner, namespace, authorization.DefaultTables)
	for i := range d.Columns {
		d.Columns[i].DeclaredDimensions = 0
	}
	if err = e.stageRowTypeLocked(stage, d); err != nil {
		return nil, err
	}
	declarations := make([]analyze.ColumnDeclaration, len(d.Columns))
	var seenColumns []bool
	for _, element := range syntax.Elements {
		if element.Column == nil {
			continue
		}
		slot, ok := d.Index[element.Column.Name]
		if !ok {
			return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, element.Column.Name), element.Column.Pos)
		}
		if seenColumns == nil {
			seenColumns = make([]bool, len(d.Columns))
		}
		if seenColumns[slot] {
			return nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, element.Column.Name))
		}
		seenColumns[slot] = true
		declaration, err := analyze.BindCreateColumnDeclaration(element.Column, name)
		if err != nil {
			return nil, err
		}
		declarations[slot] = declaration
	}
	uniques, err := analyze.BindCreateUniques(syntax, d.Index, nil, auth.Catalog)
	if err != nil {
		return nil, err
	}
	nullSyntax := *syntax
	nullSyntax.Elements = make([]parser.TableElement, 0, len(parent.Constraints.NotNull)+len(syntax.Elements))
	for _, constraint := range parent.Constraints.NotNull {
		slot := parent.IndexForAttribute(constraint.Attribute)
		nullSyntax.Elements = append(nullSyntax.Elements, parser.TableElement{NotNull: &parser.NotNullConstraint{Column: parser.Identifier{Text: d.Columns[slot].Name}, Name: parser.Identifier{Text: constraint.Name}}})
	}
	nullSyntax.Elements = append(nullSyntax.Elements, syntax.Elements...)
	d.Constraints.NotNull, err = analyze.BindCreateNotNull(&nullSyntax, d.Columns, declarations, nil, uniques, d.Index)
	if err != nil {
		return nil, err
	}
	if err = table.NameCreateNotNull(d, auth.Catalog); err != nil {
		return nil, err
	}
	for i := range d.Constraints.NotNull {
		constraint := &d.Constraints.NotNull[i]
		slot := d.IndexForAttribute(constraint.Attribute)
		constraint.PartitionLocal = declarations[slot].ColumnNotNull
		for _, element := range syntax.Elements {
			if element.NotNull != nil && element.NotNull.Column.Text == d.Columns[slot].Name {
				constraint.PartitionLocal = true
			}
		}
	}
	if err = e.stageCreateIdentities(auth, stage, d, syntax); err != nil {
		return nil, err
	}
	auth.Catalog = e.stageRead(stage)
	auth.Recapture(auth.Catalog)
	binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
	binder.Query.Auth = &auth
	localDefaults, err := bindCreateDefaults(binder, d.Columns, declarations)
	if err != nil {
		return nil, err
	}
	if len(parent.ColumnMeta.Defaults) != 0 || len(localDefaults) != 0 {
		d.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(parent.ColumnMeta.Defaults)+len(localDefaults))
		for i, attribute := range parent.Attributes {
			if declarations[i].DefaultExpr != nil || declarations[i].Identity != nil || declarations[i].Generation != nil {
				continue
			}
			if definition, ok := parent.ColumnMeta.Defaults[attribute]; ok {
				definition.OID = 0
				d.ColumnMeta.Defaults[d.Attributes[i]] = definition
			}
		}
		for attribute, definition := range localDefaults {
			d.ColumnMeta.Defaults[attribute] = definition
		}
	}
	if err = e.bindCreateGenerations(op, auth, d, declarations, scope.input); err != nil {
		return nil, err
	}
	inherited := make(map[string]catalog.CheckConstraint, len(parent.Constraints.Check))
	if len(parent.Constraints.Check) != 0 {
		slots := make(map[int32]int, len(d.Attributes))
		for i, attribute := range d.Attributes {
			slots[attribute] = i
		}
		rowRef := func(old ids.ObjectRef) ids.ObjectRef {
			if old.Address.ClassID == parent.Address().ClassID && old.Address.ObjectID == parent.Address().ObjectID {
				attribute := old.Address.SubID
				old.Address = d.Address()
				if attribute != 0 {
					old.Address.SubID = d.Attributes[parent.IndexForAttribute(attribute)]
				}
				old = e.catalog.Domain.Ref(old.Epoch, old.Address)
				old.Epoch = auth.Catalog.Epoch()
			}
			return old
		}
		for _, original := range parent.Constraints.Check {
			if original.NoInherit {
				continue
			}
			definition := original
			definition.OID = 0
			definition.Expression, err = stored.RemapExpressionWithQuery(original.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots, nil, rowRef)
			if err != nil {
				return nil, err
			}
			var refs []ids.ObjectRef
			_, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: d, Dependencies: &refs, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(op.Context(), auth.Catalog, definition.Expression)
			if err != nil {
				return nil, err
			}
			definition.Dependencies = make([]catalog.ExpressionDependency, 0, len(refs))
			for _, ref := range refs {
				address := ref.Address
				address.SubID = 0
				revision := d.Revision()
				if object, ok := auth.Catalog.Descriptor(address); ok {
					revision = object.Revision()
				}
				definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: revision})
			}
			d.Constraints.Check = append(d.Constraints.Check, definition)
			inherited[definition.Name] = definition
		}
	}
	if err = e.bindCreateChecks(op, auth, syntax, d, scope.input, inherited); err != nil {
		return nil, err
	}
	if err = table.AssignDefinitionOIDs(ddlHost{e: e}, d); err != nil {
		return nil, err
	}
	h := ddlHost{e: e, op: op, stage: stage}
	for _, declaration := range uniques {
		if declaration.Source != nil {
			stage.captureLikeSource(declaration.Source)
		}
	}
	if err = index.StageCreateUniques(h, d, uniques); err != nil {
		return nil, err
	}
	if err = index.StageCreateExclusions(h, auth, scope.input, d, syntax); err != nil {
		return nil, err
	}
	return d, nil
}
