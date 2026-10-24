package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoDomainsOID is the native virtual relation identity, not a PostgreSQL allocation.
const InfoDomainsOID ids.OID = 16215

var infoDomainsColumnTypes = [...]sqltype.ID{13248, 13248, 13248, 13246, 13243, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13243, 13243, 13243, 13246, 13243, 13246, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13248}

// Caller owns the engine capture lock. All metadata and privileges come from
// the same immutable frontier; no base-type visibility predicate is applied.
func captureInfoDomains(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoDomainsOID); err != nil {
		return nil, err
	}
	if in.TypeNamePath == nil {
		return nil, fmt.Errorf("stuntgres: missing domains type-name path")
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != len(infoDomainsColumnTypes) {
		return nil, fmt.Errorf("stuntgres: invalid domains source")
	}
	for i, id := range infoDomainsColumnTypes {
		typ, ok := in.Catalog.LookupType(id)
		if !ok || !typ.IsDomain() || in.Source.Columns[i].Usage != typ.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid domains source domain")
		}
		base, present := in.Catalog.LookupType(typ.DomainBase().Type.ID())
		if !present || typ.DomainBase().Type != base {
			return nil, fmt.Errorf("stuntgres: foreign domains source base")
		}
		expectedBase := sqltype.Name
		if id == 13246 {
			expectedBase = sqltype.VarChar
		}
		if id == 13243 {
			expectedBase = sqltype.Int4
		}
		if typ.DomainBase().Type.ID() != expectedBase {
			return nil, fmt.Errorf("stuntgres: invalid domains source carrier")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		domain, ok := descriptor.(*catalog.Type)
		if !ok || !domain.SQLType.IsDomain() || !domain.SQLType.Concrete() {
			continue
		}
		if address != domain.Address() {
			return nil, fmt.Errorf("stuntgres: invalid domains address")
		}
		if err := in.Runtime.ValidateType(in.Catalog, domain); err != nil {
			return nil, err
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), domain.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, domain.Address()), false)
			if err != nil {
				return nil, err
			}
			if rights&authorization.UsagePrivilege == 0 {
				continue
			}
		}
		namespace, ok := in.Catalog.Namespace(domain.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing domains namespace")
		}
		usage := domain.SQLType.DomainBase()
		base, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		if !ok || base.SQLType != usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign domains base authority")
		}
		baseNamespace, ok := in.Catalog.Namespace(base.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing domains base namespace")
		}
		facts, err := TypeFacts(in.Context, in.Catalog, domain, in.Runtime.TypeOutput(in.Owner, in.Catalog, in.Roles))
		if err != nil {
			return nil, err
		}
		var cells [27]sqltype.Value
		for i := range cells {
			cells[i] = sqltype.Null(in.Source.Columns[i].Usage.Type.BaseType())
		}
		text := func(i int, value string) {
			cells[i] = sqltype.String(in.Source.Columns[i].Usage.Type.BaseType(), value)
		}
		text(0, database)
		text(1, namespace.Name())
		text(2, domain.Name())
		dataType := "USER-DEFINED"
		if facts.Element != 0 && facts.Length == -1 {
			dataType = "ARRAY"
		} else if base.Namespace().ObjectID == ids.PGCatalogNamespaceOID {
			dataType, err = infoDomainsBaseName(in.Catalog, base, in.TypeNamePath)
			if err != nil {
				return nil, err
			}
		}
		text(3, dataType)
		text(19, database)
		text(20, baseNamespace.Name())
		text(21, base.Name())
		text(26, "1")
		if facts.HasDefault {
			text(18, facts.Default)
		}
		if facts.Collation != 0 {
			collation, ok := in.Catalog.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(facts.Collation)})
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing domains collation")
			}
			ns, ok := in.Catalog.Namespace(collation.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing domains collation namespace")
			}
			if ns.Name() != "pg_catalog" || collation.Name() != "default" {
				text(9, database)
				text(10, ns.Name())
				text(11, collation.Name())
			}
		}
		if err := infoDomainsModifiers(usage, &cells, in.Source); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, len(cells))
		for i, value := range cells {
			row[i], err = in.Source.Columns[i].Usage.Type.Wrap(value)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Retain array generation validation and the released JSON special spelling;
// other names use the catalog's captured visibility-aware type authority.
func infoDomainsBaseName(view catalog.Read, base *catalog.Type, path []ids.ObjectAddress) (string, error) {
	typ := base.SQLType
	if typ.IsArray() {
		element, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Element().ID())})
		if !ok || element.SQLType != typ.Element() {
			return "", fmt.Errorf("stuntgres: foreign domains array element")
		}
		name, err := infoDomainsBaseName(view, element, path)
		return name + "[]", err
	}
	if typ.ID() == sqltype.JSON {
		return "json", nil
	}
	return catalog.TypeDiagnosticNameAt(view, nil, base, path), nil
}

