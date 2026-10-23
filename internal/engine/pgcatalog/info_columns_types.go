package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoColumnTypeMetadata uses -1 for inapplicable cardinal dimensions. Domain
// metadata follows the declaration's immediate base, not recursive codecs.
type InfoColumnTypeMetadata struct {
	Domain, UDT                                           *catalog.Type
	DataType, IntervalType                                string
	CharacterMaximumLength, CharacterOctetLength          int64
	NumericPrecision, NumericPrecisionRadix, NumericScale int64
	DatetimePrecision                                     int64
}

func InfoColumnTypeFacts(view catalog.Read, usage sqltype.Usage) (InfoColumnTypeMetadata, error) {
	facts := InfoColumnTypeMetadata{CharacterMaximumLength: -1, CharacterOctetLength: -1, NumericPrecision: -1, NumericPrecisionRadix: -1, NumericScale: -1, DatetimePrecision: -1}
	if usage.Type == nil {
		return facts, fmt.Errorf("stuntgres: missing columns type")
	}
	declared, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
	if !ok {
		return facts, fmt.Errorf("stuntgres: missing columns type authority")
	}
	typ, ok := declared.(*catalog.Type)
	if !ok || typ.SQLType != usage.Type {
		return facts, fmt.Errorf("stuntgres: foreign columns type generation")
	}
	if usage.Type.IsDomain() {
		facts.Domain = typ
		usage = usage.Type.DomainBase()
		base, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if !ok {
			return facts, fmt.Errorf("stuntgres: missing columns domain base")
		}
		typ, ok = base.(*catalog.Type)
		if !ok || typ.SQLType != usage.Type {
			return facts, fmt.Errorf("stuntgres: foreign columns domain base generation")
		}
	}
	facts.UDT = typ
	namespace, ok := view.Namespace(typ.Namespace())
	if !ok {
		return facts, fmt.Errorf("stuntgres: missing columns type namespace")
	}
	if usage.Type.IsArray() {
		facts.DataType = "ARRAY"
		return facts, nil
	}
	if namespace.Address().ObjectID != ids.PGCatalogNamespaceOID || usage.Type.IsDomain() {
		facts.DataType = "USER-DEFINED"
		return facts, nil
	}
	facts.DataType = usage.Type.DiagnosticName()
	if facts.DataType == "" {
		facts.DataType = typ.Name()
	}
	id, mod := usage.Type.ID(), usage.Typmod
	switch id {
	case sqltype.BPChar, sqltype.VarChar:
		if mod != -1 {
			facts.CharacterMaximumLength = int64(mod - 4)
		}
		fallthrough
	case sqltype.Text:
		facts.CharacterOctetLength = 1073741824
		if mod != -1 {
			facts.CharacterOctetLength = facts.CharacterMaximumLength * 4
		}
	case sqltype.Bit, sqltype.VarBit:
		if mod != -1 {
			facts.CharacterMaximumLength = int64(mod)
		}
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		facts.NumericPrecision, facts.NumericPrecisionRadix, facts.NumericScale = 32, 2, 0
		if id == sqltype.Int2 {
			facts.NumericPrecision = 16
		}
		if id == sqltype.Int8 {
			facts.NumericPrecision = 64
		}
	case sqltype.Float4, sqltype.Float8:
		facts.NumericPrecision, facts.NumericPrecisionRadix = 24, 2
		if id == sqltype.Float8 {
			facts.NumericPrecision = 53
		}
	case sqltype.Numeric:
		facts.NumericPrecisionRadix = 10
		if mod != -1 {
			facts.NumericPrecision = int64(((mod - 4) >> 16) & 0xffff)
			// PostgreSQL's information-schema helper exposes unsigned scale
			// bits even when value coercion interprets a negative scale.
			facts.NumericScale = int64((mod - 4) & 0xffff)
		}
	case sqltype.Date:
		facts.DatetimePrecision = 0
	case sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz:
		facts.DatetimePrecision = 6
		if mod >= 0 {
			facts.DatetimePrecision = int64(mod)
		}
	case sqltype.Interval:
		facts.DatetimePrecision = 6
		if mod >= 0 {
			if mod&0xffff != sqltype.IntervalFullPrecision {
				facts.DatetimePrecision = int64(mod & 0xffff)
			}
			if mod>>16 != sqltype.IntervalFullRange {
				declaration, err := usage.Declaration()
				if err != nil {
					return facts, err
				}
				facts.IntervalType = strings.TrimPrefix(declaration, "INTERVAL ")
			}
		}
	}
	return facts, nil
}
