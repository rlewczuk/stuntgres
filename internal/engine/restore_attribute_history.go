package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func attributeRestoreBoundary() error {
	return core.SQLError("0A000", "restore accepts only paired generated attribute constructors")
}

// The caller holds stage.Mu. Literal names supplement, never replace, the fresh
// constructor's destination-owned reference and current catalog lifetime.
func (r *restoreRun) attributeRelationLocked(auth core.AuthorizationView, target control.AttributeDropTarget) (*catalog.Relation, error) {
	ref, ok := r.attributeConstructors[target]
	if !ok || ref.Domain() != r.stage.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() {
		return nil, attributeRestoreBoundary()
	}
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok || d.Name() != target.Table || auth.Catalog.Get(d.Namespace()).Name() != target.Schema {
		return nil, attributeRestoreBoundary()
	}
	return d, nil
}

func attributeMemberMatches(d *catalog.Relation, m control.AttributeMember, name string) bool {
	i := d.IndexForAttribute(m.Number)
	if i < 0 || d.Columns[i].Name != name {
		return false
	}
	metadata := pgcatalog.AttributeMetadata(d, i)
	return metadata.Local == m.Local && int(metadata.Inheritance) == m.Inheritance
}

func (r *restoreRun) admitAttributeRetirement(op *session.Operation, s *parser.AlterTable) error {
	pair, ok := r.attributePairs.Drops[s]
	if !ok || len(s.Actions) != 1 || s.Actions[0].Column.Text != pair.Column || s.Only == pair.Recursive {
		return attributeRestoreBoundary()
	}
	e := r.stage
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	if err := op.Context().Err(); err != nil {
		return err
	}
	root, err := r.attributeRelationLocked(auth, pair.Target)
	if err != nil {
		return err
	}
	addresses := []ids.ObjectAddress{root.Address()}
	if pair.Recursive || root.RelationKind == catalog.TableRelation {
		addresses, err = attributeRetirementClosure(op.Context(), auth.Catalog, root.Address())
		if err != nil {
			return err
		}
	}
	var seen []control.AttributeDropTarget
	for _, address := range addresses {
		d := auth.Catalog.MustRelation(address)
		slot, exists := d.Index[pair.Column]
		if !exists {
			continue
		}
		target := control.AttributeDropTarget{Schema: auth.Catalog.Get(d.Namespace()).Name(), Table: d.Name()}
		at := slices.IndexFunc(pair.Participants, func(m control.AttributeMember) bool { return m.Target == target })
		if at < 0 {
			return attributeRestoreBoundary()
		}
		if _, err := r.attributeRelationLocked(auth, target); err != nil {
			return err
		}
		if !attributeMemberMatches(d, pair.Participants[at], pair.Column) {
			return attributeRestoreBoundary()
		}
		column := d.Columns[slot]
		number := d.Attributes[slot]
		if column.Usage.Type == nil || column.Usage.Type.ID() != sqltype.Int4 || !column.Nullable || column.Usage != column.Usage.Type.Usage() {
			return attributeRestoreBoundary()
		}
		if _, exists := d.ColumnMeta.Defaults[number]; exists {
			return attributeRestoreBoundary()
		}
		if _, exists := d.ColumnMeta.Generations[number]; exists {
			return attributeRestoreBoundary()
		}
		if _, exists := d.ColumnMeta.Identities[number]; exists {
			return attributeRestoreBoundary()
		}
		if d.Security.ColumnACL[number].Explicit {
			return attributeRestoreBoundary()
		}
		for _, constraint := range d.Constraints.NotNull {
			if constraint.Attribute == number {
				return attributeRestoreBoundary()
			}
		}
		seen = append(seen, target)
	}
	if len(seen) != len(pair.Participants) {
		return attributeRestoreBoundary()
	}
	// The exact fresh constructor and inert retirement closure above grant
	// destination lookup only; ordinary ALTER still enforces ownership.
	r.cur.attributeConstruction = true
	delete(r.attributePairs.Drops, s)
	return nil // Ordinary isolated DDL owns all dependency checks and publication.
}

