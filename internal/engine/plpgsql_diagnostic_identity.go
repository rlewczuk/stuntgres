package engine

import (
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
)

// CREATE validation and a session's first admitted invocation share the existing
// routine-analysis owner. Diagnostic names are not part of persisted programs.
// The caller holds Engine.mu; path changes alone do not invalidate a signature.
func (e *Engine) retainPLDiagnosticIdentityLocked(op *session.Operation, auth AuthorizationView, d *catalog.Procedure) (string, error) {
	if d.Address().ObjectID == 0 {
		return formatPLDiagnosticIdentity(auth.Catalog, auth.definition, nil, d), nil
	}
	payload := payloadOf(op.Session)
	retained := payload.sqlRoutines[d.Address()]
	if retained.plProcedure == d && retained.plDiagnostic != "" {
		return retained.plDiagnostic, nil
	}
	path, err := referencePath(auth)
	if err != nil { return "", err }
	identity := formatPLDiagnosticIdentity(auth.Catalog, auth.definition, path, d)
	retained.plProcedure, retained.plDiagnostic = d, identity
	if payload.sqlRoutines == nil { payload.sqlRoutines = make(map[ids.ObjectAddress]sqlRoutineAnalysis) }
	payload.sqlRoutines[d.Address()] = retained
	return identity, nil
}

func (s *StatementScope) plDiagnosticIdentityFor(d *catalog.Procedure, read catalog.Read) (string, error) {
	if d.Address().ObjectID == 0 { return formatPLDiagnosticIdentity(read, nil, nil, d), nil }
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if retained := payloadOf(s.op.Session).sqlRoutines[d.Address()]; retained.plProcedure == d && retained.plDiagnostic != "" {
		return retained.plDiagnostic, nil
	}
	auth, err := s.routineAuthorizationLocked()
	if err != nil { return "", err }
	auth.Recapture(read)
	return e.retainPLDiagnosticIdentityLocked(s.op, auth, d)
}

func formatPLDiagnosticIdentity(read catalog.Read, reader catalog.DefinitionReader, path []ids.ObjectAddress, d *catalog.Procedure) string {
	if d.Name() == "inline_code_block" && d.Address().ObjectID == 0 { return "inline_code_block" }
	var text strings.Builder
	if d.Address().ObjectID != 0 && !regobj.NamedObjectVisibleAt(read, reader, path, d) {
		namespace, _ := catalog.DefinitionNamespaceAt(read, reader, d.Namespace())
		text.WriteString(catalog.DependencyIdentifier(namespace.Name()))
		text.WriteByte('.')
	}
	text.WriteString(catalog.DependencyIdentifier(d.Name()))
	text.WriteByte('(')
	regobj.WriteRoutineArguments(&text, read, reader, path, d.Signature, true)
	text.WriteByte(')')
	return text.String()
}
