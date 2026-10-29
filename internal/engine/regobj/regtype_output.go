package regobj

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TypeSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID) string {
	return regtypeSpellingMode(view, path, id, false, ids.SessionToken{})
}

// Machine object identities qualify nonspecial SQL type names, including the
// canonical temporary namespace alias, without changing ordinary regtype output.
func regtypeSpellingMode(view catalog.Read, path []ids.ObjectAddress, id ids.OID, qualified bool, temporary ids.SessionToken) string {
	if id == 0 {
		return "-"
	}
	typ, ok := view.LookupType(sqltype.ID(id))
	if !ok {
		return strconv.FormatUint(uint64(id), 10)
	}
	if typ.IsArray() && !typ.IsCatalogVector() {
		return regtypeSpellingMode(view, path, ids.OID(typ.Element().ID()), qualified, temporary) + "[]"
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
	d := view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
	name := regclassIdentifier(d.Name())
	if !qualified && NamedObjectVisible(view, path, d) {
		return name
	}
	namespace := view.MustNamespace(d.Namespace())
	schema := namespace.Name()
	if qualified && temporary != (ids.SessionToken{}) && namespace.Temporary == temporary && !namespace.TemporaryToast {
		schema = "pg_temp"
	}
	return regclassIdentifier(schema) + "." + name
}

func captureRegtypeOutputValue(rows *exec.FinalRows, view catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegtype() {
		return nil
	}
	if value.Type().BaseType().RangeType() != nil {
		return value.VisitRangeEndpoints(func(leaf sqltype.Value) error { return captureRegtypeOutputValue(rows, view, path, leaf) })
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
	if _, ok := rows.RegtypeOutput[id]; ok {
		return nil
	}
	if rows.RegtypeOutput == nil {
		rows.RegtypeOutput = make(map[uint32]string)
	}
	rows.RegtypeOutput[id] = TypeSpelling(view, path, ids.OID(id))
	rows.Output.Regtype = rows
	return nil
}

type TypeOutput struct {
	View catalog.Read
	Path []ids.ObjectAddress
}

func (r TypeOutput) FormatRegtype(id uint32) (string, error) {
	return TypeSpelling(r.View, r.Path, ids.OID(id)), nil
}
