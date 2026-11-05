package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) postgresFDWPrivileges(d *catalog.Procedure) (fdw.PostgresPrivileges, error) {
	var privileges fdw.PostgresPrivileges
	if err := s.executionContext().Err(); err != nil {
		return privileges, err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return privileges, err
	}
	current, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, d)
	if err != nil {
		return privileges, err
	}
	if !extension.StockPostgresFDW.SupportsValidator(current) {
		return privileges, fmt.Errorf("stuntgres: invalid postgres_fdw validator authority")
	}
	if err := extension.StockPostgresFDW.ValidateProcedure(auth.Catalog, current); err != nil {
		return privileges, err
	}
	if err := s.CaptureIdentityLocked(auth); err != nil {
		return privileges, err
	}
	if s.CatalogAuthority == nil {
		s.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return privileges, err
	}
	privileges.Superuser = actor.Superuser
	read := auth.Catalog
	privileges.Installed = func(name string) bool {
		return read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}) != (ids.ObjectAddress{})
	}
	privileges.Notice = func(notice sqlstate.Error) { s.Op.EmitNotice(session.LevelWarning, notice) }
	return privileges, nil
}
