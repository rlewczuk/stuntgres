package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Input authority is captured beside the existing regclass authority, not a
// separate registry. The catalog generation and effective type path are immutable.
type capturedRegtypeInput struct {
	auth      AuthorizationView
	path      []ObjectAddress
	database  string
	temporary ObjectAddress
	token     SessionToken
	op        *Operation
	authority *queryAuthority
	position  parser.Position
	portal    string
	qualified *[]ObjectAddress
	resolved  *[]expressionDependency
}

func (e *Engine) captureRegtypeInput(auth AuthorizationView, op *Operation) (capturedRegtypeInput, error) {
	path, err := e.lookupPath(auth, typeLookup)
	if err != nil {
		return capturedRegtypeInput{}, err
	}
	r := capturedRegtypeInput{auth: auth, path: path, temporary: temporaryNamespace(auth), op: op}
	if auth.session != nil {
		r.database = auth.session.databaseName
		r.token = auth.session.token
	}
	r.auth.session = nil
	r.auth.settings = nil
	return r, nil
}
func (r capturedRegtypeInput) ResolveRegtype(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if text == "-" {
		return 0, nil
	}
	numeric := len(text) != 0
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			numeric = false
			break
		}
	}
	if numeric {
		oid, _ := r.auth.definitionType(sqltype.Oid)
		value, err := oid.Input(ctx, text, -1)
		if err != nil {
			return 0, err
		}
		return value.OID()
	}
	return r.resolveTypeName(ctx, text)
}

// resolveTypeName resolves decoded SQL type syntax, without the numeric OID and
// dash shortcuts accepted by whole REGTYPE inputs.
func (r capturedRegtypeInput) resolveTypeName(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	op := r.op
	if r.authority != nil {
		op = nil
		if r.authority.scope != nil {
			op = r.authority.scope.op
		}
	}
	raw, err := parser.ParseTypeName(ctx, text, func(notice sqlstate.Error) {
		if op != nil {
			if r.portal != "" {
				notice.Context += "\n" + r.portal
			}
			op.emitNotice(levelNotice, notice)
		}
	})
	if err != nil {
		return 0, err
	}
	return r.resolveParsedTypeName(ctx, raw, op)
}

