package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (r *restoreRun) inertAttributeHelperLocked(auth core.AuthorizationView, target control.AttributeDropTarget) (*catalog.Relation, error) {
	d, err := r.attributeRelationLocked(auth, target)
	if err != nil {
		return nil, err
	}
	if !control.AttributeHelperName(d.Name()) || d.RelationKind != catalog.TableRelation || d.Owner != r.stage.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID) || len(d.Columns) != 0 || d.ColumnMeta.HighWater == 0 || int(d.ColumnMeta.HighWater) != len(d.ColumnMeta.Dropped) || d.OfType != (ids.ObjectAddress{}) || d.Membership != nil || d.Partition != nil || d.Foreign != nil || d.SQLIndex != nil || d.Sequence != nil || d.View != nil || d.Rules != nil && d.Rules.Len() != 0 || d.Triggers != nil && d.Triggers.Len() != 0 || d.Security.ACL.Explicit || len(d.Security.ColumnACL) != 0 || len(d.Security.Policies) != 0 || d.Security.RLSEnabled || d.Security.RLSForced || d.History.HasIndexes || len(d.StorageOptions.Base)+len(d.StorageOptions.Toast) != 0 {
		return nil, attributeRestoreBoundary()
	}
	if d.Inheritance != nil && len(d.Inheritance.Parents) != 0 {
		return nil, attributeRestoreBoundary()
	}
	if len(d.Constraints.Check)+len(d.Constraints.NotNull)+len(d.Constraints.Unique)+len(d.Constraints.Exclusion)+len(d.Constraints.ForeignKeys) != 0 {
		return nil, attributeRestoreBoundary()
	}
	for _, fact := range d.ColumnMeta.Dropped {
		if !fact.Dropped || !fact.Local || fact.Inheritance != 0 {
			return nil, attributeRestoreBoundary()
		}
	}
	return d, nil
}

func (r *restoreRun) admitAttributeHelperDetach(op *session.Operation, s *parser.AlterTable) error {
	pair, ok := r.attributePairs.Detaches[s]
	if !ok {
		return attributeRestoreBoundary()
	}
	e := r.stage
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	child, err := r.attributeRelationLocked(auth, pair.Child)
	if err != nil {
		return err
	}
	helper, err := r.inertAttributeHelperLocked(auth, pair.Helper)
	if err != nil {
		return err
	}
	if child.Inheritance == nil || !child.Inheritance.HasParent(helper.Address()) {
		return attributeRestoreBoundary()
	}
	delete(r.attributePairs.Detaches, s)
	return nil
}

func (r *restoreRun) admitAttributeHelperTeardown(op *session.Operation, s *parser.DropTable) error {
	target, ok := r.attributePairs.Teardowns[s]
	if !ok {
		return attributeRestoreBoundary()
	}
	e := r.stage
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	helper, err := r.inertAttributeHelperLocked(auth, target)
	if err != nil {
		return err
	}
	if len(catalog.InheritanceChildren(auth.Catalog, helper.Address())) != 0 {
		return attributeRestoreBoundary()
	}
	delete(r.attributePairs.Teardowns, s)
	delete(r.attributeConstructors, target)
	return nil
}
