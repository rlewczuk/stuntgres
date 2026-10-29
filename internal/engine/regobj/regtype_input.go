package regobj

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Input authority is captured beside the existing regclass authority, not a
// separate registry. The catalog generation and effective type path are immutable.
type TypeInput struct {
	capture   InputCapture
	op        *session.Operation
	authority *exec.Authority
	position  parser.Position
	portal    string
	qualified *[]ids.ObjectAddress
	resolved  *[]catalog.ExpressionDependency
}

// NewTypeInput returns the regtype resolver over c. Without an execution
// authority its notices go to op.
func NewTypeInput(c InputCapture, op *session.Operation) TypeInput {
	return TypeInput{capture: c, op: op}
}

// Authority returns the captured read authority.
func (r TypeInput) Authority() Authority { return r.capture.Authority }

// Path returns the captured type lookup path.
func (r TypeInput) Path() []ids.ObjectAddress { return r.capture.Path }

func (r TypeInput) ResolveRegtype(ctx context.Context, text string) (uint32, error) {
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
		a := r.capture.Authority
		oid, _ := catalog.DefinitionTypeAt(a.View.Catalog, a.Definitions, sqltype.Oid)
		value, err := oid.Input(ctx, text, -1)
		if err != nil {
			return 0, err
		}
		return value.OID()
	}
	return r.resolveTypeName(ctx, text)
}

// typeNotices emits one type-name notice or type-modifier warning.
type typeNotices func(session.NoticeLevel, sqlstate.Error)

// notices returns where this resolver's notices go: the captured operation
// without an execution authority, otherwise the authority's current host if
// it takes notices. The host is read at each call, never at capture.
func (r TypeInput) notices() typeNotices {
	if r.authority == nil {
		return operationNotices(r.op)
	}
	if host, ok := r.authority.Host.(NoticeHost); ok {
		return host.TypeNameNotice
	}
	return nil
}

func operationNotices(op *session.Operation) typeNotices {
	if op == nil {
		return nil
	}
	return op.EmitNotice
}

// resolveTypeName resolves decoded SQL type syntax, without the numeric OID and
// dash shortcuts accepted by whole REGTYPE inputs.
func (r TypeInput) resolveTypeName(ctx context.Context, text string) (uint32, error) {
	usage, err := r.ResolveInputUsage(ctx, text)
	if err != nil {
		return 0, err
	}
	return uint32(usage.Type.ID()), nil
}

// ResolveInputUsage resolves a SQL declaration, preserving its encoded typmod.
// Unlike REGTYPE value input, it admits neither numeric OIDs nor dash.
func (r TypeInput) ResolveInputUsage(ctx context.Context, text string) (sqltype.Usage, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Usage{}, err
	}
	emit := r.notices()
	raw, err := parser.ParseTypeName(ctx, text, func(notice sqlstate.Error) {
		if emit != nil {
			if r.portal != "" {
				notice.Context += "\n" + r.portal
			}
			emit(session.LevelNotice, notice)
		}
	})
	if err != nil {
		return sqltype.Usage{}, err
	}
	return r.resolveParsed(ctx, raw, emit)
}

// Object-address lists and SQL routine declarations already have positioned
// type syntax. Reuse captured identity lookup without reparsing text or
// admitting a use provider. Modifier warnings go to op.
func (r TypeInput) ResolveParsedTypeName(ctx context.Context, raw parser.TypeName, op *session.Operation) (uint32, error) {
	usage, err := r.resolveParsed(ctx, raw, operationNotices(op))
	if err != nil {
		return 0, err
	}
	return uint32(usage.Type.ID()), nil
}

