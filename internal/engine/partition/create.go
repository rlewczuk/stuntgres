package partition

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TupleSQL(ctx context.Context, tuple []catalog.PartitionDatum) (string, error) {
	var text strings.Builder
	for i, datum := range tuple {
		if i > 0 {
			text.WriteString(", ")
		}
		switch datum.Kind {
		case -1:
			text.WriteString("MINVALUE")
		case 1:
			text.WriteString("MAXVALUE")
		default:
			value, err := datum.Value.Type().SQLLiteral(ctx, datum.Value)
			if err != nil {
				return "", err
			}
			text.WriteString(value)
		}
	}
	return text.String(), nil
}

// Boundary is a statement-local projection of the one immutable child
// topology. Tuples and descriptors are referenced, not copied or persisted.
type Boundary struct {
	Tuple    []catalog.PartitionDatum
	Relation *catalog.Relation
	Lower    bool
}

// RejectForeignKeys rejects FOREIGN KEY declarations in a CREATE TABLE with
// a PARTITION clause; it is the statement's first diagnostic.
func RejectForeignKeys(s *parser.CreateTable) error {
	for _, element := range s.Elements {
		foreign := element.ForeignKey != nil
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				foreign = foreign || constraint.ForeignKey != nil
			}
		}
		if foreign {
			return sqlError("0A000", "foreign keys involving partition hierarchies are unsupported")
		}
	}
	return nil
}

