package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// All links are stable catalog addresses/attributes, never descriptor pointers.
type foreignKeyConstraint struct {
	OID              OID
	Name             string
	Attributes       []int32
	Target           ObjectAddress
	Referenced       []int32
	Index            ObjectAddress
	Full             bool
	UpdateAction     parser.ForeignKeyAction
	DeleteAction     parser.ForeignKeyAction
	DeleteAttributes []int32
}

type foreignKeyComparison struct {
	child, parent int
	comparison    sqltype.IndexForeignKeyComparison
}

func foreignKeyDeclarations(s *parser.CreateTable) []parser.ForeignKeyConstraint {
	var declarations []parser.ForeignKeyConstraint
	for _, element := range s.Elements {
		if element.ForeignKey != nil {
			declarations = append(declarations, *element.ForeignKey)
		}
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if constraint.ForeignKey != nil {
					f := *constraint.ForeignKey
					f.Columns = []parser.Identifier{{Text: element.Column.Name, Pos: element.Column.Pos}}
					declarations = append(declarations, f)
				}
			}
		}
	}
	return declarations
}

func foreignKeyAttributes(d *relationDescriptor, columns []parser.Identifier) ([]int32, error) {
	attributes := make([]int32, len(columns))
	for i, column := range columns {
		slot, ok := d.index[column.Text]
		if !ok {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" referenced in foreign key constraint does not exist`, column.Text))
		}
		attributes[i] = d.attributes[slot]
	}
	return attributes, nil
}

func foreignKeyIndex(view CatalogRead, parent *relationDescriptor, attributes []int32) (*relationDescriptor, error) {
	var found *relationDescriptor
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		eligible, err := foreignKeyIndexEligible(parent, index, attributes)
		if err != nil {
			return nil, err
		}
		if eligible && (found == nil || index.address.ObjectID < found.address.ObjectID) {
			found = index
		}
	}
	if found == nil {
		return nil, sqlError("42830", fmt.Sprintf(`there is no unique constraint matching given keys for referenced table "%s"`, parent.name))
	}
	return found, nil
}

func foreignKeyIndexEligible(parent, index *relationDescriptor, attributes []int32) (bool, error) {
	d := index.sqlIndex
	if d == nil || d.parent != parent.address || !d.unique || !d.ready || !d.valid || !d.live || d.predicate != nil || len(d.keys) != len(attributes) {
		return false, nil
	}
	for _, attribute := range attributes {
		matches := 0
		for _, key := range d.keys {
			a, node, err := indexCatalogKey(parent, key)
			if err != nil {
				return false, err
			}
			if node != nil {
				return false, nil
			}
			if a == attribute {
				matches++
			}
		}
		if matches != 1 {
			return false, nil
		}
	}
	return true, nil
}

func bindForeignKeyComparison(view CatalogRead, child, parent, index *relationDescriptor, f foreignKeyConstraint) ([]foreignKeyComparison, error) {
	comparisons := make([]foreignKeyComparison, len(f.Attributes))
	for i, attribute := range f.Attributes {
		c, p := slices.Index(child.attributes, attribute), slices.Index(parent.attributes, f.Referenced[i])
		if c < 0 || p < 0 {
			return nil, fmt.Errorf("stuntgres: missing foreign-key attribute")
		}
		cu, pu := child.columns[c].Usage, parent.columns[p].Usage
		var err error
		bound := foreignKeyComparison{child: c, parent: p}
		matchedAt := -1
		for at, key := range index.sqlIndex.keys {
			a, node, keyErr := indexCatalogKey(parent, key)
			if keyErr != nil {
				return nil, keyErr
			}
			if node == nil && a == f.Referenced[i] {
				pu = indexKeyCollation(pu, key)
				bound.comparison, err = sqltype.BindIndexForeignKey(pu, cu, key.class, view.LookupType)
				matchedAt = at
				break
			}
		}
		if matchedAt < 0 {
			return nil, fmt.Errorf("stuntgres: missing foreign-key comparison provider")
		}
		if err != nil {
			d := sqlError("42804", fmt.Sprintf(`foreign key constraint "%s" cannot be implemented`, f.Name)).(*sqlstate.Error)
			d.Detail = fmt.Sprintf(`Key columns "%s" of the referencing table and "%s" of the referenced table are of incompatible types: %s and %s.`, child.columns[c].Name, parent.columns[p].Name, cu.Type.DiagnosticName(), pu.Type.DiagnosticName())
			return nil, d
		}
		comparisons[matchedAt] = bound
	}
	return comparisons, nil
}

