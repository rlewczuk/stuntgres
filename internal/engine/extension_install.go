package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Registration is initialization-only and duplicate checked, like catalog
// provider registration. It does not execute scripts or load external packages.
func (e *Engine) registerExtension(name string, implementation extension.Installer) error {
	if e.catalog.state != nil {
		return fmt.Errorf("stuntgres: extension registry is closed")
	}
	if name == "" || implementation == nil || implementation.Control().Version == "" {
		return fmt.Errorf("stuntgres: invalid extension registration")
	}
	if catalog.ExtensionNameDetail(name, false) != "" {
		return fmt.Errorf("stuntgres: invalid extension registration name")
	}
	if err := extension.ValidateActions(implementation); err != nil {
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
	changes := scope.owner.Catalog.Clone()
	for a, d := range stage.Descriptors() {
		// Validation ran without the mutex. Capture the stage's exact
		// predecessor, never whatever a peer published during validation.
		if _, captured := changes.Originals[a]; !captured {
			changes.Originals[a] = stage.Base.Get(a)
		}
		if _, extension := d.(*catalog.Extension); extension {
			if old, exists := stage.Base.Extension(a); exists {
				for _, member := range old.Members {
					if _, captured := changes.Originals[member]; !captured {
						changes.Originals[member] = stage.Base.Get(member)
					}
				}
			}
		}
		e.catalogChangeLocked(changes, a, d)
	}
	for key, original := range stage.Touched() {
		changes.Names[key] = original
	}
	changes.AddFence(extensionAuthorityFence{observation: extension.AuthorityObservation{Roles: stage.roleRevision, Namespace: stage.Base.Get(stage.extensionNamespace), Database: stage.databasePredecessor}})
	scope.catalog = changes
	scope.catalogAuthority = &catalogAuthority{session: scope.op.Session, authorization: stage.authRevision, roles: stage.roleRevision, ref: ids.ObjectRef{Address: catalog.CurrentDatabaseAddress}, descriptor: stage.databasePredecessor, databaseAuthority: stage.databaseAuthority, databasePredecessor: stage.databasePredecessor}
	e.mu.Unlock()
	return Result{}, nil
}

func (e *Engine) stageExtensionLocked(scope *StatementScope, s *parser.CreateExtension) (*CatalogStage, error) {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	staged, err := stageExtensionCreate(&extensionHost{engine: e, op: scope.op, auth: auth}, auth, auth.View, s)
	if err != nil || staged == nil {
		return nil, err
	}
	return stageOf(staged)
}

// stageExtensionCreate stages CREATE EXTENSION: the existence and option
// checks, the version path, the target namespace, the trust and CREATE
// checks, then the extension row, the provider's base members and the update
// actions along the path. It returns nil without error for a skipped IF NOT
// EXISTS.
func stageExtensionCreate(host extension.Host, auth extension.Authority, view access.View, s *parser.CreateExtension) (extension.Stage, error) {
	key := ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}
	if view.Catalog.NameAddress(key) != (ids.ObjectAddress{}) {
		if s.IfNotExists {
			host.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: fmt.Sprintf(`extension "%s" already exists, skipping`, s.Name.Text)})
			return nil, nil
		}
		return nil, sqlError("42710", fmt.Sprintf(`extension "%s" already exists`, s.Name.Text))
	}
	implementation, ok := view.Owner.Extensions[s.Name.Text]
	installer, installable := implementation.(extension.Installer)
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
	if err := extension.VersionError(version); err != nil {
		return nil, err
	}
	path := implementation.Versions().InstallPath(version)
	if path == nil {
		return nil, sqlError("22023", fmt.Sprintf(`extension "%s" has no installation script nor update path for version "%s"`, s.Name.Text, version))
	}
	var namespace ids.ObjectAddress
	if schema != nil {
		namespace = view.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema.Text})
		if namespace == (ids.ObjectAddress{}) {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, schema.Text))
		}
	} else {
		path, pathErr := host.CreationPath()
		if pathErr != nil {
			return nil, pathErr
		}
		if len(path) == 0 {
			return nil, sqlError("3F000", "no schema has been selected to create in")
		}
		namespace = path[0]
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	if !actor.Superuser && !host.ReplayingTableDestination() {
		if !registration.Trusted {
			return nil, sqlError("42501", fmt.Sprintf(`permission denied to create extension "%s"`, s.Name.Text))
		}
		rights, err := auth.Effective(view.Owner.Domain.Ref(view.Catalog.Epoch(), catalog.CurrentDatabaseAddress), false)
		if err != nil {
			return nil, err
		}
		if rights&authorization.CreatePrivilege == 0 {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to create extension "%s"`, s.Name.Text), Hint: "Must have CREATE privilege on current database to create this extension."}
		}
	}
	memberOwner := actor.Ref
	if registration.Superuser && !actor.Superuser {
		memberOwner = host.BootstrapSuperuser()
	}
	oid, err := host.AllocateOID()
	if err != nil {
		return nil, err
	}
	rev, err := host.Revision()
	if err != nil {
		return nil, err
	}
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectRevision: rev, ObjectName: s.Name.Text, Version: path[0], TargetNamespace: namespace, Owner: actor.Ref}
	stage := host.BeginStage(namespace)
	if err := stage.Create(ext); err != nil {
		return nil, err
	}
	if err := installer.Stage(stage, ext, memberOwner); err != nil {
		return nil, err
	}
	if err := extension.StagePath(host.Context(), stage, implementation, ext, memberOwner, path); err != nil {
		return nil, err
	}
	return stage, nil
}
