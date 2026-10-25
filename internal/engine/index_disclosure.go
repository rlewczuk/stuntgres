package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

// DisclosureEnv is auth as the environment of index validation: nil for a
// nil view or one the engine did not build, both of which are trusted publication.
func DisclosureEnv(auth *AuthorizationView) index.DisclosureEnv {
	if auth == nil || !auth.live() {
		return nil
	}
	return auth
}

// TypeDiagnosticNames implements index.DisclosureEnv.
func (v *AuthorizationView) TypeDiagnosticNames(view access.View) (func(sqltype.ID) string, error) {
	named := *v
	named.View = view
	return CaptureTypeDiagnosticName(named)
}