func foreignKeyReferencesAllowed(auth AuthorizationView, parent *relationDescriptor, attributes []int32) error {
	ref := ObjectRef{owner: auth.engine.owner, Epoch: auth.catalog.Epoch(), Address: parent.address}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return err
	}
	if rights&ReferencesPrivilege != 0 {
		return nil
	}
	for _, attribute := range attributes {
		column := ref
		column.Address.SubID = attribute
		rights, err = auth.Effective(column, false)
		if err != nil {
			return err
		}
		if rights&ReferencesPrivilege == 0 {
			return sqlError("42501", "permission denied for table "+parent.name)
		}
	}
	return nil
}

func (e *Engine) stageForeignKey(auth AuthorizationView, stage *CatalogStage, child *relationDescriptor, raw parser.ForeignKeyConstraint, target ObjectAddress) error {
	view := e.stageRead(stage)
	auth.catalog = view
	ref := ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: target}
	var err error
	if target == (ObjectAddress{}) {
		ref, err = e.resolveReference(auth, raw.Target)
		if err != nil {
			return err
		}
	}
	parent, ok := view.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing foreign-key parent")
	}
	if hierarchyRelation(child) || hierarchyRelation(parent) {
		return sqlError("0A000", "foreign keys involving partition hierarchies are unsupported")
	}
	if parent.relationKind != tableRelation {
		return sqlError("42809", fmt.Sprintf(`referenced relation "%s" is not a table`, parent.name))
	}
	childTemp := view.state.descriptors[child.namespace].(*namespaceDescriptor).temporary
	parentTemp := view.state.descriptors[parent.namespace].(*namespaceDescriptor).temporary
	if childTemp != (SessionToken{}) && parentTemp == (SessionToken{}) {
		return sqlError("42P16", "constraints on temporary tables may reference only temporary tables")
	}
	if childTemp == (SessionToken{}) && parentTemp != (SessionToken{}) {
		return sqlError("42P16", "constraints on permanent tables may reference only permanent tables")
	}
	if childTemp != parentTemp {
		return ErrCatalogReplaced
	}
	local, err := foreignKeyAttributes(child, raw.Columns)
	if err != nil {
		return err
	}
	deleteAttributes, err := foreignKeyAttributes(child, raw.DeleteColumns)
	if err != nil {
		return err
	}
	normalized := deleteAttributes[:0]
	for i, a := range deleteAttributes {
		if !slices.Contains(local, a) {
			return sqlError("42P10", fmt.Sprintf(`column "%s" referenced in ON DELETE SET action must be part of foreign key`, raw.DeleteColumns[i].Text))
		}
		if !slices.Contains(normalized, a) {
			normalized = append(normalized, a)
		}
	}
	deleteAttributes = normalized
	for _, a := range local {
		if _, generated := child.generations[a]; !generated {
			continue
		}
		if raw.UpdateAction == parser.ForeignKeyCascade || raw.UpdateAction == parser.ForeignKeySetNull || raw.UpdateAction == parser.ForeignKeySetDefault {
			return sqlError("42601", "invalid ON UPDATE action for foreign key constraint containing generated column")
		}
		if raw.DeleteAction == parser.ForeignKeySetNull || raw.DeleteAction == parser.ForeignKeySetDefault {
			return sqlError("42601", "invalid ON DELETE action for foreign key constraint containing generated column")
		}
	}
	var referenced []int32
	if len(raw.Referenced) == 0 {
		for _, key := range parent.uniqueConstraints {
			if key.Primary {
				referenced = slices.Clone(key.Attributes)
				break
			}
		}
		if referenced == nil {
			return sqlError("42704", fmt.Sprintf(`there is no primary key for referenced table "%s"`, parent.name))
		}
	} else {
		referenced, err = foreignKeyAttributes(parent, raw.Referenced)
		if err != nil {
			return err
		}
	}
	if len(local) != len(referenced) {
		return sqlError("42830", "number of referencing and referenced columns for foreign key disagree")
	}
	for i, a := range referenced {
		if slices.Contains(referenced[:i], a) {
			return sqlError("42830", "foreign key referenced-columns list must not contain duplicates")
		}
	}
	index, err := foreignKeyIndex(view, parent, referenced)
	if err != nil {
		return err
	}
	if err := foreignKeyReferencesAllowed(auth, parent, referenced); err != nil {
		return err
	}
	used := constraintNames(view, child.namespace, child)
	name := raw.Name.Text
	if name == "" {
		labels := make([]string, len(raw.Columns))
		for i, column := range raw.Columns {
			labels[i] = column.Text
		}
		name = generatedConstraintName(child.name, strings.Join(labels, "_"), "fkey", used)
	}
	if relationConstraintNameExists(child, name) {
		return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, child.name))
	}
	f := foreignKeyConstraint{Name: name, Attributes: local, Target: parent.address, Referenced: referenced, Index: index.address, Full: raw.Full, UpdateAction: raw.UpdateAction, DeleteAction: raw.DeleteAction, DeleteAttributes: deleteAttributes}
	if _, err := bindForeignKeyComparison(view, child, parent, index, f); err != nil {
		return err
	}
	f.OID, err = e.allocateOIDLocked()
	if err != nil {
		return err
	}
	child.foreignKeys = append(child.foreignKeys, f)
	stage.descriptors[child.address] = child
	return nil
}

