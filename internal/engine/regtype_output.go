package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func regtypeSpelling(view CatalogRead, path []ids.ObjectAddress, id ids.OID) string {
	return regtypeSpellingMode(view, path, id, false)
}

// Machine object identities qualify nonspecial SQL type names, including the
// canonical temporary namespace alias, without changing ordinary regtype output.
func regtypeSpellingMode(view CatalogRead, path []ids.ObjectAddress, id ids.OID, qualified bool) string {
	if id == 0 {
		return "-"
	}
	typ, ok := view.LookupType(sqltype.ID(id))
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	if typ.IsArray() {
		return regtypeSpellingMode(view, path, ids.OID(typ.Element().ID()), qualified) + "[]"
	}
	switch typ.ID() {
	case sqltype.Bool:
		return "boolean"
	case sqltype.Int2:
		return "smallint"
	case sqltype.Int4:
		return "integer"
	case sqltype.Int8:
		return "bigint"
	case sqltype.Float4:
		return "real"
	case sqltype.Float8:
		return "double precision"
	case sqltype.BPChar:
		return "character"
	case sqltype.VarChar:
		return "character varying"
	case sqltype.Time:
		return "time without time zone"
	case sqltype.Timetz:
		return "time with time zone"
	case sqltype.Timestamp:
		return "timestamp without time zone"
	case sqltype.Timestamptz:
		return "timestamp with time zone"
	case sqltype.VarBit:
		return "bit varying"
	case sqltype.Bit:
		return "bit"
	case sqltype.Numeric:
		return "numeric"
	case sqltype.Interval:
		return "interval"
	case sqltype.JSON:
		return "json"
	}
	d := view.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}]
	name := regclassIdentifier(d.Name())
	if !qualified && namedObjectVisible(view, path, d) {
		return name
	}
	namespace := view.state.descriptors[d.Namespace()].(*namespaceDescriptor)
	schema := namespace.name
	if qualified && namespace.temporary != (SessionToken{}) {
		schema = "pg_temp"
	}
	return regclassIdentifier(schema) + "." + name
}

func (s *StatementScope) FormatRegtype(id uint32) (string, error) {
	if s == nil || s.commentCatalog.state == nil {
		return "", fmt.Errorf("stuntgres: missing regtype output snapshot")
	}
	return regtypeSpelling(s.commentCatalog, s.deparsePath, ids.OID(id)), nil
}

func (r *finalRows) FormatRegtype(id uint32) (string, error) {
	text, ok := r.regtypeOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regtype output snapshot")
	}
	return text, nil
}

func captureRegtypeOutputValue(rows *finalRows, view CatalogRead, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegtype() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureRegtypeOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureRegtypeOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.TypeOID()
	if err != nil {
		return err
	}
	if _, ok := rows.regtypeOutput[id]; ok {
		return nil
	}
	if rows.regtypeOutput == nil {
		rows.regtypeOutput = make(map[uint32]string)
	}
	rows.regtypeOutput[id] = regtypeSpelling(view, path, ids.OID(id))
	rows.output.Regtype = rows
	return nil
}

type catalogRegtypeOutput struct {
	view CatalogRead
	path []ids.ObjectAddress
}

func (r catalogRegtypeOutput) FormatRegtype(id uint32) (string, error) {
	return regtypeSpelling(r.view, r.path, ids.OID(id)), nil
}
