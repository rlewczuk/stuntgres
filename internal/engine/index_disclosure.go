package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/sqltype"
)

// disclosureEnv is auth as the environment of index validation: nil for a
// nil view or one without an engine, both of which are trusted publication.
func disclosureEnv(auth *AuthorizationView) indexDisclosureEnv {
	if auth == nil || auth.engine == nil {
		return nil
	}
	return auth
}

// TypeDiagnosticNames implements indexDisclosureEnv.
func (v *AuthorizationView) TypeDiagnosticNames(view access.View) (func(sqltype.ID) string, error) {
	named := *v
	named.View = view
	return v.engine.captureTypeDiagnosticName(named)
}