// StageCreate stages CREATE TABLE s with a PARTITION clause in namespace,
// owned by owner. parent is the resolved, owner-checked PARTITION OF
// parent, nil for a plain PARTITION BY table. It stages the child (or the
// ordinary table), binds the partition keys and prepares the bound, and
// returns the pending definition; the caller fences the default leaves'
// storage, captures their data and finishes the definition once the CREATE
// window closes. foreignTargets are the FOREIGN KEY parents analysis
// resolved. It runs with Engine.mu held and never waits.
func StageCreate(h Host, auth ddl.Authority, s *parser.CreateTable, namespace ids.ObjectAddress, parent *catalog.Relation, owner authorization.PrincipalRef, input sqltype.InputEnvironment, foreignTargets []ids.ObjectRef) (*Definition, error) {
	if parent != nil && parent.Partition == nil {
		return nil, sqlError("42P17", fmt.Sprintf(`"%s" is not partitioned`, parent.Name()))
	}
	if parent != nil {
		if _, err := stageChild(h, auth, parent, s, namespace, owner, input); err != nil {
			return nil, err
		}
	} else if err := table.StageCreateTable(h, auth, s, namespace, owner, input, foreignTargets, catalog.PartitionedRelation); err != nil {
		return nil, err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	address, _ := h.NameBinding(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	child := h.Stage().Get(address).(*catalog.Relation)
	part, err := BindKeys(h, h.Recapture(auth, h.StageRead()), child, s.PartitionBy, input)
	if err != nil {
		return nil, err
	}
	if part != nil {
		for _, constraint := range child.Constraints.Check {
			if constraint.NoInherit {
				return nil, sqlError("42P16", fmt.Sprintf(`cannot add NO INHERIT constraint to partitioned table "%s"`, child.Name()))
			}
		}
		child.Partition = part
		child.RelationKind = catalog.PartitionedRelation
	}
	d := &Definition{Parent: parent, Child: child}
	if parent != nil {
		view := h.AccessView(auth)
		d.Raw = s.PartitionOf.Bound
		d.Bound, err = PrepareBounds(parent, d.Raw)
		if err != nil {
			return nil, err
		}
		child.Membership = d.Bound
		h.AddDomainBase(parent)
		if err = CheckDefaultConflict(view.Catalog, parent, d.Bound, name, s.PartitionOf.Bound.Pos); err != nil {
			return nil, err
		}
		if !d.Bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
			defaultRelation := view.Catalog.MustRelation(parent.Partition.DefaultChild)
			err = PhysicalDescendants(view.Catalog, defaultRelation, func(leaf *catalog.Relation) error {
				d.DefaultLeaves = append(d.DefaultLeaves, view.Ref(leaf.Address()))
				h.AddDomainBase(leaf)
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	// Invalid keys/bounds and inherited definitions win over this declared
	// implementation boundary; neither path publishes allocated auxiliaries.
	d.Unsupported = len(child.ColumnMeta.Generations) != 0 || len(child.ColumnMeta.Identities) != 0
	for _, element := range s.Elements {
		if element.Column != nil && sequence.SerialTypeName(element.Column.TypeName) != "" {
			d.Unsupported = true
		}
	}
	return d, nil
}

// stageChild stages the PARTITION OF child of parent. Inherited columns and
// cooked definitions carry admitted catalog identities. Re-parsing their
// qualified names would add schema/cast/function privileges PostgreSQL does
// not demand and could resolve a different provider by name.
func stageChild(h Host, auth ddl.Authority, parent *catalog.Relation, syntax *parser.CreateTable, namespace ids.ObjectAddress, owner authorization.PrincipalRef, input sqltype.InputEnvironment) (*catalog.Relation, error) {
	name := syntax.Name.Parts[len(syntax.Name.Parts)-1].Text
	available, err := h.DisplaceAssociatedArray(namespace, name)
	if err != nil {
		return nil, err
	}
	if !available {
		d := sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name)).(*sqlstate.Error)
		d.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
		return nil, d
	}
	view := h.AccessView(auth)
	for _, column := range parent.Columns {
		typ := column.Usage.Type
		if typ.IsUserDefined() || typ.IsArray() && typ.Element().IsDomain() {
			ref := view.Ref(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
			if err := view.CanUse(auth, ref); err != nil {
				return nil, err
			}
			h.AddDomainBase(view.Catalog.Get(ref.Address))
		}
	}
	d, err := table.NewRelation(h, namespace, name, owner, slices.Clone(parent.Columns), nil)
	if err != nil {
		return nil, err
	}
	d.Persistence = syntax.Persistence
	d.TableMethod = parent.TableMethod
	if d.TableMethod == 0 && syntax.PartitionBy == nil {
		d.TableMethod, err = table.ResolveTableMethod(view.Catalog, h.DefaultTableAccessMethod())
		if err != nil { return nil, err }
	}
	d.Security.ACL = h.CreationACL(d.Owner, namespace, authorization.DefaultTables)
	for i := range d.Columns {
		d.Columns[i].DeclaredDimensions = 0
	}
	if err = h.StageRowType(d); err != nil {
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
	uniques, err := analyze.BindCreateUniques(syntax, d.Index, nil, view.Catalog)
	if err != nil {
		return nil, err
	}
	nullSyntax := *syntax
	nullSyntax.Elements = make([]parser.TableElement, 0, len(parent.Constraints.NotNull)+len(syntax.Elements))
	localNotNull := make(map[string]bool)
	for slot, declaration := range declarations {
		if declaration.ColumnNotNull {
			localNotNull[d.Columns[slot].Name] = true
		}
	}
	for _, element := range syntax.Elements {
		if element.NotNull != nil {
			localNotNull[element.NotNull.Column.Text] = true
		}
	}
	for _, constraint := range parent.Constraints.NotNull {
		slot := parent.IndexForAttribute(constraint.Attribute)
		if localNotNull[d.Columns[slot].Name] {
			continue // CREATE's local declaration owns the name, even when unnamed.
		}
		nullSyntax.Elements = append(nullSyntax.Elements, parser.TableElement{NotNull: &parser.NotNullConstraint{Column: parser.Identifier{Text: d.Columns[slot].Name}, Name: parser.Identifier{Text: constraint.Name}}})
	}
	nullSyntax.Elements = append(nullSyntax.Elements, syntax.Elements...)
	d.Constraints.NotNull, err = analyze.BindCreateNotNull(&nullSyntax, d.Columns, declarations, nil, uniques, d.Index)
	if err != nil {
		return nil, err
	}
	if err = table.NameCreateNotNull(d, view.Catalog); err != nil {
		return nil, err
	}
	for i := range d.Constraints.NotNull {
		constraint := &d.Constraints.NotNull[i]
		slot := d.IndexForAttribute(constraint.Attribute)
		for _, source := range parent.Constraints.NotNull {
			if parent.IndexForAttribute(source.Attribute) == slot {
				constraint.PartitionOrigin = &catalog.InheritedConstraint{Local: localNotNull[d.Columns[slot].Name], Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: source.Attribute, Constraint: source.OID}}}
				break
			}
		}
	}
	if err = table.StageCreateIdentities(h, auth, d, syntax); err != nil {
		return nil, err
	}
	auth = h.Recapture(auth, h.StageRead())
	view = h.AccessView(auth)
	localDefaults, err := h.BindColumnDefaults(auth, d.Columns, declarations, input, ids.SessionToken{})
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
	if err = h.BindGenerations(auth, d, declarations, input); err != nil {
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
				address := d.Address()
				if attribute != 0 {
					address.SubID = d.Attributes[parent.IndexForAttribute(attribute)]
				}
				old = view.Ref(address)
			}
			return old
		}
		for _, original := range parent.Constraints.Check {
			if original.NoInherit {
				continue
			}
			definition := original
			definition.OID = 0
			definition.PartitionOrigin = &catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Constraint: original.OID}}}
			definition.Expression, err = stored.RemapExpressionWithQuery(original.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots, nil, rowRef)
			if err != nil {
				return nil, err
			}
			var refs []ids.ObjectRef
			_, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: d, Dependencies: &refs, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(h.Context(), view.Catalog, definition.Expression)
			if err != nil {
				return nil, err
			}
			definition.Dependencies = make([]catalog.ExpressionDependency, 0, len(refs))
			for _, ref := range refs {
				address := ref.Address
				address.SubID = 0
				revision := d.Revision()
				if object, ok := view.Catalog.Descriptor(address); ok {
					revision = object.Revision()
				}
				definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: revision})
			}
			d.Constraints.Check = append(d.Constraints.Check, definition)
			inherited[definition.Name] = definition
		}
	}
	if err = table.BindChecks(h, auth, d, table.CreateCheckSyntax(syntax), input, false, inherited); err != nil {
		return nil, err
	}
	if err = table.AssignDefinitionOIDs(h, d); err != nil {
		return nil, err
	}
	for _, declaration := range uniques {
		if declaration.Source != nil {
			h.TableStage().CaptureLikeSource(declaration.Source)
		}
	}
	if err = index.StageCreateUniques(h, d, uniques); err != nil {
		return nil, index.DefaultClassDiagnostic(h, auth, err)
	}
	if err = index.StageCreateExclusions(h, auth, input, d, syntax); err != nil {
		return nil, err
	}
	return d, nil
}

