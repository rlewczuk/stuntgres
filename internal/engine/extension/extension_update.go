package extension

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// StageUpdate stages ALTER EXTENSION UPDATE: the owner check, the
// target version and its update path, the trust and CREATE checks, then the
// replaced extension row and the update actions along the path. It returns
// nil without error when the target version is already installed.
func StageUpdate(host Host, auth ddl.Authority, view access.View, s *parser.AlterExtensionUpdate) (Stage, error) {
	address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text})
	old, exists := view.Catalog.Extension(address)
	if !exists {
		return nil, sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, s.Name.Text))
	}
	if err := view.Check(auth, access.Requirement{Object: view.Owner.Domain.Ref(view.Catalog.Epoch(), address), OwnerOnly: true, OwnerNoun: "extension"}); err != nil {
		return nil, err
	}
	implementation := view.Owner.Extensions[old.Name()]
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
	if err := VersionError(version); err != nil {
		return nil, err
	}
	if old.Version == version {
		host.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`version "%s" of extension "%s" is already installed`, version, old.Name())})
		return nil, nil
	}
	path := implementation.Versions().Path(old.Version, version)
	if path == nil {
		return nil, sqlError("22023", fmt.Sprintf(`extension "%s" has no update path from version "%s" to version "%s"`, old.Name(), old.Version, version))
	}
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	control := implementation.Control()
	if !actor.Superuser {
		if !control.Trusted {
			return nil, sqlError("42501", fmt.Sprintf(`permission denied to update extension "%s"`, old.Name()))
		}
		rights, err := view.Effective(auth, view.Owner.Domain.Ref(view.Catalog.Epoch(), view.Owner.DatabaseAddress()), false)
		if err != nil {
			return nil, err
		}
		if rights&authorization.CreatePrivilege == 0 {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to update extension "%s"`, old.Name()), Hint: "Must have CREATE privilege on current database to update this extension."}
		}
	}
	owner := actor.Ref
	if control.Superuser && !actor.Superuser {
		owner = host.BootstrapSuperuser()
	}
	ext := *old
	// The selected stock transition creates one new member. Reserve that slot
	// while detaching, so the typed action needs no second slice allocation.
	ext.Members = make([]ids.ObjectAddress, len(old.Members), len(old.Members)+1)
	copy(ext.Members, old.Members)
	ext.ObjectRevision, err = host.Revision()
	if err != nil {
		return nil, err
	}
	stage := host.BeginStage(ext.TargetNamespace)
	if err := stage.Replace(&ext); err != nil {
		return nil, err
	}
	if err := StagePath(host.Context(), stage, implementation, &ext, owner, path); err != nil {
		return nil, err
	}
	return stage, nil
}