func (r *restoreRun) admitAttributeDefinition(op *session.Operation, s *parser.AlterTable) error {
	add, adding := r.attributePairs.Adds[s]
	target, constraint := r.attributePairs.Constraints[s]
	if adding {
		target = add.Target
	}
	if !adding && !constraint {
		return attributeRestoreBoundary()
	}
	if err := control.CheckAttributeDefinition(s); err != nil {
		return err
	}
	if s.Name.Parts[0].Text != target.Schema || s.Name.Parts[1].Text != target.Table {
		return attributeRestoreBoundary()
	}
	e := r.stage
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	d, err := r.attributeRelationLocked(auth, target)
	if err == nil && adding && (d.ColumnMeta.HighWater+1 != add.Number || d.IndexForAttribute(add.Number) >= 0) {
		err = attributeRestoreBoundary()
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	// A paired staged declaration has its own fresh-constructor authority.
	// CHECKs may legitimately merge inherited definitions here; generation
	// recipes authorize a different replay path, not these constructors.
	r.cur.attributeConstruction = true
	if adding {
		r.cur.attributeAdd = &add
		delete(r.attributePairs.Adds, s)
	} else {
		delete(r.attributePairs.Constraints, s)
	}
	return nil
}

// Valid foreign keys use the incumbent deferred-target/finish binder. Their
// portable SQL runs after rows; native capture preserves that same final state.
func beforeRestoreAttributeForeignKey(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	s, ok := stmt.(*parser.AlterTable)
	if !ok || len(s.Actions) != 1 || s.Actions[0].Action != parser.AddForeignKey {
		return false, nil
	}
	target, paired := r.attributePairs.Constraints[s]
	if !paired {
		return false, nil
	}
	if r.physicalForeignProvider != nil || r.cur.physicalConstructor != nil {
		return false, nil
	}
	if err := control.CheckAttributeDefinition(s); err != nil {
		return false, err
	}
	e := r.stage
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var d *catalog.Relation
	if err == nil {
		d, err = r.attributeRelationLocked(auth, target)
	}
	var ref ids.ObjectRef
	if err == nil {
		ref = auth.View.Ref(d.Address())
	}
	e.Mu.Unlock()
	if err != nil {
		return false, err
	}
	creation := &parser.CreateTable{Name: s.Name, Elements: []parser.TableElement{{ForeignKey: s.Actions[0].ForeignKey}}}
	pending, err := tablecmds.CaptureRestoredForeignKeys(e, op, table.ForeignKeyDeclarations(creation))
	if err != nil {
		return false, err
	}
	pending.Child = ref
	if err := tablecmds.ResolveRestoredForeignKeyTargets(e, &pending); err != nil {
		return false, err
	}
	r.pendingForeignKeys = append(r.pendingForeignKeys, pending)
	delete(r.attributePairs.Constraints, s)
	return true, nil
}

func afterRestoreAttributeConstructor(r *restoreRun, _ *session.Operation, stmt parser.Statement, outcome control.Outcome) error {
	creation, ok := stmt.(*parser.CreateTable)
	if !ok {
		return nil
	}
	target, paired := r.attributePairs.Creates[creation]
	if !paired {
		return nil
	}
	if outcome.Created == (ids.ObjectRef{}) {
		return attributeRestoreBoundary()
	}
	r.attributeConstructors[target] = outcome.Created
	delete(r.attributePairs.Creates, creation)
	return nil
}

func afterRestoreAttributeDefinition(r *restoreRun, _ *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	pair := r.cur.attributeAdd
	if pair == nil {
		return nil
	}
	s := stmt.(*parser.AlterTable)
	column := s.Actions[0].Definition
	view := r.stage.CatalogRead()
	for i, member := range pair.Participants {
		ref, ok := r.attributeConstructors[member.Target]
		if !ok {
			return attributeRestoreBoundary()
		}
		d, ok := view.Relation(ref.Address)
		if !ok || !attributeMemberMatches(d, member, column.Name) {
			return attributeRestoreBoundary()
		}
		creation := &parser.CreateTable{}
		if i == 0 {
			creation.Elements = []parser.TableElement{{Column: column}}
		}
		table.CaptureRestoredPlaceholders(view, ref, creation, r.placeholderOrigins)
		table.CaptureHistoricalRelation(view, ref, r.historicalAuthority.Facts)
	}
	return nil
}

func finishRestoreAttributeRetirements(r *restoreRun, _ context.Context, _ *session.Operation) error {
	if len(r.attributePairs.Drops)+len(r.attributePairs.Adds)+len(r.attributePairs.Constraints)+len(r.attributePairs.Creates)+len(r.attributePairs.Detaches)+len(r.attributePairs.Teardowns) != 0 {
		return core.SQLError("0A000", "restore has unconsumed generated attribute constructors")
	}
	return nil
}
