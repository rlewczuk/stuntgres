package core

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Engine.Mu is held; all selection precedes Settings.Mu -> Session.Mu. No
// converter or external callback runs here, and only copied capabilities escape.
func (e *Engine) prepareClientEncodingLocked(op *session.Operation, id int) (sqltype.ClientEncodingPolicy, error) {
	if id == 0 || id == 6 {
		return sqltype.NewClientEncodingPolicy(id, sqltype.EncodingConversionHandler{}, sqltype.EncodingConversionHandler{})
	}
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return sqltype.ClientEncodingPolicy{}, err
	}
	explicit, _, err := ExplicitPath(auth)
	if err != nil {
		return sqltype.ClientEncodingPolicy{}, err
	}
	path := EffectivePath(auth, explicit, analyze.ConversionLookup)
	incoming, err := resolveConversionDefault(auth.Catalog, path, id, 6)
	if err != nil {
		return sqltype.ClientEncodingPolicy{}, err
	}
	outgoing, err := resolveConversionDefault(auth.Catalog, path, 6, id)
	if err != nil {
		return sqltype.ClientEncodingPolicy{}, err
	}
	var in, out sqltype.EncodingConversionHandler
	if incoming.procedure != nil {
		in = incoming.procedure.Conversion
	}
	if outgoing.procedure != nil {
		out = outgoing.procedure.Conversion
	}
	policy, err := sqltype.NewClientEncodingPolicy(id, in, out)
	if err != nil {
		return policy, err
	}
	return policy.WithDefaultIdentity(uint64(incoming.conversion.OID), uint64(outgoing.conversion.OID), uint64(auth.Catalog.Epoch())), nil
}
func clientEncodingSettingError(id int) error {
	name := sqltype.EncodingName(id)
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`invalid value for parameter "client_encoding": "%s"`, name), Detail: "Conversion between " + name + " and UTF8 is not supported."}
}
