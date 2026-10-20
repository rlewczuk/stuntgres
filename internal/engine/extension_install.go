package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// extensionInstaller is the engine side of an implementation: staging needs
// the engine and its catalog stage, which catalog code never names.
type extensionInstaller interface {
	catalog.ExtensionImplementation
	stage(*Engine, *CatalogStage, *catalog.Extension, authorization.PrincipalRef, AuthorizationView) error
}

// Registration is initialization-only and duplicate checked, like catalog
// provider registration. It does not execute scripts or load external packages.
func (e *Engine) registerExtension(name string, implementation extensionInstaller) error {
	if e.catalog.state != nil {
		return fmt.Errorf("stuntgres: extension registry is closed")
	}
	if name == "" || implementation == nil || implementation.Control().Version == "" {
		return fmt.Errorf("stuntgres: invalid extension registration")
	}
	if catalog.ExtensionNameDetail(name, false) != "" {
		return fmt.Errorf("stuntgres: invalid extension registration name")
	}
	if err := validateExtensionActions(implementation); err != nil {
		return err
	}
	if e.catalog.Extensions == nil {
		e.catalog.Extensions = make(map[string]catalog.ExtensionImplementation)
	}
	if _, exists := e.catalog.Extensions[name]; exists {
		return fmt.Errorf("stuntgres: duplicate extension registration %q", name)
	}
	e.catalog.Extensions[name] = implementation
	return nil
}

func (e *Engine) executeExtension(scope *StatementScope, s *parser.CreateExtension, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(scope, s)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	return e.publishExtensionStage(scope, stage)
}

// CREATE and UPDATE publish through the same validated statement-owned changes.
// Provider callbacks run in ValidateCatalogStage outside the engine mutex.
func (e *Engine) publishExtensionStage(scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(scope.op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	changes := cloneCatalogChanges(scope.owner.catalog)
	for a, d := range stage.Descriptors() {
		// Validation ran without the mutex. Capture the stage's exact
		// predecessor, never whatever a peer published during validation.
		if _, captured := changes.originals[a]; !captured {
			changes.originals[a] = stage.Base.Get(a)
		}
		if _, extension := d.(*catalog.Extension); extension {
			if old, exists := stage.Base.Extension(a); exists {
				for _, member := range old.Members {
					if _, captured := changes.originals[member]; !captured {
						changes.originals[member] = stage.Base.Get(member)
					}
				}
			}
		}
		e.catalogChangeLocked(changes, a, d)
	}
	for key, original := range stage.Touched() {
		changes.names[key] = original
	}
	changes.extensionAuthority = append(changes.extensionAuthority, extensionAuthorityObservation{roles: stage.roleRevision, namespace: stage.Base.Get(stage.extensionNamespace), database: stage.databasePredecessor})
	scope.catalog = changes
	scope.catalogAuthority = &catalogAuthority{session: scope.op.session, authorization: stage.authRevision, roles: stage.roleRevision, ref: ids.ObjectRef{Address: catalog.CurrentDatabaseAddress}, descriptor: stage.databasePredecessor, databaseAuthority: stage.databaseAuthority, databasePredecessor: stage.databasePredecessor}
	e.mu.Unlock()
	return Result{}, nil
}

func (e *Engine) stageExtensionLocked(scope *StatementScope, s *parser.CreateExtension) (*CatalogStage, error) {
	op := scope.op
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return nil, err
	}
	key := ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}
	if auth.Catalog.NameAddress(key) != (ids.ObjectAddress{}) {
		if s.IfNotExists {
			op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: fmt.Sprintf(`extension "%s" already exists, skipping`, s.Name.Text)})
			return nil, nil
		}
		return nil, sqlError("42710", fmt.Sprintf(`extension "%s" already exists`, s.Name.Text))
	}
	implementation, ok := e.catalog.Extensions[s.Name.Text]
	installer, installable := implementation.(extensionInstaller)
	if !ok || !installable {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`extension "%s" is not available`, s.Name.Text)}
	}
	registration := implementation.Control()
	version := registration.Version
	var schema *parser.Identifier
	seen := map[string]bool{}
	for _, option := range s.Options {
		if seen[option.Name] {
			return nil, expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
		}
		seen[option.Name] = true
		switch option.Name {
		case "version":
			version = option.Value.Text
		case "schema":
			value := option.Value
			schema = &value
		default:
			return nil, fmt.Errorf("stuntgres: invalid extension option")
		}
	}
	if err := extensionVersionError(version); err != nil {
		return nil, err
	}
	path := implementation.Versions().InstallPath(version)
	if path == nil {
		return nil, sqlError("22023", fmt.Sprintf(`extension "%s" has no installation script nor update path for version "%s"`, s.Name.Text, version))
	}
	var namespace ids.ObjectAddress
	if schema != nil {
		namespace = auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema.Text})
		if namespace == (ids.ObjectAddress{}) {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, schema.Text))
		}
	} else {
		path, _, pathErr := e.explicitPath(auth)
		if pathErr != nil {
			return nil, pathErr
		}
		if len(path) == 0 {
			return nil, sqlError("3F000", "no schema has been selected to create in")
		}
		namespace = path[0]
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	if !actor.Superuser && !op.restoreTableDestination {
		if !registration.Trusted {
			return nil, sqlError("42501", fmt.Sprintf(`permission denied to create extension "%s"`, s.Name.Text))
		}
		rights, err := auth.Effective(e.catalog.Domain.Ref(auth.Catalog.Epoch(), catalog.CurrentDatabaseAddress), false)
		if err != nil {
			return nil, err
		}
		if rights&authorization.CreatePrivilege == 0 {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to create extension "%s"`, s.Name.Text), Hint: "Must have CREATE privilege on current database to create this extension."}
		}
	}
	memberOwner := actor.Ref
	if registration.Superuser && !actor.Superuser {
		memberOwner = e.roles.domain.Principal(10)
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return nil, err
	}
	rev, err := e.catalog.revisionLocked()
	if err != nil {
		return nil, err
	}
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectRevision: rev, ObjectName: s.Name.Text, Version: path[0], TargetNamespace: namespace, Owner: actor.Ref}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.session)
	stage.extensionNamespace = namespace
	if err := stage.Create(ext); err != nil {
		return nil, err
	}
	if err := installer.stage(e, stage, ext, memberOwner, auth); err != nil {
		return nil, err
	}
	if err := stageExtensionPath(op.Context(), e, stage, implementation, ext, memberOwner, auth, path); err != nil {
		return nil, err
	}
	return stage, nil
}
