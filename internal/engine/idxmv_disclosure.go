package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// indexDisclosureEnv is the live SQL principal that index validation reports
// duplicate keys to: its access environment, its routine admission authority,
// and the type diagnostic names of a view of it. The validators take it, or
// any access.Environment, next to the access.View it belongs to; a nil
// environment is trusted publication, which has no SQL principal and
// discloses every key. *AuthorizationView implements it.
type indexDisclosureEnv interface {
	access.Environment
	exec.AdmissionAuthority
	// TypeDiagnosticNames captures type diagnostic names as this principal,
	// running with view in place of its own view, names them.
	TypeDiagnosticNames(view access.View) (func(sqltype.ID) string, error)
}
