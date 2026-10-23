package analyze

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// TypeNameInput is what ResolveTypeName reads besides the name and the
// namespaces the root's lookup selected.
type TypeNameInput struct {
	Catalog    catalog.Read
	Definition catalog.DefinitionReader // Cooking-only sparse reader; nil reads Catalog.
	// UnknownNull admits unknown and a deferred record; DomainBase checks a
	// domain base type; StringInput admits internal "char"; JSONReturning
	// rejects SQL/JSON returning pseudo-types; ThresholdArrayInput admits
	// enum arrays.
	UnknownNull, DomainBase, StringInput, JSONReturning, ThresholdArrayInput bool
}

// TypeNameOperation is the operation side of a type name resolution; both
// are nil without an operation. It is a parameter of its own so the callers'
// closures stay on their stack.
type TypeNameOperation struct {
	// Context is the context of a typmod given as text. It is called only for
	// such a modifier, because the operation may take its session mutex; nil
	// means context.Background.
	Context func() context.Context
	// Warn receives a typmod warning with its position; nil drops it.
	Warn func(sqlstate.Error)
}

// ResolveTypeName resolves a type declaration in the selected namespaces:
// the first namespace that has name wins, then the admission checks, typmod
// resolution and array bounds apply. name is the canonical name the lookup
// read and object the name it resolved, for diagnostics.
func ResolveTypeName(raw parser.TypeName, name string, object parser.ObjectName, path NamespaceSelection, in TypeNameInput, op TypeNameOperation) (sqltype.Usage, error) {
	var found ids.ObjectAddress
	for i := range path.Count() {
		found = catalog.DefinitionLookupAt(in.Catalog, in.Definition, ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name})
		if found != (ids.ObjectAddress{}) {
			break
		}
	}
	if found == (ids.ObjectAddress{}) {
		if in.DomainBase && raw.Name == "record" && (raw.Schema == nil || raw.Schema.Text == "pg_catalog") {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42804", `"record" is not a valid base type for a domain`)
		}
		if raw.Schema == nil && typedef.UnsupportedColumnType(raw) {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
		}
		return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42704", fmt.Sprintf(`type "%s%s" does not exist`, catalog.ObjectNameText(object), strings.Repeat("[]", len(raw.ArrayBounds))))
	}
	d, validType := catalog.DefinitionTypeAt(in.Catalog, in.Definition, sqltype.ID(found.ObjectID))
	if !validType {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing definition type provider")
	}
	if in.JSONReturning && functions.JSONReturningPseudoType(d) {
		return sqltype.Usage{}, sqlError("0A000", "returning pseudo-types is not supported in SQL/JSON functions")
	}
	if in.DomainBase && (!d.Concrete() || d.IsArray() && d.Element().IsEnum() || d.ID() == sqltype.Void) {
		return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "42804", fmt.Sprintf(`"%s" is not a valid base type for a domain`, d.Name()))
	}
	if in.UnknownNull && d.ID() == sqltype.Unknown && len(raw.Modifiers) == 0 {
		return d.Usage(), nil
	}
	if in.UnknownNull && d.ID() == 2249 && len(raw.Modifiers) == 0 && len(raw.ArrayBounds) == 0 {
		return sqltype.DeferredRecord().Usage(), nil
	}
	if !d.Concrete() || d.IsArray() && d.Element().IsEnum() && !in.ThresholdArrayInput || d.ID() == sqltype.Char && !in.StringInput || d.ID() == sqltype.Void {
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
			return sqltype.Usage{}, InputErrorAt(err, raw.Pos)
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
		integer, _ := catalog.DefinitionTypeAt(in.Catalog, in.Definition, sqltype.Int4)
		ctx := context.Background()
		if op.Context != nil {
			ctx = op.Context()
		}
		value, err := integer.Input(ctx, modifier.Raw, -1)
		if err != nil {
			return sqltype.Usage{}, InputErrorAt(err, raw.Pos)
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
			return sqltype.Usage{}, InputErrorAt(err, raw.Pos)
		}
		if warning != nil && op.Warn != nil {
			if raw.Pos.Character > 0 {
				warning.Position = strconv.Itoa(raw.Pos.Character)
			}
			op.Warn(*warning)
		}
	}
	if len(raw.ArrayBounds) > 0 {
		if d.BaseType().ID() == sqltype.PgSnapshot {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
		}
		if d.BaseType().ID() == sqltype.Macaddr || d.BaseType().ID() == sqltype.Macaddr8 || sqltype.IsTransactionIdentifier(d.BaseType().ID()) {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported column type")
		}
		array, ok := catalog.DefinitionArrayAt(in.Catalog, in.Definition, d)
		if !ok || d.IsEnum() && !in.ThresholdArrayInput {
			return sqltype.Usage{}, typedef.TypeDiagnostic(raw, "0A000", "unsupported array element type")
		}
		u.Type = array
	}
	return u, nil
}
