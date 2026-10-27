package core

import (
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

func (e *Engine) resolveAnnotationType(raw parser.TypeName, op *session.Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.ResolveAnnotationTypeMode(raw, op, unknownNull, false)
}

func (e *Engine) ResolveAnnotationTypeMode(raw parser.TypeName, op *session.Operation, unknownNull, domainBase bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, domainBase, false, false)
}

// resolveAnnotationTypeInput authorizes and runs the namespace lookup, then
// resolves the name there (analyze.ResolveTypeName). Typmod text reads the
// operation's context and a typmod warning becomes an operation notice.
func (e *Engine) resolveAnnotationTypeInput(raw parser.TypeName, op *session.Operation, unknownNull, domainBase, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error) {
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if err := e.Catalog.ValidateCatalogViewLocked(auth.Catalog); err != nil {
		return sqltype.Usage{}, err
	}
	var operation analyze.TypeNameOperation
	if op != nil {
		operation.Context = op.Context
		operation.Warn = func(warning sqlstate.Error) { op.EmitNotice(session.LevelWarning, warning) }
	}
	return e.resolveAnnotationTypeAuthority(raw, auth, unknownNull, domainBase, jsonReturning, thresholdArrayInput, operation)
}

// resolveAnnotationTypeAuthority is the common resolver for live operation and
// captured native-constructor binders. Both use identical lookup/admission.
func (e *Engine) resolveAnnotationTypeAuthority(raw parser.TypeName, auth AuthorizationView, unknownNull, domainBase, jsonReturning, thresholdArrayInput bool, operation analyze.TypeNameOperation) (sqltype.Usage, error) {
	name := raw.Name
	if !raw.Quoted && raw.Schema == nil {
		name = typedef.CanonicalDeclarationTypeName(name)
	}
	object := parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}
	if raw.Schema != nil {
		object.Parts = []parser.Identifier{*raw.Schema, {Text: name, Pos: raw.Pos}}
	}
	path, err := LookupNamespacesSnapshot(auth, object, analyze.TypeLookup)
	if err != nil {
		return sqltype.Usage{}, err
	}
	in := analyze.TypeNameInput{
		Catalog: auth.Catalog, Definition: auth.Definition,
		UnknownNull: unknownNull, DomainBase: domainBase,
		JSONReturning: jsonReturning, ThresholdArrayInput: thresholdArrayInput,
	}
	return analyze.ResolveTypeName(raw, name, object, path, in, operation)
}