func (r TypeInput) resolveParsed(ctx context.Context, raw parser.TypeName, emit typeNotices) (sqltype.Usage, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Usage{}, err
	}
	a := r.capture.Authority
	if !raw.Quoted && raw.Schema == nil {
		canonical := typedef.CanonicalDeclarationTypeName(raw.Name)
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
		return sqltype.Usage{}, sqlError("42601", "improper qualified name (too many dotted names): "+spelling())
	}
	if len(parts) == 3 {
		if parts[0].Text != r.capture.Database {
			return sqltype.Usage{}, sqlError("0A000", "cross-database references are not implemented: "+spelling())
		}
		parts = parts[1:]
	}
	name := parts[len(parts)-1].Text
	path := analyze.NamespaceSelection{Path: r.capture.Path}
	if raw.System {
		path = analyze.NamespaceSelection{Qualified: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}
	} else if len(parts) == 2 {
		ns := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			ns = r.capture.Temporary
		}
		if ns == (ids.ObjectAddress{}) {
			return sqltype.Usage{}, sqlstate.SoftInputError(sqlError("3F000", fmt.Sprintf("schema \"%s\" does not exist", parts[0].Text)))
		}
		namespace, _ := catalog.DefinitionNamespaceAt(a.View.Catalog, a.Definitions, ns)
		if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != r.capture.Token {
			return sqltype.Usage{}, sqlError("42501", "permission denied for schema "+namespace.Name())
		}
		if err := a.View.CanUse(a.Environment, a.View.Ref(ns)); err != nil {
			return sqltype.Usage{}, err
		}
		if r.qualified != nil && !slices.Contains(*r.qualified, ns) {
			*r.qualified = append(*r.qualified, ns)
		}
		path = analyze.NamespaceSelection{Qualified: ns}
	}
	var typ *sqltype.Descriptor
	for i := range path.Count() {
		if address := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name}); address != (ids.ObjectAddress{}) {
			typ, _ = catalog.DefinitionTypeAt(a.View.Catalog, a.Definitions, sqltype.ID(address.ObjectID))
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
		typ, _ = catalog.DefinitionArrayAt(a.View.Catalog, a.Definitions, typ)
	}
	if typ == nil {
		return sqltype.Usage{}, sqlstate.SoftInputError(sqlError("42704", fmt.Sprintf("type \"%s\" does not exist", parsedName())))
	}
	usage := typ.Usage()
	if len(raw.Modifiers) > 0 {
		provider := typ
		if provider.IsArray() {
			provider = provider.Element()
		}
		switch provider.ID() {
		case sqltype.BPChar, sqltype.VarChar, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.Timetz, sqltype.Bit, sqltype.VarBit, sqltype.Numeric:
		default:
			return sqltype.Usage{}, sqlstate.HardInputError(sqlError("42601", fmt.Sprintf("type modifier is not allowed for type \"%s\"", parsedName())))
		}
		modifiers := make([]int32, len(raw.Modifiers))
		for i, m := range raw.Modifiers {
			if m.NonSimple || m.UnaryPlus {
				return sqltype.Usage{}, sqlstate.HardInputError(sqlError("42601", "type modifiers must be simple constants or identifiers"))
			}
			if m.Integer != nil {
				modifiers[i] = *m.Integer
				continue
			}
			integer, _ := catalog.DefinitionTypeAt(a.View.Catalog, a.Definitions, sqltype.Int4)
			v, err := integer.Input(ctx, m.Raw, -1)
			if err != nil {
				return sqltype.Usage{}, sqlstate.HardInputError(err)
			}
			n, _ := v.Integer()
			modifiers[i] = int32(n)
		}
		switch provider.ID() {
		case sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz:
			if len(modifiers) != 1 {
				return sqltype.Usage{}, sqlstate.HardInputError(sqlError("22023", "invalid type modifier"))
			}
		}
		modified, warning, err := provider.ResolveModifiers(modifiers...)
		if err != nil {
			return sqltype.Usage{}, sqlstate.HardInputError(err)
		}
		usage.Typmod = modified.Typmod
		if warning != nil && emit != nil {
			if r.position.Character > 0 {
				warning.Position = strconv.Itoa(r.position.Character)
			}
			warning.Context = r.portal
			emit(session.LevelWarning, *warning)
		}
	}
	if r.resolved != nil {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
		d, _ := catalog.DescriptorAt(a.View.Catalog, a.Definitions, address)
		ref := a.View.Ref(address)
		*r.resolved = append(*r.resolved, catalog.ExpressionDependency{Ref: ref, Revision: d.Revision()})
	}
	return usage, nil
}

func (r TypeInput) RegtypeParameter(slot int, empty bool) sqltype.RegtypeResolver {
	text := "..."
	if empty {
		text = ""
	}
	r.portal = fmt.Sprintf("unnamed portal parameter $%d = '%s'", slot, text)
	return r
}
