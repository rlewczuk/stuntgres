package engine

import (
	"context"
	"fmt"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// statementHost is the root package's implementation of every consumer host
// interface: exec.Host now, the analyze and feature hosts later (§7 decision
// 8). It is a defined type over StatementScope, so (*statementHost)(scope) is
// a free conversion and lock contracts stay with the scope. Host methods do
// not widen the public StatementScope facade.
type statementHost StatementScope

var _ exec.Host = (*statementHost)(nil)
var _ regobj.NoticeHost = (*statementHost)(nil)
var _ fdw.WriteRuntime = (*statementHost)(nil)

// HostOf returns an untyped nil for a nil scope, never a typed nil.
func HostOf(scope *StatementScope) exec.Host {
	if scope == nil {
		return nil
	}
	return (*statementHost)(scope)
}

// ScopeOf is the checked conversion back for engine code that reads the scope
// behind a runtime record's host.
func ScopeOf(host exec.Host) *StatementScope {
	h, _ := host.(*statementHost)
	return (*StatementScope)(h)
}

func (h *statementHost) scope() *StatementScope { return (*StatementScope)(h) }

func (h *statementHost) Context() context.Context { return h.Op.Context() }
func (h *statementHost) Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment) {
	return h.Input, h.Output
}

// RoutineAdmission is the statement's routine admission authority and its
// original dependency manifest; it fails once the statement has finished.
func (h *statementHost) RoutineAdmission() (exec.RoutineAccess, []catalog.ExpressionDependency, error) {
	if h.Finished {
		return exec.RoutineAccess{}, nil, fmt.Errorf("stuntgres: ended routine admission authority")
	}
	return h.RoutineAuth.routineAccess(), h.RoutineDependencies, nil
}

func (h *statementHost) StatementStart() time.Time          { return h.statementStart }
func (h *statementHost) CTEInvocation() *exec.CTEInvocation { return h.cteInvocation }

// QueryLocks is the statement's row-lock context, read at recheck time.
func (h *statementHost) QueryLocks() *exec.LockContext { return h.QueryLockContext }

func (h *statementHost) ReleaseReplacementRead() { h.scope().foreignUnlocked() }
func (h *statementHost) EnlistForeign(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error) {
	return h.scope().enlistForeign(ctx, wrapper, request, writing)
}

// Detached/internal evaluation can borrow a host without command admission.
// It has no admitted async work; never consult live settings as a fallback.
func (h *statementHost) AsyncAppendEnabled() bool {
	return h != nil && h.Op != nil && h.Op.CommandAdmission && h.Op.AsyncAppend
}

// TrackForeignModification records m so statement end finishes it.
func (h *statementHost) TrackForeignModification(m *fdw.Modification) {
	s := h.scope()
	s.foreignTables().modifications = append(s.foreignTablesOrZero().modifications, m)
}

// foreignWrites is the statement's foreign write runtime.
func (s *StatementScope) foreignWrites() fdw.WriteRuntime { return (*statementHost)(s) }

func (h *statementHost) FormatRegclass(id uint32) (string, error) {
	return h.scope().FormatRegclass(id)
}

func (h *statementHost) FormatRegtype(id uint32) (string, error) {
	return h.scope().FormatRegtype(id)
}

func (h *statementHost) FormatRegproc(id uint32) (string, error) {
	return h.scope().FormatRegproc(id)
}

func (h *statementHost) FormatRegprocedure(id uint32) (string, error) {
	return h.scope().FormatRegprocedure(id)
}

func (h *statementHost) FormatRegrole(id uint32) (string, error) {
	return h.scope().FormatRegrole(id)
}

func (h *statementHost) FormatRegoper(id uint32) (string, error) {
	return h.scope().FormatRegoper(id)
}

func (h *statementHost) FormatRegoperator(id uint32) (string, error) {
	return h.scope().FormatRegoperator(id)
}

func (h *statementHost) FormatRegcollation(id uint32) (string, error) {
	return h.scope().FormatRegcollation(id)
}

func (h *statementHost) FormatRegnamespace(id uint32) (string, error) {
	return h.scope().FormatRegnamespace(id)
}

func (h *statementHost) FormatRegconfig(id uint32) (string, error) {
	return h.scope().FormatRegconfig(id)
}

func (h *statementHost) FormatRegdictionary(id uint32) (string, error) {
	return h.scope().FormatRegdictionary(id)
}

func (h *statementHost) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	return h.scope().FormatACLRole(authority, id)
}

// TypeNameNotice implements regobj.NoticeHost: a type-name notice goes to the
// operation of the current execution, if there is one.
func (h *statementHost) TypeNameNotice(level session.NoticeLevel, notice sqlstate.Error) {
	if h == nil || h.Op == nil {
		return
	}
	h.Op.EmitNotice(level, notice)
}

// PgcryptoNotice delivers owned diagnostics through the live operation filter.
func (h *statementHost) PgcryptoNotice(notice sqlstate.Error) {
	if h == nil || h.Op == nil {
		return
	}
	h.Op.EmitNotice(session.LevelNotice, notice)
}

func (h *statementHost) JSONPathNotice(notice sqlstate.Error) {
	if h == nil || h.Op == nil {
		return
	}
	h.Op.EmitNotice(session.LevelWarning, notice)
}