func (e *Engine) stageCreateForeignKeys(auth AuthorizationView, stage *CatalogStage, child *relationDescriptor, s *parser.CreateTable, targets []ObjectRef) error {
	declarations := foreignKeyDeclarations(s)
	if len(targets) != 0 && len(targets) != len(declarations) {
		return fmt.Errorf("stuntgres: invalid foreign-key creation authority")
	}
	for i, raw := range declarations {
		var target ObjectAddress
		if len(targets) != 0 {
			auth.catalog = e.stageRead(stage)
			current, err := e.resolveReference(auth, raw.Target)
			if err != nil {
				return err
			}
			captured := targets[i]
			if captured == (ObjectRef{}) {
				if current.Address != child.address {
					return ErrCatalogReplaced
				}
			} else if current != captured {
				return ErrCatalogReplaced
			}
			target = current.Address
		}
		if err := e.stageForeignKey(auth, stage, child, raw, target); err != nil {
			return err
		}
	}
	return nil
}

func validateForeignKeys(view CatalogRead, child *relationDescriptor, names map[string]bool) error {
	for _, f := range child.foreignKeys {
		if child.relationKind != tableRelation || f.OID == 0 || f.Name == "" || len(f.Name) > 63 || !utf8.ValidString(f.Name) || strings.IndexByte(f.Name, 0) >= 0 || names[f.Name] || len(f.Attributes) == 0 || len(f.Attributes) != len(f.Referenced) {
			return fmt.Errorf("stuntgres: invalid foreign-key definition")
		}
		if f.UpdateAction > parser.ForeignKeySetDefault || f.DeleteAction > parser.ForeignKeySetDefault || len(f.DeleteAttributes) != 0 && f.DeleteAction != parser.ForeignKeySetNull && f.DeleteAction != parser.ForeignKeySetDefault {
			return fmt.Errorf("stuntgres: invalid foreign-key action")
		}
		for i, a := range f.DeleteAttributes {
			if !slices.Contains(child.attributes, a) || !slices.Contains(f.Attributes, a) || slices.Contains(f.DeleteAttributes[:i], a) {
				return fmt.Errorf("stuntgres: invalid foreign-key action attribute")
			}
		}
		names[f.Name] = true
		parent, ok := view.state.descriptors[f.Target].(*relationDescriptor)
		if !ok || parent.relationKind != tableRelation {
			return fmt.Errorf("stuntgres: missing foreign-key target")
		}
		index, ok := view.state.descriptors[f.Index].(*relationDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing foreign-key index")
		}
		eligible, err := foreignKeyIndexEligible(parent, index, f.Referenced)
		if err != nil {
			return err
		}
		if !eligible {
			return fmt.Errorf("stuntgres: foreign-key index authority mismatch")
		}
		if view.state.descriptors[parent.namespace].(*namespaceDescriptor).temporary != view.state.descriptors[child.namespace].(*namespaceDescriptor).temporary {
			return fmt.Errorf("stuntgres: foreign-key temporary provenance mismatch")
		}
		if _, err := bindForeignKeyComparison(view, child, parent, index, f); err != nil {
			return err
		}
	}
	return nil
}

