package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// fileFDWPrivileges captures only immutable Boolean facts under Engine.Mu.
// It opens no SQL journal/frame and releases the lock before pure COPY parsing.
// Publication uses the statement's existing role/catalog/procedure fences.
func (s *StatementScope) fileFDWPrivileges(d *catalog.Procedure, kind catalog.ForeignObjectKind) (fdw.FilePrivileges, error) {
	var privileges fdw.FilePrivileges
	if err := s.executionContext().Err(); err != nil { return privileges, err }
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil { return privileges, err }
	current, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, d)
	if err != nil { return privileges, err }
	if !extension.StockFileFDW.SupportsValidator(current) { return privileges, fmt.Errorf("stuntgres: invalid file_fdw validator authority") }
	if err := extension.StockFileFDW.ValidateProcedure(auth.Catalog, current); err != nil { return privileges, err }
	if err := s.CaptureIdentityLocked(auth); err != nil { return privileges, err }
	if kind != catalog.ForeignTable { return privileges, nil }
	if s.CatalogAuthority == nil {
		// Membership facts authorize the eventual catalog publication, not just
		// this pure parse. Retain the first snapshot through the ordinary Finish
		// fence without opening a SQL frame or allocating a catalog journal.
		s.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	}
	for _, entry := range []struct { name string; allowed *bool }{{"pg_read_server_files", &privileges.ReadFiles}, {"pg_execute_server_program", &privileges.ExecutePrograms}} {
		role, ok := auth.Roles.Lookup(entry.name)
		if !ok { return privileges, fmt.Errorf("stuntgres: missing %s role", entry.name) }
		*entry.allowed, err = auth.Roles.InheritsRole(auth.State.CurrentUser, role.Ref)
		if err != nil { return privileges, err }
	}
	return privileges, nil
}

func (h *contribHost) FileFDWValidate(n plan.FileFDWExpr, values [2]sqltype.Value) error {
	s := h.host().scope()
	d, ok := n.RoutineReference.(*catalog.Procedure)
	if !ok { return fmt.Errorf("stuntgres: missing file_fdw procedure") }
	kind, err := values[1].OID()
	if err != nil { return err }
	privileges, err := s.fileFDWPrivileges(d, catalog.ForeignObjectKind(kind))
	if err != nil { return err }
	return fdw.ValidateFileOptionArray(s.executionContext(), catalog.ForeignObjectKind(kind), values[0], privileges)
}
