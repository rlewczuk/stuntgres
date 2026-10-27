package role

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// PlanRoleComment decides COMMENT ON ROLE name: the role must exist and the
// current user must be a superuser. It returns the next role record of view,
// with the role's comment set to text (empty removes it); the engine
// publishes it.
func PlanRoleComment(view access.View, name, text string) (*access.RoleRecord, error) {
	target, ok := view.Roles.Lookup(name)
	if !ok {
		return nil, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, name))
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	if !actor.Superuser {
		attribute := "CREATEROLE"
		if target.Superuser {
			attribute = "SUPERUSER"
		}
		return nil, Diagnostic("42501", "permission denied", "The current user must have the "+attribute+" attribute.")
	}
	if view.RoleRecord.Revision == ^uint64(0) {
		return nil, fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	target.Comment = text
	next, err := view.RoleRecord.Root.PutDescriptor(target)
	if err != nil {
		return nil, err
	}
	return view.RoleRecord.WithRoot(next), nil
}

// PlanObjectComment decides COMMENT ON a schema, table, foreign table or
// column: it resolves the target (a relation through locked, the relation
// the engine locked for the statement), checks its kind, schema USAGE and
// ownership, and returns the target's descriptor with the comment set to text
// (empty removes it) and a new revision from revision. domain mints the
// references; the engine stages and publishes the result.
func PlanObjectComment(view access.View, env access.Environment, domain *ids.Domain, s *parser.Comment, locked ids.ObjectRef, text string, revision func() (ids.DefinitionRevision, error)) (catalog.Descriptor, error) {
	var ref ids.ObjectRef
	switch s.Kind {
	case parser.CommentSchema:
		a, ok := view.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: s.Name.Parts[0].Text})
		if !ok {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, s.Name.Parts[0].Text))
		}
		ref = domain.Ref(view.Catalog.Epoch(), a)
	case parser.CommentTable, parser.CommentForeignTable, parser.CommentColumn:
		ref = locked
		if ref.Domain() == nil {
			return nil, fmt.Errorf("stuntgres: COMMENT relation lock is missing")
		}
		relation := view.Catalog.MustRelation(ref.Address)
		operation := "comment table"
		if s.Kind == parser.CommentColumn {
			operation = "comment column"
		}
		if s.Kind == parser.CommentForeignTable {
			if relation.RelationKind != catalog.ForeignRelation {
				return nil, sqlError("42809", fmt.Sprintf(`"%s" is not a foreign table`, relation.Name()))
			}
		} else if !(s.Kind == parser.CommentColumn && relation.RelationKind == catalog.ForeignRelation) {
			if err := analyze.TableKindError(relation, operation); err != nil {
				return nil, err
			}
		}
		if err := view.CanUse(env, domain.Ref(ref.Epoch, relation.Namespace())); err != nil {
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
	if err := view.Check(env, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return nil, err
	}
	a := ref.Address
	a.SubID = 0
	stamp, err := revision()
	if err != nil {
		return nil, err
	}
	var updated catalog.Descriptor
	switch d := view.Catalog.Get(a).(type) {
	case *catalog.Namespace:
		n := *d
		n.Comment = text
		n.ObjectRevision = stamp
		updated = &n
	case *catalog.Relation:
		n := *d
		n.ObjectRevision = stamp
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
	return updated, nil
}