func foreignKeyRowsMatch(ctx context.Context, comparisons []foreignKeyComparison, child, parent []sqltype.Value, input sqltype.InputEnvironment) (bool, error) {
	for _, comparison := range comparisons {
		left, right := child[comparison.child], parent[comparison.parent]
		if left.IsNull() || right.IsNull() {
			return false, nil
		}
		cmp, err := comparison.comparison.Compare(ctx, right, left, input)
		if err != nil || cmp != 0 {
			return false, err
		}
	}
	return true, nil
}

func foreignKeyNullState(comparisons []foreignKeyComparison, row []sqltype.Value) (any, all bool) {
	all = true
	for _, c := range comparisons {
		n := row[c.child].IsNull()
		any = any || n
		all = all && n
	}
	return
}

func foreignKeyDiagnostic(ctx context.Context, auth AuthorizationView, child, parent *relationDescriptor, f foreignKeyConstraint, values []sqltype.Value, parentSide, partial bool, output sqltype.OutputEnvironment) error {
	d := sqlError("23503", fmt.Sprintf(`insert or update on table "%s" violates foreign key constraint "%s"`, child.name, f.Name)).(*sqlstate.Error)
	d.Schema = auth.catalog.state.descriptors[child.namespace].Name()
	d.Table, d.Constraint = child.name, f.Name
	if partial {
		d.Detail = "MATCH FULL does not allow mixing of null and nonnull key values."
		return d
	}
	violating, attributes, suffix := child, f.Attributes, fmt.Sprintf(` is not present in table "%s".`, parent.name)
	if parentSide {
		d.Message = fmt.Sprintf(`update or delete on table "%s" violates foreign key constraint "%s" on table "%s"`, parent.name, f.Name, child.name)
		violating, attributes, suffix = parent, f.Referenced, fmt.Sprintf(` is still referenced from table "%s".`, child.name)
	}
	disposition, err := rowSecurityDecision(auth, violating, auth.state.CurrentUser, policySelect, false)
	if err != nil {
		return err
	}
	if disposition != rlsUnrestricted {
		d.Detail = "Key" + suffix
		return d
	}
	keys := make([]indexKey, len(attributes))
	for i, a := range attributes {
		keys[i].attribute = a
	}
	allowed, err := uniqueKeyDetailAllowed(auth, ObjectRef{owner: auth.engine.owner, Epoch: auth.catalog.Epoch(), Address: violating.address}, keys)
	if err != nil {
		return err
	}
	if !allowed {
		d.Detail = "Key" + suffix
		return d
	}
	labels, text := make([]string, len(attributes)), make([]string, len(attributes))
	for i, a := range attributes {
		slot := slices.Index(violating.attributes, a)
		labels[i] = violating.columns[slot].Name
		text[i], err = values[slot].Type().OutputWithEnvironment(ctx, values[slot], output)
		if err != nil {
			return err
		}
	}
	d.Detail = "Key (" + strings.Join(labels, ", ") + ")=(" + strings.Join(text, ", ") + ")" + suffix
	return d
}
