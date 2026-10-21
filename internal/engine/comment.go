package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// Comment reads immutable metadata from r, a view e owns, without applying an
// ACL to its text.
func (e *Engine) Comment(r catalog.Read, ref ids.ObjectRef) (string, bool, error) {
	if r.Owner() == nil {
		return "", false, fmt.Errorf("stuntgres: foreign catalog view")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.catalog.validateObjectRefLocked(r, ref); err != nil {
		return "", false, err
	}
	a := ref.Address
	sub := a.SubID
	a.SubID = 0
	switch r.Get(a).(type) {
	case *catalog.Namespace, *catalog.Type, *catalog.Relation:
	default:
		return "", false, fmt.Errorf("stuntgres: object class has no comment metadata")
	}
	text := pgcatalog.DescriptorComment(r.Get(a), sub)
	return text, text != "", nil
}

func (e *Engine) executeComment(scope *StatementScope, s *parser.Comment, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.op
	e.mu.Lock()
	stage, err := e.stageCommentLocked(scope, s)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}

func (e *Engine) stageCommentLocked(scope *StatementScope, s *parser.Comment) (*CatalogStage, error) {
	op := scope.op
	if err := scope.validateLockedRelation(e); err != nil {
		return nil, err
	}
	auth, err := e.statementAuthorizationLocked(op, s)
	if err != nil {
		return nil, err
	}
	if err = op.Context().Err(); err != nil {
		return nil, err
	}
	text := s.Text
	if s.Null {
		text = ""
	}
	if s.Kind == parser.CommentRole {
		target, ok := auth.Roles.Lookup(s.Name.Parts[0].Text)
		if !ok {
			return nil, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, s.Name.Parts[0].Text))
		}
		actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return nil, err
		}
		if !actor.Superuser {
			attribute := "CREATEROLE"
			if target.Superuser {
				attribute = "SUPERUSER"
			}
			return nil, role.Diagnostic("42501", "permission denied", "The current user must have the "+attribute+" attribute.")
		}
		if auth.RoleRecord.Revision == ^uint64(0) {
			return nil, fmt.Errorf("stuntgres: authorization revision exhausted")
		}
		target.Comment = text
		next, err := auth.RoleRecord.Root.PutDescriptor(target)
		if err != nil {
			return nil, err
		}
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := publicationOperationLocked(e, op); err != nil {
			return nil, err
		}
		e.installOperationRolesLocked(op, &access.RoleRecord{Root: next, Revision: auth.RoleRecord.Revision + 1})
		return nil, nil
	}
	var ref ids.ObjectRef
	switch s.Kind {
	case parser.CommentSchema:
		a, ok := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: s.Name.Parts[0].Text})
		if !ok {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, s.Name.Parts[0].Text))
		}
		ref = e.catalog.Domain.Ref(auth.Catalog.Epoch(), a)
	case parser.CommentTable, parser.CommentColumn:
		ref = scope.lockedRef
		if ref.Domain() == nil {
			return nil, fmt.Errorf("stuntgres: COMMENT relation lock is missing")
		}
		relation := auth.Catalog.MustRelation(ref.Address)
		operation := "comment table"
		if s.Kind == parser.CommentColumn {
			operation = "comment column"
		}
		if err := analyze.TableKindError(relation, operation); err != nil {
			return nil, err
		}
		if err := auth.CanUse(e.catalog.Domain.Ref(ref.Epoch, relation.Namespace())); err != nil {
			return nil, err
		}
		if s.Kind == parser.CommentColumn {
			d := relation
			name := s.Name.Parts[len(s.Name.Parts)-1].Text
			index, ok := d.Index[name]
			if !ok {
				parts := make([]string, len(s.Name.Parts)-1)
				for i := range parts {
					parts[i] = s.Name.Parts[i].Text
				}
				return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, strings.Join(parts, ".")))
			}
			ref.Address.SubID = d.Attributes[index]
		}
	default:
		return nil, sqlError("0A000", "COMMENT object kind is unsupported")
	}
	if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return nil, err
	}
	a := ref.Address
	a.SubID = 0
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return nil, err
	}
	var updated catalog.Descriptor
	switch d := auth.Catalog.Get(a).(type) {
	case *catalog.Namespace:
		n := *d
		n.Comment = text
		n.ObjectRevision = revision
		updated = &n
	case *catalog.Relation:
		n := *d
		n.ObjectRevision = revision
		if ref.Address.SubID == 0 {
			n.Comment = text
		} else {
			n.ColumnMeta.Comments = make(map[int32]string, len(d.ColumnMeta.Comments)+1)
			for key, value := range d.ColumnMeta.Comments {
				n.ColumnMeta.Comments[key] = value
			}
			if text == "" {
				delete(n.ColumnMeta.Comments, ref.Address.SubID)
			} else {
				n.ColumnMeta.Comments[ref.Address.SubID] = text
			}
		}
		updated = &n
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	if err := stage.Replace(updated); err != nil {
		return nil, err
	}
	return stage, nil
}
