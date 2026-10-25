package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Called only by the existing binder against its current coherent catalog.
func (e *Engine) ResolveType(raw parser.TypeName, op *session.Operation) (sqltype.Usage, error) {
	u, err := e.resolveAnnotationType(raw, op, false)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if physical := u.Type.BaseType(); physical.IsArray() && !typedef.StoredArrayElement(physical.Element()) {
		return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
	}
	if u.Type.IsArray() && u.Type.Element().IsDomain() {
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if err := auth.CanUse(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})); err != nil {
			return sqltype.Usage{}, err
		}
	}
	return u, nil
}

// Query-derived schemas can store builtin CHAR without admitting raw declarations.
// Only the isolated Restore caller reconstructs those private declarations;
// transaction identifiers and regclass use ordinary public type admission.
func (e *Engine) resolveTableColumnType(raw parser.TypeName, op *session.Operation) (sqltype.Usage, error) {
	u, err := e.ResolveType(raw, op)
	privateScalar := sqltype.ID(0)
	if err != nil && len(raw.Modifiers) == 0 && len(raw.ArrayBounds) == 0 && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
		if raw.Name == "char" && raw.Quoted {
			privateScalar = sqltype.Char
		}
	}
	if privateScalar == 0 {
		return u, err
	}
	if op == nil || !OperationReplayOrZero(op).TableDestination {
		return u, err
	}
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" {
		return u, err
	}
	auth, authErr := e.AuthorizationLocked(op)
	if authErr != nil {
		return sqltype.Usage{}, authErr
	}
	bootstrap, authErr := auth.Roles.Descriptor(auth.State.Authenticated)
	if authErr != nil || !bootstrap.Superuser || e.ImportTargets == nil {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: invalid trusted restore column authority")
	}
	typ, ok := auth.Catalog.LookupType(privateScalar)
	if !ok || !typ.Concrete() || typ.IsUserDefined() {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing builtin restore column type")
	}
	return typ.Usage(), nil
}

func (e *Engine) resolveAnnotationType(raw parser.TypeName, op *session.Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.ResolveAnnotationTypeMode(raw, op, unknownNull, false)
}

func (e *Engine) ResolveAnnotationTypeMode(raw parser.TypeName, op *session.Operation, unknownNull, domainBase bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, domainBase, false, false, false)
}

// String roles admit internal CHAR without admitting it as a column or a
// general cast target. Lookup, permissions and modifier checks stay type-owned.
func (e *Engine) resolveStringAnnotationType(raw parser.TypeName, op *session.Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, false, true, false, false)
}

// resolveAnnotationTypeInput authorizes and runs the namespace lookup, then
// resolves the name there (analyze.ResolveTypeName). Typmod text reads the
// operation's context and a typmod warning becomes an operation notice.
func (e *Engine) resolveAnnotationTypeInput(raw parser.TypeName, op *session.Operation, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error) {
	name := raw.Name
	if !raw.Quoted && raw.Schema == nil {
		name = typedef.CanonicalDeclarationTypeName(name)
	}
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	object := parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}
	if raw.Schema != nil {
		object.Parts = []parser.Identifier{*raw.Schema, {Text: name, Pos: raw.Pos}}
	}
	path, err := e.LookupNamespaces(auth, object, analyze.TypeLookup)
	if err != nil {
		return sqltype.Usage{}, err
	}
	in := analyze.TypeNameInput{
		Catalog: auth.Catalog, Definition: auth.Definition,
		UnknownNull: unknownNull, DomainBase: domainBase, StringInput: stringInput,
		JSONReturning: jsonReturning, ThresholdArrayInput: thresholdArrayInput,
	}
	var operation analyze.TypeNameOperation
	if op != nil {
		operation.Context = op.Context
		operation.Warn = func(warning sqlstate.Error) { op.EmitNotice(session.LevelWarning, warning) }
	}
	return analyze.ResolveTypeName(raw, name, object, path, in, operation)
}
