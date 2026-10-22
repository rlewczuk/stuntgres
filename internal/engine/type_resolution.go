package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Called only by the existing binder against its current coherent catalog.
func (e *Engine) resolveType(raw parser.TypeName, op *session.Operation) (sqltype.Usage, error) {
	u, err := e.resolveAnnotationType(raw, op, false)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if physical := u.Type.BaseType(); physical.IsArray() && !typedef.StoredArrayElement(physical.Element()) {
		return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
	}
	if u.Type.IsArray() && u.Type.Element().IsDomain() {
		auth, err := e.authorizationLocked(op)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})); err != nil {
			return sqltype.Usage{}, err
		}
	}
	return u, nil
}

// Query-derived schemas can store builtin CHAR without admitting raw declarations.
// Only the isolated Restore caller reconstructs those private declarations;
// transaction identifiers and regclass use ordinary public type admission.
func (e *Engine) resolveTableColumnType(raw parser.TypeName, op *session.Operation) (sqltype.Usage, error) {
	u, err := e.resolveType(raw, op)
	privateScalar := sqltype.ID(0)
	if err != nil && len(raw.Modifiers) == 0 && len(raw.ArrayBounds) == 0 && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
		if raw.Name == "char" && raw.Quoted {
			privateScalar = sqltype.Char
		}
	}
	if privateScalar == 0 {
		return u, err
	}
	if op == nil || !operationReplayOrZero(op).tableDestination {
		return u, err
	}
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" {
		return u, err
	}
	auth, authErr := e.authorizationLocked(op)
	if authErr != nil {
		return sqltype.Usage{}, authErr
	}
	bootstrap, authErr := auth.Roles.Descriptor(auth.State.Authenticated)
	if authErr != nil || !bootstrap.Superuser || e.importTargets == nil {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: invalid trusted restore column authority")
	}
	typ, ok := auth.Catalog.LookupType(privateScalar)
	if !ok || !typ.Concrete() || typ.IsUserDefined() {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing builtin restore column type")
	}
	return typ.Usage(), nil
}

func (e *Engine) resolveAnnotationType(raw parser.TypeName, op *session.Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeMode(raw, op, unknownNull, false)
}

func (e *Engine) resolveAnnotationTypeMode(raw parser.TypeName, op *session.Operation, unknownNull, domainBase bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, domainBase, false, false, false)
}

// String roles admit internal CHAR without admitting it as a column or a
// general cast target. Lookup, permissions and modifier checks stay type-owned.
func (e *Engine) resolveStringAnnotationType(raw parser.TypeName, op *session.Operation, unknownNull bool) (sqltype.Usage, error) {
	return e.resolveAnnotationTypeInput(raw, op, unknownNull, false, true, false, false)
}

