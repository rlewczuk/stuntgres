package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

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
