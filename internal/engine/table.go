package engine

import (
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Called with e.mu held; every return releases it. Preflight the whole group
// and its stable-column AUTO sequence dependencies before publication.
func (e *Engine) dropTableLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.DropTable, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if len(s.Names) == 0 || s.Mode != parser.DropRestrict && s.Mode != parser.DropCascade {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: invalid table drop")
	}
	refs := make([]ObjectRef, 0, len(s.Names))
	seen := make(map[ObjectAddress]bool, len(s.Names))
	var notices []sqlstate.Error
	for _, name := range s.Names {
		if len(name.Parts) < 1 || len(name.Parts) > 2 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: invalid relation name")
		}
		kind, missing := "table", name.Parts[len(name.Parts)-1].Text
		if len(name.Parts) == 2 {
			_, exists := auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Parts[0].Text}]
			if !exists {
				kind, missing = "schema", name.Parts[0].Text
			}
		}
		var ref ObjectRef
		var err error
		if kind == "schema" {
			err = sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, missing))
		} else {
			ref, err = e.resolveReference(auth, name)
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42P01" {
				err = sqlError("42P01", fmt.Sprintf(`table "%s" does not exist`, missing))
			}
		}
		if err != nil {
			d, ok := err.(*sqlstate.Error)
			if !s.IfExists || !ok || (d.Code != "42P01" && d.Code != "3F000") {
				e.mu.Unlock()
				return Result{}, err
			}
			notices = append(notices, sqlstate.Error{
				Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000",
				Message: fmt.Sprintf(`%s "%s" does not exist, skipping`, kind, missing),
			})
			continue
		}
		relation := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if relation.relationKind != tableRelation {
			if relation.relationKind == indexRelation {
				e.mu.Unlock()
				return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a table`, relation.name), Hint: "Use DROP INDEX to remove an index."}
			}
			e.mu.Unlock()
			hint := "Use DROP SEQUENCE to remove a sequence."
			if relation.relationKind == viewRelation {
				hint = "Use DROP VIEW to remove a view."
			}
			return Result{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a table`, relation.name), Hint: hint}
		}
		if seen[ref.Address] {
			continue
		}
		tableErr := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
		if tableErr != nil {
			descriptor := auth.catalog.state.descriptors[ref.Address]
			nsref := ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: descriptor.Namespace()}
			if err := auth.Check(AccessRequirement{Object: nsref, OwnerOnly: true}); err != nil {
				e.mu.Unlock()
				return Result{}, tableErr
			}
		}
		seen[ref.Address] = true
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		e.mu.Unlock()
		for _, notice := range notices {
			op.emitNotice(levelNotice, notice)
		}
		return Result{}, nil
	}
	base := auth.catalog
	stage := e.BeginCatalogStage(base)
	stage.authSession = op.session
	stage.authRevision = auth.revision
	stage.roleRevision = auth.roles.state.revision
	path, err := e.referencePath(auth)
	if err != nil {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	stage.dependencyPath = path
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, ref := range refs {
		if err := stage.Remove(ref); err != nil {
			return Result{}, err
		}
	}
	targets := make([]ObjectAddress, len(refs))
	for i, ref := range refs {
		targets[i] = ref.Address
	}
	if err := stageViewDependents(stage, targets, s.Mode); err != nil {
		return Result{}, err
	}
	if err := scope.stageOwnedSequences(stage, targets, s.Mode); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: base.Epoch()})
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	emitViewCascade(op, stage.viewCascadeNames)
	for _, notice := range notices {
		op.emitNotice(levelNotice, notice)
	}
	return Result{}, nil
}

func isSystemColumn(name string) bool {
	switch name {
	case "ctid", "xmin", "cmin", "xmax", "cmax", "tableoid":
		return true
	}
	return false
}
