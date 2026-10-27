package core

import (
	"fmt"

	"stuntgres/internal/engine/functions"
)

// GenericFileList uses the executing expression principal, not the preparing
// session. Lock order is Engine -> Settings -> Session, matching setting reads.
func (h *statementHost) GenericFileList(kind functions.GenericFileListKind, dirname string) error {
	if kind.Name() == "" {
		return fmt.Errorf("stuntgres: invalid generic file listing kind")
	}
	if kind != functions.GenericFileDir {
		return functions.GenericFileListUnsupported(kind)
	}
	if err := h.checkGenericFilePath(dirname); err != nil {
		return err
	}
	return functions.GenericFileListUnsupported(kind)
}

// checkGenericFilePath borrows current authority in Engine -> Settings -> Session order.
func (h *statementHost) checkGenericFilePath(dirname string) error {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	role, ok := auth.Roles.Lookup("pg_read_server_files")
	if !ok {
		return fmt.Errorf("stuntgres: missing pg_read_server_files role")
	}
	privileged, err := auth.Roles.InheritsRole(auth.State.CurrentUser, role.Ref)
	if err != nil {
		return err
	}
	owner := s.Op.EffectiveSettings()
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := owner.AdmitLocked(DomainOf(e), s.Op); err != nil {
		return err
	}
	if _, err := functions.CheckGenericFileListPath(dirname, owner.Value("data_directory"), owner.Value("log_directory"), privileged); err != nil {
		return err
	}
	return nil
}
