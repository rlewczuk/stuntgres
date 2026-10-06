package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/sqlstate"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ColumnDescriptor struct {
	Name                       string
	Usage                      sqltype.Usage
	Nullable, NullabilityKnown bool
	OriginTable                uint32
	OriginAttribute            int16
}

type StatementDescription struct {
	Parameters ParameterInfo
	Results    []ColumnDescriptor
	// Immutable scalar captures, never descriptors, execution plans or frames.
	timestamps []timestampConstant
}

type timestampConstant struct {
	offset int
	scalar int64
	typmod int32
}

// Catalog wrappers retain the existing class/namespace/revision/epoch ownership
// seam. A sqltype ID is a projection, not an authorization token.
type typeDescriptor struct{ typ *sqltype.Descriptor }

func (d *typeDescriptor) Address() ObjectAddress {
	return ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.ID())}
}
func (*typeDescriptor) Revision() DefinitionRevision { return 1 }
func (*typeDescriptor) Namespace() ObjectAddress {
	return ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
}
func (d *typeDescriptor) Name() string   { return d.typ.Name() }
func (*typeDescriptor) Kind() ObjectKind { return TypeObject }

type typeProvider struct{}

func (typeProvider) ClassID() OID { return TypeClassID }
func (typeProvider) Validate(view CatalogRead, descriptor Descriptor) error {
	t, ok := descriptor.(*typeDescriptor)
	if !ok || t.typ == nil || t.typ.ID() == 0 {
		return fmt.Errorf("stuntgres: invalid type descriptor")
	}
	ns, ok := view.Descriptor(descriptor.Namespace())
	if !ok || ns.Kind() != NamespaceObject {
		return fmt.Errorf("stuntgres: type namespace is missing")
	}
	found := view.Lookup(NameKey{ClassID: TypeClassID, Namespace: descriptor.Namespace(), Name: descriptor.Name()})
	if len(found) != 1 || found[0] != descriptor.Address() {
		return fmt.Errorf("stuntgres: invalid type identity")
	}
	return nil
}

func (r CatalogRead) LookupType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	d, ok := r.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)})
	if !ok {
		return nil, false
	}
	t, ok := d.(*typeDescriptor)
	if !ok {
		return nil, false
	}
	return t.typ, true
}

// Called only by the existing binder against its current coherent catalog.
func (e *Engine) resolveType(raw parser.TypeName, op *Operation) (sqltype.Usage, error) {
	name := raw.Name
	if !raw.Quoted {
		switch name {
		case "smallint":
			name = "int2"
		case "integer":
			name = "int4"
		case "bigint":
			name = "int8"
		case "boolean":
			name = "bool"
		case "real":
			name = "float4"
		case "float", "double precision":
			name = "float8"
		}
	}
	view := CatalogRead{owner: e, state: e.state}
	found := view.Lookup(NameKey{ClassID: TypeClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}, Name: name})
	if len(found) != 1 {
		if unsupportedColumnType(raw) {
			return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
		}
		return sqltype.Usage{}, typeDiagnostic(raw, "42704", fmt.Sprintf(`type "%s" does not exist`, raw.Name))
	}
	d, _ := view.LookupType(sqltype.ID(found[0].ObjectID))
	if !d.Concrete() || d.ID() == sqltype.Name {
		return sqltype.Usage{}, typeDiagnostic(raw, "0A000", "unsupported column type")
	}
	if raw.UnaryPlus {
		return sqltype.Usage{}, typeDiagnostic(raw, "42601", "type modifiers must be simple constants or identifiers")
	}
	u := d.Usage()
	precision := raw.Precision
	if raw.RawPrecision != "" {
		integer, _ := view.LookupType(sqltype.Int4)
		ctx := context.Background()
		if op != nil {
			ctx = op.Context()
		}
		value, err := integer.Input(ctx, raw.RawPrecision, -1)
		if err != nil {
			return sqltype.Usage{}, inputErrorAt(err, raw.Pos)
		}
		n, err := value.Integer()
		if err != nil {
			return sqltype.Usage{}, err
		}
		p := int32(n)
		precision = &p
	}
	if precision != nil {
		var warning *sqlstate.Error
		var err error
		u, warning, err = d.ResolveModifier(*precision)
		if err != nil {
			return sqltype.Usage{}, inputErrorAt(err, raw.Pos)
		}
		if warning != nil && op != nil {
			if raw.Pos.Character > 0 {
				warning.Position = strconv.Itoa(raw.Pos.Character)
			}
			op.emitNotice(levelWarning, *warning)
		}
	}
	return u, nil
}

func typeDiagnostic(raw parser.TypeName, code, message string) error {
	if raw.Pos.Character > 0 {
		return insertError(code, message, raw.Pos)
	}
	return sqlError(code, message)
}

// Recognized PostgreSQL types outside the concrete storage authority are a
// capability boundary, not nonexistent names. Classification belongs to binding
// so IF NOT EXISTS can skip it, never to the raw syntax parser.
func unsupportedColumnType(raw parser.TypeName) bool {
	switch raw.Name {
	case "smallserial", "serial2", "serial", "serial4", "bigserial", "serial8":
		// CREATE recognizes these utility pseudo names even when quoted.
		return true
	case "money", "char", "bpchar", "varchar",
		"numeric", "bit", "varbit", "date", "time", "timetz",
		"timestamptz", "interval", "bytea", "json", "jsonb", "xml",
		"name", "oid", "inet", "cidr", "macaddr", "macaddr8", "point", "line",
		"lseg", "box", "path", "polygon", "circle", "tsvector", "tsquery", "record":
		return true
	}
	if !raw.Quoted {
		switch raw.Name {
		case "decimal", "dec", "double", "precision",
			"character":
			return true
		}
	}
	return false
}
