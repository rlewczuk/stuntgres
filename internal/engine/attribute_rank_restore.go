package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Restore alone calls this interpreter, against its isolated catalog. It is not
// an UPDATE execution path and never grants writable access to catalog sources.
func (e *Engine) restoreAttributeRank(op *session.Operation, raw *parser.Update) error {
	name, column, rank, err := restoreAttributeRankSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "permission denied for table pg_attribute")
	}
	e.mu.Lock()
	if e.importTargets == nil || auth.engine != e || auth.session != op.Session || auth.RoleRecord != e.roles.published || auth.Catalog.Owner() == nil || auth.Catalog.Owner().Domain != e.catalog.Domain || auth.Catalog.State() != e.catalog.state || auth.Catalog.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := auth.Catalog.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation {
		e.mu.Unlock()
		return sqlError("0A000", restoreAttributeRankBoundary)
	}
	slot, exists := old.Index[column]
	if !exists {
		e.mu.Unlock()
		return sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, old.Name()))
	}
	if rank != 0 && !old.Columns[slot].Usage.Type.IsArray() {
		e.mu.Unlock()
		return sqlError("0A000", restoreAttributeRankBoundary)
	}
	if old.Columns[slot].DeclaredDimensions == rank {
		e.mu.Unlock()
		return nil
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Columns = slices.Clone(old.Columns)
	next.Columns[slot].DeclaredDimensions = rank
	staged := e.BeginCatalogStage(auth.Catalog)
	staged.authorizeLocked(auth, op.Session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(staged)
	if err = staged.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), staged)
	if err != nil {
		return err
	}
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: ref.Epoch})
	return err
}