// Object-address lists and SQL routine declarations already have positioned
// type syntax. Reuse captured identity lookup without reparsing text or
// admitting a use provider.
func (r capturedRegtypeInput) resolveParsedTypeName(ctx context.Context, raw parser.TypeName, op *Operation) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !raw.Quoted && raw.Schema == nil {
		canonical := canonicalDeclarationTypeName(raw.Name)
		if canonical != raw.Name {
			raw.Name, raw.System, raw.Parts = canonical, true, nil
		}
	}
	parts := raw.Parts
	if len(parts) == 0 {
		if raw.Schema != nil {
			parts = append(parts, *raw.Schema)
		}
		parts = append(parts, parser.Identifier{Text: raw.Name})
	}
	originalParts := parts
	spelling := func() string {
		names := make([]string, len(originalParts))
		for i, p := range originalParts {
			names[i] = p.Text
		}
		return strings.Join(names, ".")
	}
	if len(parts) > 3 {
		return 0, sqlError("42601", "improper qualified name (too many dotted names): "+spelling())
	}
	if len(parts) == 3 {
		if parts[0].Text != r.database {
			return 0, sqlError("0A000", "cross-database references are not implemented: "+spelling())
		}
		parts = parts[1:]
	}
	name := parts[len(parts)-1].Text
	path := namespaceSelection{path: r.path}
	if raw.System {
		path = namespaceSelection{qualified: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}}
	} else if len(parts) == 2 {
		ns := r.auth.definitionLookup(NameKey{ClassID: NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			ns = r.temporary
		}
		if ns == (ObjectAddress{}) {
			return 0, sqlError("3F000", fmt.Sprintf("schema \"%s\" does not exist", parts[0].Text))
		}
		namespace, _ := r.auth.definitionNamespace(ns)
		if namespace.temporary != (SessionToken{}) && namespace.temporary != r.token {
			return 0, sqlError("42501", "permission denied for schema "+namespace.name)
		}
		if err := r.auth.CanUse(ObjectRef{owner: r.auth.engine.domain, Epoch: r.auth.catalog.Epoch(), Address: ns}); err != nil {
			return 0, err
		}
		if r.qualified != nil && !slices.Contains(*r.qualified, ns) {
			*r.qualified = append(*r.qualified, ns)
		}
		path = namespaceSelection{qualified: ns}
	}
	var typ *sqltype.Descriptor
	for i := range path.count() {
		if address := r.auth.definitionLookup(NameKey{ClassID: TypeClassID, Namespace: path.at(i), Name: name}); address != (ObjectAddress{}) {
			typ, _ = r.auth.definitionType(sqltype.ID(address.ObjectID))
			break
		}
	}
	parsedName := func() string {
		name := ""
		if raw.System {
			name = "pg_catalog." + raw.Name
		} else {
			name = spelling()
		}
		if len(raw.ArrayBounds) > 0 {
			name += "[]"
		}
		return name
	}
	if typ != nil && len(raw.ArrayBounds) > 0 {
		typ, _ = r.auth.definitionArray(typ)
	}
	if typ == nil {
		return 0, sqlError("42704", fmt.Sprintf("type \"%s\" does not exist", parsedName()))
	}
	if len(raw.Modifiers) > 0 {
		provider := typ
		if provider.IsArray() {
			provider = provider.Element()
		}
		switch provider.ID() {
		case sqltype.BPChar, sqltype.VarChar, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.Timetz, sqltype.Bit, sqltype.VarBit, sqltype.Numeric:
		default:
			return 0, sqlError("42601", fmt.Sprintf("type modifier is not allowed for type \"%s\"", parsedName()))
		}
		modifiers := make([]int32, len(raw.Modifiers))
		for i, m := range raw.Modifiers {
			if m.NonSimple || m.UnaryPlus {
				return 0, sqlError("42601", "type modifiers must be simple constants or identifiers")
			}
			if m.Integer != nil {
				modifiers[i] = *m.Integer
				continue
			}
			integer, _ := r.auth.definitionType(sqltype.Int4)
			v, err := integer.Input(ctx, m.Raw, -1)
			if err != nil {
				return 0, err
			}
			n, _ := v.Integer()
			modifiers[i] = int32(n)
		}
		switch provider.ID() {
		case sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz:
			if len(modifiers) != 1 {
				return 0, sqlError("22023", "invalid type modifier")
			}
		}
		_, warning, err := provider.ResolveModifiers(modifiers...)
		if err != nil {
			return 0, err
		}
		if warning != nil && op != nil {
			if r.position.Character > 0 {
				warning.Position = strconv.Itoa(r.position.Character)
			}
			warning.Context = r.portal
			op.emitNotice(levelWarning, *warning)
		}
	}
	if r.resolved != nil {
		address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.ID())}
		d, _ := r.auth.definitionDescriptor(address)
		ref := ObjectRef{owner: r.auth.engine.domain, Epoch: r.auth.catalog.Epoch(), Address: address}
		*r.resolved = append(*r.resolved, expressionDependency{ref: ref, revision: d.Revision()})
	}
	return uint32(typ.ID()), nil
}

func regtypeInputError(err error, pos parser.Position) error {
	if d, ok := err.(*sqlstate.Error); ok && pos.Character > 0 {
		copied := *d
		copied.Position = strconv.Itoa(pos.Character)
		return &copied
	}
	return err
}

func (r capturedRegtypeInput) RegtypeParameter(slot int, empty bool) sqltype.RegtypeResolver {
	text := "..."
	if empty {
		text = ""
	}
	r.portal = fmt.Sprintf("unnamed portal parameter $%d = '%s'", slot, text)
	return r
}
