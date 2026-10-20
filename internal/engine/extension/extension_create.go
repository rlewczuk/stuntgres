package extension

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// StageCreate stages CREATE EXTENSION: the existence and option checks, the
// version path, the target namespace, the trust and CREATE checks, then the
// extension row, the provider's base members and the update actions along the
// path. It returns nil without error for a skipped IF NOT EXISTS.
func StageCreate(host Host, auth Authority, view access.View, s *parser.CreateExtension) (Stage, error) {
	key := ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}
	if view.Catalog.NameAddress(key) != (ids.ObjectAddress{}) {
		if s.IfNotExists {
			host.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: fmt.Sprintf(`extension "%s" already exists, skipping`, s.Name.Text)})
			return nil, nil
		}
		return nil, sqlError("42710", fmt.Sprintf(`extension "%s" already exists`, s.Name.Text))
	}
	implementation, ok := view.Owner.Extensions[s.Name.Text]
	installer, installable := implementation.(Installer)
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
	if err := VersionError(version); err != nil {
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
	if err := StagePath(host.Context(), stage, implementation, ext, memberOwner, path); err != nil {
		return nil, err
	}
	return stage, nil
}