func (e *Engine) resolveAnnotationTypeInput(raw parser.TypeName, op *session.Operation, unknownNull, domainBase, stringInput, jsonReturning, thresholdArrayInput bool) (sqltype.Usage, error) {
	name := raw.Name
	if !raw.Quoted && raw.Schema == nil {
		name = typedef.CanonicalDeclarationTypeName(name)
	}
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	object := parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}
	if raw.Schema != nil {
		object.Parts = []parser.Identifier{*raw.Schema, {Text: name, Pos: raw.Pos}}
	}
	path, err := e.lookupNamespaces(auth, object, analyze.TypeLookup)
	if err != nil {
		return sqltype.Usage{}, err
	}
	var found ids.ObjectAddress
	for i := range path.Count() {
		found = auth.definitionLookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name})
		if found != (ids.ObjectAddress{}) {
			break
		}
	}
	if found == (ids.ObjectAddress{}) {
		if domainBase && raw.Name == "record" && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42804", `"record" is not a valid base type for a domain`)
		}
		if raw.Schema == nil && typedef.UnsupportedColumnType(raw) {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
		}
		return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42704", fmt.Sprintf(`type "%s%s" does not exist`, catalog.ObjectNameText(object), strings.Repeat("[]", len(raw.ArrayBounds))))
	}
	d, validType := auth.definitionType(sqltype.ID(found.ObjectID))
	if !validType {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing definition type provider")
	}
	if jsonReturning && functions.JSONReturningPseudoType(d) {
		return sqltype.Usage{}, sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions")
	}
	if domainBase && (!d.Concrete() || d.IsArray() && d.Element().IsEnum() || d.ID() == sqltype.Void) {
		return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42804", fmt.Sprintf(`"%s" is not a valid base type for a domain`, d.Name()))
	}
	if unknownNull && d.ID() == sqltype.Unknown && len(raw.Modifiers) == 0 {
		return d.Usage(), nil
	}
	if unknownNull && d.ID() == 2249 && len(raw.Modifiers) == 0 && len(raw.ArrayBounds) == 0 {
		return sqltype.DeferredRecord().Usage(), nil
	}
	if !d.Concrete() || d.IsArray() && d.Element().IsEnum() && !thresholdArrayInput || d.ID() == sqltype.Char && !stringInput || d.ID() == sqltype.Void {
		return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
	}
	u := d.Usage()
	if !raw.Quoted && (raw.Name == "char" || raw.Name == "character") && len(raw.Modifiers) == 0 {
		u.Typmod = 5
	}
	if !raw.Quoted && raw.Schema == nil && raw.Name == "bit" && len(raw.Modifiers) == 0 {
		u.Typmod = 1
	}
	if len(raw.Modifiers) > 0 {
		if d.IsUserDefined() || d.IsDomain() {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42601", fmt.Sprintf(`type modifier is not allowed for type "%s"`, catalog.ObjectNameText(object)))
		}
		if err := d.CheckModifierSupport(); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42601" && raw.Schema != nil {
				// Explicit qualification owns its diagnostic spelling. Unqualified
				// aliases and lowered declarations retain the descriptor's name.
				return sqltype.Usage{}, typedef.TypeDiagnostic(raw, diagnostic.Code, fmt.Sprintf(`type modifier is not allowed for type "%s"`, catalog.ObjectNameText(object)))
			}
			return sqltype.Usage{}, analyze.InputErrorAt(err, raw.Pos)
		}
	}
	modifiers := make([]int32, len(raw.Modifiers))
	for i, modifier := range raw.Modifiers {
		if modifier.NonSimple || modifier.UnaryPlus {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42601", "type modifiers must be simple constants or identifiers")
		}
		if modifier.Integer != nil {
			modifiers[i] = *modifier.Integer
			continue
		}
		integer, _ := auth.definitionType(sqltype.Int4)
		ctx := context.Background()
		if op != nil {
			ctx = op.Context()
		}
		value, err := integer.Input(ctx, modifier.Raw, -1)
		if err != nil {
			return sqltype.Usage{}, analyze.InputErrorAt(err, raw.Pos)
		}
		n, err := value.Integer()
		if err != nil {
			return sqltype.Usage{}, err
		}
		modifiers[i] = int32(n)
	}
	if len(modifiers) > 0 {
		var warning *sqlstate.Error
		var err error
		u, warning, err = d.ResolveModifiers(modifiers...)
		if err != nil {
			return sqltype.Usage{}, analyze.InputErrorAt(err, raw.Pos)
		}
		if warning != nil && op != nil {
			if raw.Pos.Character > 0 {
				warning.Position = strconv.Itoa(raw.Pos.Character)
			}
			op.EmitNotice(session.LevelWarning, *warning)
		}
	}
	if len(raw.ArrayBounds) > 0 {
		if sqltype.IsTransactionIdentifier(d.BaseType().ID()) {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
		}
		array, ok := auth.definitionArray(d)
		if !ok || d.IsEnum() && !thresholdArrayInput {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported array element type")
		}
		u.Type = array
	}
	return u, nil
}
