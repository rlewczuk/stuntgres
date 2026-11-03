package extension

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// StageCreate installs the complete prerequisite closure into one private stage.
// Existing top-level installations retain IF NOT EXISTS precedence; recursive
// installations select their own default versions and never publish a prefix.
func StageCreate(host Host, auth ddl.Authority, view access.View, s *parser.CreateExtension) (Stage, error) {
	key := ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}
	if view.Catalog.NameAddress(key) != (ids.ObjectAddress{}) {
		if s.IfNotExists {
			host.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: fmt.Sprintf(`extension "%s" already exists, skipping`, s.Name.Text)})
			return nil, nil
		}
		return nil, sqlError("42710", fmt.Sprintf(`extension "%s" already exists`, s.Name.Text))
	}
	implementation, installer, err := installationProvider(view, s.Name.Text)
	if err != nil {
		return nil, err
	}
	control := implementation.Control()
	version := control.Version
	installation := statementInstaller{host: host, auth: auth, view: view}
	var seen uint8
	for _, option := range s.Options {
		var bit uint8
		switch option.Name {
		case "version":
			bit = 1
		case "schema":
			bit = 2
		case "cascade":
			bit = 4
		default:
			return nil, fmt.Errorf("stuntgres: invalid extension option")
		}
		if seen&bit != 0 {
			return nil, expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
		}
		seen |= bit
		switch option.Name {
		case "version":
			version = option.Value.Text
		case "schema":
			installation.schema = option.Value.Text
			installation.explicitSchema = true
		case "cascade":
			installation.cascade = true
		}
	}
	if err := installation.install(s.Name.Text, implementation, installer, control, version); err != nil {
		return nil, err
	}
	return installation.stage, nil
}

type statementInstaller struct {
	host                    Host
	auth                    ddl.Authority
	view                    access.View
	stage                   Stage
	schema                  string
	explicitSchema, cascade bool
	active                  []string
}

func installationProvider(view access.View, name string) (catalog.ExtensionImplementation, Installer, error) {
	implementation, ok := view.Owner.Extensions[name]
	installer, installable := implementation.(Installer)
	if !ok || !installable {
		return nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`extension "%s" is not available`, name)}
	}
	return implementation, installer, nil
}

func (i *statementInstaller) namespace(name string, control catalog.ExtensionControl) (ids.ObjectAddress, ids.ObjectAddress, error) {
	var explicit ids.ObjectAddress
	if i.explicitSchema {
		explicit = i.view.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: i.schema})
		if explicit == (ids.ObjectAddress{}) {
			return explicit, explicit, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, i.schema))
		}
	}
	if control.Schema != nil {
		if i.explicitSchema && i.schema != *control.Schema && !i.cascade {
			return ids.ObjectAddress{}, explicit, sqlError("0A000", fmt.Sprintf("extension %q must be installed in schema %q", name, *control.Schema))
		}
		namespace := i.view.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: *control.Schema})
		if namespace == (ids.ObjectAddress{}) {
			return namespace, explicit, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, *control.Schema))
		}
		return namespace, explicit, nil
	}
	if i.explicitSchema {
		return explicit, explicit, nil
	}
	path, err := i.host.CreationPath()
	if err != nil {
		return ids.ObjectAddress{}, explicit, err
	}
	if len(path) == 0 {
		return ids.ObjectAddress{}, explicit, sqlError("3F000", "no schema has been selected to create in")
	}
	return path[0], explicit, nil
}

