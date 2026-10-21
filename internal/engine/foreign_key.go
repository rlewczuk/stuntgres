package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

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

func foreignKeyAttributes(d *catalog.Relation, columns []parser.Identifier) ([]int32, error) {
	attributes := make([]int32, len(columns))
	for i, column := range columns {
		slot, ok := d.Index[column.Text]
		if !ok {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" referenced in foreign key constraint does not exist`, column.Text))
		}
		attributes[i] = d.Attributes[slot]
	}
	return attributes, nil
}

func foreignKeyIndex(view catalog.Read, parent *catalog.Relation, attributes []int32) (*catalog.Relation, error) {
	var found *catalog.Relation
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		eligible, err := foreignKeyIndexEligible(parent, index, attributes)
		if err != nil {
			return nil, err
		}
		if eligible && (found == nil || index.Address().ObjectID < found.Address().ObjectID) {
			found = index
		}
	}
	if found == nil {
		return nil, sqlError("42830", fmt.Sprintf(`there is no unique constraint matching given keys for referenced table "%s"`, parent.Name()))
	}
	return found, nil
}

func foreignKeyIndexEligible(parent, idx *catalog.Relation, attributes []int32) (bool, error) {
	d := idx.SQLIndex
	if d == nil || d.Parent != parent.Address() || !d.Unique || !d.Ready || !d.Valid || !d.Live || d.Predicate != nil || len(d.Keys) != len(attributes) {
		return false, nil
	}
	for _, attribute := range attributes {
		matches := 0
		for _, key := range d.Keys {
			a, node, err := index.CatalogKey(parent, key)
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

func bindForeignKeyComparison(view catalog.Read, child, parent, idx *catalog.Relation, f catalog.ForeignKeyConstraint) ([]foreignKeyComparison, error) {
	comparisons := make([]foreignKeyComparison, len(f.Attributes))
	for i, attribute := range f.Attributes {
		c, p := slices.Index(child.Attributes, attribute), slices.Index(parent.Attributes, f.Referenced[i])
		if c < 0 || p < 0 {
			return nil, fmt.Errorf("stuntgres: missing foreign-key attribute")
		}
		cu, pu := child.Columns[c].Usage, parent.Columns[p].Usage
		var err error
		bound := foreignKeyComparison{child: c, parent: p}
		matchedAt := -1
		for at, key := range idx.SQLIndex.Keys {
			a, node, keyErr := index.CatalogKey(parent, key)
			if keyErr != nil {
				return nil, keyErr
			}
			if node == nil && a == f.Referenced[i] {
				pu = index.KeyCollation(pu, key)
				bound.comparison, err = sqltype.BindIndexForeignKey(pu, cu, key.Class, view.LookupType)
				matchedAt = at
				break
			}
		}
		if matchedAt < 0 {
			return nil, fmt.Errorf("stuntgres: missing foreign-key comparison provider")
		}
		if err != nil {
			d := sqlError("42804", fmt.Sprintf(`foreign key constraint "%s" cannot be implemented`, f.Name)).(*sqlstate.Error)
			d.Detail = fmt.Sprintf(`Key columns "%s" of the referencing table and "%s" of the referenced table are of incompatible types: %s and %s.`, child.Columns[c].Name, parent.Columns[p].Name, cu.Type.DiagnosticName(), pu.Type.DiagnosticName())
			return nil, d
		}
		comparisons[matchedAt] = bound
	}
	return comparisons, nil
}

func foreignKeyReferencesAllowed(view access.View, env access.Environment, parent *catalog.Relation, attributes []int32) error {
	ref := view.Ref(parent.Address())
	rights, err := view.Effective(env, ref, false)
	if err != nil {
		return err
	}
	if rights&authorization.ReferencesPrivilege != 0 {
		return nil
	}
	for _, attribute := range attributes {
		column := ref
		column.Address.SubID = attribute
		rights, err = view.Effective(env, column, false)
		if err != nil {
			return err
		}
		if rights&authorization.ReferencesPrivilege == 0 {
			return sqlError("42501", "permission denied for table "+parent.Name())
		}
	}
	return nil
}

func (e *Engine) stageForeignKey(auth AuthorizationView, stage *CatalogStage, child *catalog.Relation, raw parser.ForeignKeyConstraint, target, supporting ids.ObjectAddress) error {
	view := e.stageRead(stage)
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	ref := e.catalog.Domain.Ref(view.Epoch(), target)
	var err error
	if target == (ids.ObjectAddress{}) {
		ref, err = e.resolveReference(auth, raw.Target)
		if err != nil {
			return err
		}
	}
	parent, ok := view.Relation(ref.Address)
	if !ok {
		return fmt.Errorf("stuntgres: missing foreign-key parent")
	}
	if catalog.HierarchyRelation(child) || catalog.HierarchyRelation(parent) {
		return sqlError("0A000", "foreign keys involving partition hierarchies are unsupported")
	}
	if parent.RelationKind != catalog.TableRelation {
		return sqlError("42809", fmt.Sprintf(`referenced relation "%s" is not a table`, parent.Name()))
	}
	childTemp := view.MustNamespace(child.Namespace()).Temporary
	parentTemp := view.MustNamespace(parent.Namespace()).Temporary
	if childTemp != (ids.SessionToken{}) && parentTemp == (ids.SessionToken{}) {
		return sqlError("42P16", "constraints on temporary tables may reference only temporary tables")
	}
	if childTemp == (ids.SessionToken{}) && parentTemp != (ids.SessionToken{}) {
		return sqlError("42P16", "constraints on permanent tables may reference only permanent tables")
	}
	if childTemp != parentTemp {
		return ids.ErrCatalogReplaced
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
		if _, generated := child.ColumnMeta.Generations[a]; !generated {
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
		for _, key := range parent.Constraints.Unique {
			if key.Primary {
				referenced = slices.Clone(key.Attributes)
				break
			}
		}
		if referenced == nil {
			return sqlError("42704", fmt.Sprintf(`there is no primary key for referenced table "%s"`, parent.Name()))
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
	var index *catalog.Relation
	if supporting == (ids.ObjectAddress{}) {
		index, err = foreignKeyIndex(view, parent, referenced)
	} else {
		var ok bool
		index, ok = view.Relation(supporting)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		var eligible bool
		eligible, err = foreignKeyIndexEligible(parent, index, referenced)
		if err == nil && !eligible {
			return ids.ErrCatalogReplaced
		}
	}
	if err != nil {
		return err
	}
	if err := foreignKeyReferencesAllowed(auth.View, auth, parent, referenced); err != nil {
		return err
	}
	used := catalog.ConstraintNames(view, child.Namespace(), child)
	name := raw.Name.Text
	if name == "" {
		labels := make([]string, len(raw.Columns))
		for i, column := range raw.Columns {
			labels[i] = column.Text
		}
		name = catalog.GeneratedConstraintName(child.Name(), strings.Join(labels, "_"), "fkey", used)
	}
	if catalog.RelationConstraintNameExists(child, name) {
		return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, child.Name()))
	}
	f := catalog.ForeignKeyConstraint{Name: name, Attributes: local, Target: parent.Address(), Referenced: referenced, Index: index.Address(), Full: raw.Full, UpdateAction: raw.UpdateAction, DeleteAction: raw.DeleteAction, DeleteAttributes: deleteAttributes, NotValid: raw.NotValid}
	if _, err := bindForeignKeyComparison(view, child, parent, index, f); err != nil {
		return err
	}
	f.OID, err = e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	child.Constraints.ForeignKeys = append(child.Constraints.ForeignKeys, f)
	stage.UpdateDescriptor(child)
	return nil
}

func (e *Engine) stageCreateForeignKeys(auth AuthorizationView, stage *CatalogStage, child *catalog.Relation, s *parser.CreateTable, targets []ids.ObjectRef) error {
	declarations := foreignKeyDeclarations(s)
	if len(targets) != 0 && len(targets) != len(declarations) {
		return fmt.Errorf("stuntgres: invalid foreign-key creation authority")
	}
	for i, raw := range declarations {
		var target ids.ObjectAddress
		if len(targets) != 0 {
			auth.Catalog = e.stageRead(stage)
			auth.Recapture(auth.Catalog)
			current, err := e.resolveReference(auth, raw.Target)
			if err != nil {
				return err
			}
			captured := targets[i]
			if captured == (ids.ObjectRef{}) {
				if current.Address != child.Address() {
					return ids.ErrCatalogReplaced
				}
			} else if current != captured {
				return ids.ErrCatalogReplaced
			}
			target = current.Address
		}
		if err := e.stageForeignKey(auth, stage, child, raw, target, ids.ObjectAddress{}); err != nil {
			return err
		}
	}
	return nil
}

func validateForeignKeys(view catalog.Read, child *catalog.Relation, names map[string]bool) error {
	for _, f := range child.Constraints.ForeignKeys {
		if child.RelationKind != catalog.TableRelation || f.OID == 0 || f.Name == "" || len(f.Name) > 63 || !utf8.ValidString(f.Name) || strings.IndexByte(f.Name, 0) >= 0 || names[f.Name] || len(f.Attributes) == 0 || len(f.Attributes) != len(f.Referenced) {
			return fmt.Errorf("stuntgres: invalid foreign-key definition")
		}
		if f.UpdateAction > parser.ForeignKeySetDefault || f.DeleteAction > parser.ForeignKeySetDefault || len(f.DeleteAttributes) != 0 && f.DeleteAction != parser.ForeignKeySetNull && f.DeleteAction != parser.ForeignKeySetDefault {
			return fmt.Errorf("stuntgres: invalid foreign-key action")
		}
		for i, a := range f.DeleteAttributes {
			if !slices.Contains(child.Attributes, a) || !slices.Contains(f.Attributes, a) || slices.Contains(f.DeleteAttributes[:i], a) {
				return fmt.Errorf("stuntgres: invalid foreign-key action attribute")
			}
		}
		names[f.Name] = true
		parent, ok := view.Relation(f.Target)
		if !ok || parent.RelationKind != catalog.TableRelation {
			return fmt.Errorf("stuntgres: missing foreign-key target")
		}
		index, ok := view.Relation(f.Index)
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
		if view.MustNamespace(parent.Namespace()).Temporary != view.MustNamespace(child.Namespace()).Temporary {
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

func foreignKeyDiagnostic(ctx context.Context, view access.View, env access.Environment, child, parent *catalog.Relation, f catalog.ForeignKeyConstraint, values []sqltype.Value, parentSide, partial bool, output sqltype.OutputEnvironment) error {
	d := sqlError("23503", fmt.Sprintf(`insert or update on table "%s" violates foreign key constraint "%s"`, child.Name(), f.Name)).(*sqlstate.Error)
	d.Schema = view.Catalog.Get(child.Namespace()).Name()
	d.Table, d.Constraint = child.Name(), f.Name
	if partial {
		d.Detail = "MATCH FULL does not allow mixing of null and nonnull key values."
		return d
	}
	violating, attributes, suffix := child, f.Attributes, fmt.Sprintf(` is not present in table "%s".`, parent.Name())
	if parentSide {
		d.Message = fmt.Sprintf(`update or delete on table "%s" violates foreign key constraint "%s" on table "%s"`, parent.Name(), f.Name, child.Name())
		violating, attributes, suffix = parent, f.Referenced, fmt.Sprintf(` is still referenced from table "%s".`, child.Name())
	}
	disposition, err := access.RowSecurityDecision(view.Roles, violating, view.State.CurrentUser, catalog.PolicySelect, false)
	if err != nil {
		return err
	}
	if disposition != access.RLSUnrestricted {
		d.Detail = "Key" + suffix
		return d
	}
	keys := make([]catalog.IndexKey, len(attributes))
	for i, a := range attributes {
		keys[i].Attribute = a
	}
	allowed, err := index.UniqueKeyDetailAllowed(view, env, view.Ref(violating.Address()), keys)
	if err != nil {
		return err
	}
	if !allowed {
		d.Detail = "Key" + suffix
		return d
	}
	labels, text := make([]string, len(attributes)), make([]string, len(attributes))
	for i, a := range attributes {
		slot := slices.Index(violating.Attributes, a)
		labels[i] = violating.Columns[slot].Name
		text[i], err = values[slot].Type().OutputWithEnvironment(ctx, values[slot], output)
		if err != nil {
			return err
		}
	}
	d.Detail = "Key (" + strings.Join(labels, ", ") + ")=(" + strings.Join(text, ", ") + ")" + suffix
	return d
}