// BindKeys binds the PARTITION BY keys of relation d; nil when raw is nil.
// It runs with Engine.mu held and never waits.
func BindKeys(h Host, auth ddl.Authority, d *catalog.Relation, raw *parser.PartitionKey, input sqltype.InputEnvironment) (*catalog.PartitionDefinition, error) {
	if raw == nil {
		return nil, nil
	}
	if raw.Strategy == parser.PartitionList && len(raw.Keys) != 1 {
		return nil, sqlError("42P17", `cannot use "list" partition strategy with more than one column`)
	}
	if len(raw.Keys) == 0 || len(raw.Keys) > 32 {
		return nil, sqlError("54011", "cannot partition using more than 32 columns")
	}
	definition := &catalog.PartitionDefinition{Strategy: raw.Strategy, Keys: make([]catalog.PartitionKeyEntry, len(raw.Keys))}
	for i, r := range raw.Keys {
		key := catalog.PartitionKeyEntry{}
		var usage sqltype.Usage
		if r.Expression == nil {
			slot, ok := d.Index[r.Column.Text]
			if !ok {
				if id, _ := sqltype.SystemAttributeType(r.Column.Text); id != 0 {
					return nil, expr.ErrorAt("42P17", fmt.Sprintf(`cannot use system column "%s" in partition key`, r.Column.Text), r.Pos)
				}
				return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" named in partition key does not exist`, r.Column.Text), r.Pos)
			}
			key.Attribute = d.Attributes[slot]
			usage = d.Columns[slot].Usage
			key.Label = r.Column.Text
			if _, generated := d.ColumnMeta.Generations[key.Attribute]; generated {
				diagnostic := sqlError("42P17", "cannot use generated column in partition key")
				return nil, GeneratedError(diagnostic, r.Pos, key.Label)
			}
		} else {
			derived, uses, err := h.BindKeyExpression(auth, d, r.Expression, input)
			if err != nil {
				return nil, err
			}
			if exec.PlannedVolatility(derived.Execution) != expr.Immutable {
				return nil, sqlError("42P17", "functions in partition key expression must be marked IMMUTABLE")
			}
			if !derived.Execution.Properties().RowDependent {
				return nil, sqlError("42P17", "cannot use constant expression as partition key")
			}
			for _, slot := range uses {
				if _, generated := d.ColumnMeta.Generations[d.Attributes[slot]]; generated {
					return nil, GeneratedError(sqlError("42P17", "cannot use generated column in partition key"), r.Pos, d.Columns[slot].Name)
				}
			}
			key.Derived = derived
			view := h.AccessView(auth)
			text, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: d, Dependencies: &key.Derived.Dependencies}).SQL(h.Context(), view.Catalog, derived.Definition)
			if err != nil {
				return nil, err
			}
			key.Label = text
			usage = derived.Execution.ResultUsage()
		}
		method := catalog.BtreeMethod
		if raw.Strategy == parser.PartitionHash {
			method = catalog.HashMethod
		}
		bound, err := index.BindKey(h, auth, usage, r, method.OID())
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" && r.Opclass == nil && raw.Strategy == parser.PartitionHash {
				copy := *diagnostic
				copy.Hint = "You must specify a hash operator class or define a default hash operator class for the data type."
				return nil, &copy
			}
			return nil, err
		}
		key.Usage = index.KeyCollation(usage, bound)
		key.Class, key.Collation, key.ExplicitClass = bound.Class, bound.Collation, bound.ExplicitClass
		if raw.Strategy == parser.PartitionHash {
			key.Hash, err = sqltype.BindPartitionHash(key.Usage, key.Class)
		} else {
			key.Comparison, err = sqltype.BindIndexOrdering(key.Usage, key.Class)
		}
		if err != nil {
			return nil, err
		}
		definition.Keys[i] = key
	}
	return definition, nil
}