// The released helpers inspect the immediate base ID and the domain typmod,
// not flattened Usage metadata (notably for unlimited strings and numeric scale).
func infoDomainsModifiers(usage sqltype.Usage, cells *[27]sqltype.Value, source *catalog.Relation) error {
	number := func(i int, n int64) error {
		value, err := sqltype.Integer(source.Columns[i].Usage.Type.BaseType(), n)
		cells[i] = value
		return err
	}
	id, mod := usage.Type.ID(), usage.Typmod
	switch id {
	case sqltype.BPChar, sqltype.VarChar, sqltype.Text:
		if mod != -1 {
			if err := number(4, int64(mod-4)); err != nil {
				return err
			}
		}
		octets := int64(1073741824)
		if mod != -1 {
			octets = int64(mod-4) * 4
		}
		if err := number(5, octets); err != nil {
			return err
		}
	case sqltype.Bit, sqltype.VarBit:
		if mod != -1 {
			if err := number(4, int64(mod)); err != nil {
				return err
			}
		}
	case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Float4, sqltype.Float8, sqltype.Numeric:
		precision, radix := int64(0), int64(2)
		switch id {
		case sqltype.Int2:
			precision = 16
		case sqltype.Int4:
			precision = 32
		case sqltype.Int8:
			precision = 64
		case sqltype.Float4:
			precision = 24
		case sqltype.Float8:
			precision = 53
		case sqltype.Numeric:
			radix = 10
			if mod != -1 {
				precision = int64((mod - 4) >> 16 & 0xffff)
			}
		}
		if precision != 0 {
			if err := number(12, precision); err != nil {
				return err
			}
		}
		if err := number(13, radix); err != nil {
			return err
		}
		if id == sqltype.Numeric && mod != -1 {
			if err := number(14, int64((mod-4)&0xffff)); err != nil {
				return err
			}
		} else if id == sqltype.Int2 || id == sqltype.Int4 || id == sqltype.Int8 {
			if err := number(14, 0); err != nil {
				return err
			}
		}
	case sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval:
		precision := mod
		if id == sqltype.Date {
			precision = 0
		} else if id == sqltype.Interval {
			precision = mod & 0xffff
			if precision == sqltype.IntervalFullPrecision {
				precision = 6
			}
			if mod != -1 && mod>>16 != sqltype.IntervalFullRange {
				// Declaration preserves arbitrary interval typmods using generic
				// syntax; this view instead needs the released range spelling.
				rangeUsage := usage
				rangeUsage.Typmod = mod | sqltype.IntervalFullPrecision
				declaration, err := rangeUsage.Declaration()
				if err != nil {
					return err
				}
				name := strings.TrimPrefix(declaration, "INTERVAL ")
				if mod&0xffff != sqltype.IntervalFullPrecision {
					name = fmt.Sprintf("%s(%d)", name, mod&0xffff)
				}
				cells[16] = sqltype.String(source.Columns[16].Usage.Type.BaseType(), name)
			}
		} else if mod == -1 {
			precision = 6
		}
		if err := number(15, int64(precision)); err != nil {
			return err
		}
	}
	return nil
}
