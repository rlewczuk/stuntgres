package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecuteComment runs COMMENT ON for every object kind except sequences,
// whose comments are tablecmds.ExecuteSequenceComment's.
func ExecuteComment(e *core.Engine, scope *core.StatementScope, s *parser.Comment, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	op := scope.Op
	e.Mu.Lock()
	stage, err := stageCommentLocked(e, scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return core.Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return core.Result{}, err
}

func stageCommentLocked(e *core.Engine, scope *core.StatementScope, s *parser.Comment) (*core.CatalogStage, error) {
	op := scope.Op
	if err := scope.ValidateLockedRelation(e); err != nil {
		return nil, err
	}
	auth, err := e.StatementAuthorizationLocked(op, s)
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
		roles, err := role.PlanRoleComment(auth.View, s.Name.Parts[0].Text, text)
		if err != nil {
			return nil, err
		}
		op.Session.Mu.Lock()
		defer op.Session.Mu.Unlock()
		if err := core.PublicationOperationLocked(e, op); err != nil {
			return nil, err
		}
		return nil, core.InstallOperationRolesLocked(e, op, roles)
	}
	resolved := scope.LockedRef
	if s.Kind == parser.CommentTextSearchDictionary {
		host := core.DDLHost{E: e, Op: op}
		// REL_18_6 get_object_address resolves dictionary COMMENT from a
		// semantic name list through get_ts_dict_oid/LookupExplicitNamespace,
		// not expression locations. Preserve the original AST for diagnostics.
		name := s.Name
		var semanticParts [3]parser.Identifier
		name.Parts = append(semanticParts[:0], s.Name.Parts...)
		for i := range name.Parts {
			name.Parts[i].Pos = parser.Position{}
		}
		path, err := host.SearchNamespaces(auth, name, ids.TSDictionaryClassID)
		if err != nil {
			return nil, err
		}
		for _, namespace := range path {
			address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: namespace, Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
			if address != (ids.ObjectAddress{}) {
				resolved = e.Catalog.Domain.Ref(auth.Catalog.Epoch(), address)
				break
			}
		}
		if resolved.Domain() == nil {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf(`text search dictionary "%s" does not exist`, catalog.ObjectNameText(s.Name))}
		}
	}
	host := RoutineHost{core.DDLHost{E: e, Op: op}}
	switch s.Kind {
	case parser.CommentType, parser.CommentDomain:
		// REG_18_6 COMMENT resolves the type through LookupTypeName without an
		// expression position; the shared regtype seam owns 42704/3F000/USAGE.
		id, err := host.ResolveRegtype(auth, s.Type)
		if err != nil {
			return nil, err
		}
		if s.Kind == parser.CommentDomain {
			typ := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
			if !typ.SQLType.IsDomain() {
				return nil, utilityError("42809", fmt.Sprintf(`"%s" is not a domain`, utilityTypeName(s.Type)))
			}
		}
		resolved = e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
	case parser.CommentFunction, parser.CommentProcedure, parser.CommentRoutine:
		// Shared GRANT routine identity owns bare-name/args/wrong-kind and
		// ambiguity texts; ROUTINE selects both routine kinds.
		proc, _, err := routine.ResolveUtilityRoutine(host, auth, role.CommentRoutineKind(s.Kind), s.Routine)
		if err != nil {
			return nil, err
		}
		resolved = e.Catalog.Domain.Ref(auth.Catalog.Epoch(), proc.Address())
	}
	var target ids.ObjectAddress
	updated, err := role.PlanObjectComment(auth.View, auth, e.Catalog.Domain, s, resolved, text, e.Catalog.RevisionLocked, &target)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	if err := stage.Replace(updated); err != nil {
		return nil, err
	}
	stage.RecordDDLCommand("COMMENT", target)
	return stage, nil
}