func (i *statementInstaller) install(name string, implementation catalog.ExtensionImplementation, installer Installer, control catalog.ExtensionControl, version string) error {
	if err := i.host.Context().Err(); err != nil {
		return err
	}
	if err := VersionError(version); err != nil {
		return err
	}
	path := implementation.Versions().InstallPath(version)
	if path == nil {
		return sqlError("22023", fmt.Sprintf(`extension "%s" has no installation script nor update path for version "%s"`, name, version))
	}
	namespace, explicit, err := i.namespace(name, control)
	if err != nil {
		return err
	}
	// Missing prerequisites without CASCADE do not need a candidate at all.
	if !i.cascade && i.stage == nil {
		for _, required := range control.Requires {
			if i.view.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: required}) == (ids.ObjectAddress{}) {
				return missingPrerequisite(required)
			}
		}
	}
	if i.stage == nil {
		i.stage = i.host.BeginStage(namespace)
	}
	i.stage.ObserveNamespace(namespace)
	if explicit != (ids.ObjectAddress{}) {
		i.stage.ObserveNamespace(explicit)
	}
	i.active = append(i.active, name)
	defer func() { i.active = i.active[:len(i.active)-1] }()
	for _, required := range control.Requires {
		if err := i.host.Context().Err(); err != nil {
			return err
		}
		for _, active := range i.active {
			if required == active {
				return sqlError("42P19", fmt.Sprintf(`cyclic dependency detected between extensions "%s" and "%s"`, required, name))
			}
		}
		address := i.stage.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: required})
		// Get reads staged descriptors only; incumbents (including earlier
		// transaction-private installations) live in the coherent base overlay.
		if ext, ok := i.stage.CurrentRead().Extension(address); ok {
			i.stage.ObservePrerequisite(address)
			i.stage.ObserveNamespace(ext.TargetNamespace)
			continue
		}
		if !i.cascade {
			return missingPrerequisite(required)
		}
		dependency, dependencyInstaller, err := installationProvider(i.view, required)
		if err != nil {
			return err
		}
		i.host.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`installing required extension "%s"`, required)})
		dependencyControl := dependency.Control()
		if err := i.install(required, dependency, dependencyInstaller, dependencyControl, dependencyControl.Version); err != nil {
			return err
		}
	}
	actor, err := i.view.Roles.Descriptor(i.view.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser && !i.host.ReplayingTableDestination() {
		if !control.Trusted {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to create extension "%s"`, name), Hint: "Must be superuser to create this extension."}
		}
		rights, err := i.view.Effective(i.auth, i.view.Owner.Domain.Ref(i.view.Catalog.Epoch(), i.view.Owner.DatabaseAddress()), false)
		if err != nil {
			return err
		}
		if rights&authorization.CreatePrivilege == 0 {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to create extension "%s"`, name), Hint: "Must have CREATE privilege on current database to create this extension."}
		}
	}
	memberOwner := actor.Ref
	if control.Superuser && !actor.Superuser {
		memberOwner = i.host.BootstrapSuperuser()
	}
	oid, err := i.host.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := i.host.Revision()
	if err != nil {
		return err
	}
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectRevision: revision, ObjectName: name, Version: path[0], TargetNamespace: namespace, Owner: actor.Ref}
	if control.Comment != nil {
		ext.Comment = *control.Comment
	}
	if err := i.stage.Create(ext); err != nil {
		return err
	}
	if err := installer.Stage(i.stage, ext, memberOwner); err != nil {
		return err
	}
	if err := StagePath(i.host.Context(), i.stage, implementation, ext, memberOwner, path); err != nil {
		return err
	}
	if err := SealInstallation(i.stage, ext, ext.Members); err != nil {
		return err
	}
	if err := i.stage.Replace(ext); err != nil {
		return err
	}
	if validator, ok := implementation.(interface {
		validateRecipe(catalog.Read, *catalog.Extension) error
	}); ok {
		if err := validator.validateRecipe(i.stage.CurrentRead(), ext); err != nil {
			return err
		}
	}
	if err := StageConsumers(i.host.Context(), i.stage, implementation, ext, memberOwner); err != nil {
		return err
	}
	return AdmitInstallationCompanions(i.stage, ext)
}

func missingPrerequisite(name string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf("required extension %q is not installed", name), Hint: "Use CREATE EXTENSION ... CASCADE to install required extensions too."}
}
