package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type extensionUpdateAction struct {
	edge  catalog.ExtensionVersionEdge
	stage func(*Engine, *CatalogStage, *catalog.Extension, authorization.PrincipalRef, AuthorizationView) error
}
type extensionUpdater interface {
	updates() []extensionUpdateAction
}

func extensionActions(implementation catalog.ExtensionImplementation) []extensionUpdateAction {
	if updater, ok := implementation.(extensionUpdater); ok {
		return updater.updates()
	}
	return nil
}
func validateExtensionActions(implementation catalog.ExtensionImplementation) error {
	versions := implementation.Versions()
	if err := versions.Validate(implementation.Control().Version); err != nil {
		return err
	}
	seen := map[catalog.ExtensionVersionEdge]bool{}
	for _, action := range extensionActions(implementation) {
		if action.stage == nil || seen[action.edge] || !slices.Contains(versions.Edges, action.edge) {
			return fmt.Errorf("stuntgres: invalid extension update action")
		}
		seen[action.edge] = true
	}
	for _, edge := range versions.Edges {
		if versions.InstallPath(edge.From) != nil && !seen[edge] {
			return fmt.Errorf("stuntgres: executable extension edge has no action")
		}
	}
	return nil
}

func stageExtensionPath(ctx context.Context, e *Engine, stage *CatalogStage, implementation catalog.ExtensionImplementation, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView, path []string) error {
	for i := 1; i < len(path); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		edge := catalog.ExtensionVersionEdge{From: path[i-1], To: path[i]}
		found := false
		for _, action := range extensionActions(implementation) {
			if action.edge == edge {
				if err := action.stage(e, stage, ext, owner, auth); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found || ext.Version != edge.To {
			return fmt.Errorf("stuntgres: unavailable executable extension transition")
		}
	}
	return nil
}

func (e *Engine) executeExtensionUpdate(scope *StatementScope, s *parser.AlterExtensionUpdate, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	stage, err := e.stageExtensionUpdateLocked(scope, s)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	return e.publishExtensionStage(scope, stage)
}

func (e *Engine) stageExtensionUpdateLocked(scope *StatementScope, s *parser.AlterExtensionUpdate) (*CatalogStage, error) {
	if err := scope.op.Context().Err(); err != nil {
		return nil, err
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text})
	old, exists := auth.Catalog.Extension(address)
	if !exists {
		return nil, sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, s.Name.Text))
	}
	if err := auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), address), OwnerOnly: true, OwnerNoun: "extension"}); err != nil {
		return nil, err
	}
	implementation := e.catalog.Extensions[old.Name()]
	if implementation == nil {
		return nil, fmt.Errorf("stuntgres: installed extension has no implementation")
	}
	version := implementation.Control().Version
	for i, option := range s.Options {
		if i != 0 {
			return nil, expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
		}
		version = option.Value.Text
	}
	if err := extensionVersionError(version); err != nil {
		return nil, err
	}
	if old.Version == version {
		scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`version "%s" of extension "%s" is already installed`, version, old.Name())})
		return nil, nil
	}
	path := implementation.Versions().Path(old.Version, version)
	if path == nil {
		return nil, sqlError("22023", fmt.Sprintf(`extension "%s" has no update path from version "%s" to version "%s"`, old.Name(), old.Version, version))
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	control := implementation.Control()
	if !actor.Superuser {
		if !control.Trusted {
			return nil, sqlError("42501", fmt.Sprintf(`permission denied to update extension "%s"`, old.Name()))
		}
		rights, err := auth.Effective(e.catalog.Domain.Ref(auth.Catalog.Epoch(), catalog.CurrentDatabaseAddress), false)
		if err != nil {
			return nil, err
		}
		if rights&authorization.CreatePrivilege == 0 {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to update extension "%s"`, old.Name()), Hint: "Must have CREATE privilege on current database to update this extension."}
		}
	}
	owner := actor.Ref
	if control.Superuser && !actor.Superuser {
		owner = e.roles.domain.Principal(10)
	}
	ext := *old
	// The selected stock transition creates one new member. Reserve that slot
	// while detaching, so the typed action needs no second slice allocation.
	ext.Members = make([]ids.ObjectAddress, len(old.Members), len(old.Members)+1)
	copy(ext.Members, old.Members)
	ext.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.session)
	stage.extensionNamespace = ext.TargetNamespace
	if err := stage.Replace(&ext); err != nil {
		return nil, err
	}
	if err := stageExtensionPath(scope.op.Context(), e, stage, implementation, &ext, owner, auth, path); err != nil {
		return nil, err
	}
	return stage, nil
}
