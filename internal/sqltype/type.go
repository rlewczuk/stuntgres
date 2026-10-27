// Package sqltype owns concrete type identity, immutable values and the bounded
// input, assignment and comparison providers used by the SQL engine.
package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqlstate"
)

type ID uint32

const (
	Bool             ID = 16
	Bytea            ID = 17
	Char             ID = 18
	Name             ID = 19
	Int8             ID = 20
	Int2             ID = 21
	Int2Vector       ID = 22
	Int4             ID = 23
	Regproc          ID = 24
	Text             ID = 25
	Oid              ID = 26
	Tid              ID = 27
	Xid              ID = 28
	Cid              ID = 29
	OidVector        ID = 30
	Xid8             ID = 5069
	PgLSN            ID = 3220
	PgSnapshot       ID = 5038
	JSON             ID = 114
	PgNodeTree       ID = 194
	PgNDistinct      ID = 3361
	PgDependencies   ID = 3402
	PgMCVList        ID = 5017
	Point            ID = 600
	Lseg             ID = 601
	Path             ID = 602
	Box              ID = 603
	Polygon          ID = 604
	Line             ID = 628
	Cidr             ID = 650
	Float4           ID = 700
	Float8           ID = 701
	Unknown          ID = 705
	Circle           ID = 718
	Money            ID = 790
	Macaddr          ID = 829
	Macaddr8         ID = 774
	Inet             ID = 869
	NameArray        ID = 1003
	ACLItem          ID = 1033
	ACLItemArray     ID = 1034
	BPChar           ID = 1042
	VarChar          ID = 1043
	Date             ID = 1082
	Time             ID = 1083
	Timestamp        ID = 1114
	Timestamptz      ID = 1184
	Interval         ID = 1186
	Timetz           ID = 1266
	Bit              ID = 1560
	VarBit           ID = 1562
	Numeric          ID = 1700
	Refcursor        ID = 1790
	Regprocedure     ID = 2202
	Regoper          ID = 2203
	Regoperator      ID = 2204
	Regclass         ID = 2205
	Regtype          ID = 2206
	Void             ID = 2278
	UUID             ID = 2950
	Regconfig        ID = 3734
	Regdictionary    ID = 3769
	JSONB            ID = 3802
	Regrole          ID = 4096
	Regnamespace     ID = 4089
	Regcollation     ID = 4191
	Int4Range        ID = 3904
	NumRange         ID = 3906
	TimestampRange   ID = 3908
	TimestamptzRange ID = 3910
	DateRange        ID = 3912
	Int8Range        ID = 3926
	TSVector         ID = 3614
	TSQuery          ID = 3615
	JSONPath         ID = 4072
	Int4Multirange   ID = 4451
	NumMultirange    ID = 4532
	Int8Multirange   ID = 4536
)

type CollationDerivation uint8

const (
	None CollationDerivation = iota
	Default
)

type CollationRef struct {
	ID           uint32
	Derivation   CollationDerivation
	provider     uint32
	registeredID uint32
}

type Usage struct {
	Type      *Descriptor
	Typmod    int32
	Collation CollationRef
}

// Codec text output is independent of SQL quoting and driver export.
type Codec interface {
	Input(context.Context, string, int32) (Value, error)
	Output(context.Context, Value) (string, error)
	SQLLiteral(context.Context, Value) (string, error)
}

// Descriptor has no mutable or execution-local state. Catalogs own generations.
type Descriptor struct {
	id                                  ID
	name, sql, database                 string
	base                                Usage
	ownDomainChecks, domainChecks       bool
	ownDomainNotNull, domainNotNull     bool
	element                             *Descriptor
	associatedArray                     ID
	delimiter                           byte
	enum                                *enumDefinition
	queryInt                            bool
	seg                                 bool
	lquery                              bool
	ltree                               bool
	ltxtquery                           bool
	hstore                              bool
	cube                                bool
	citext                              bool
	ean13                               bool
	isbn                                bool
	isbn13                              bool
	ismn                                bool
	ismn13                              bool
	issn                                bool
	issn13                              bool
	upc                                 bool
	composite                           *compositeDefinition
	rangeSubtype                        *Descriptor
	customRange                         *RangeDefinition
	rangeBoolean                        *Descriptor
	multirangeRange                     *Descriptor
	multirangeCompanion                 *Descriptor
	networkCIDR                         *Descriptor
	networkMasklen                      *Descriptor
	networkBoolean                      *Descriptor
	geometryPoint                       *Descriptor
	circleBoolean, circleFloat          *Descriptor
	pointBoolean, pointFloat            *Descriptor
	polygonBoolean, polygonPoint        *Descriptor
	polygonInt                          *Descriptor
	pathInt                             *Descriptor
	ftsBoolean, ftsDistance, ftsText    *Descriptor
	ftsChar, ftsTextArray, ftsCharArray *Descriptor
	lookupOnly                          bool
	catalogNullOwner                    *Descriptor // Self on authentic restricted catalog result descriptors.
	queryNullOwner                      *Descriptor // Self only on canonical query-only pseudo NULL identities.
	polymorphicOwner                    *Descriptor // Self only on canonical polymorphic signature identities.
	namespace                           string
}

func (d *Descriptor) ID() ID          { return d.id }
func (d *Descriptor) Name() string    { return d.name }
func (d *Descriptor) SQLName() string { return d.sql }

// PostgreSQLTypeLength returns pg_type.typlen, not a declaration or display
// length. Catalog identity alone does not grant codec or storage authority.
func (d *Descriptor) PostgreSQLTypeLength() int16 {
	if d.IsDomain() {
		return d.base.Type.PostgreSQLTypeLength()
	}
	if d.IsEnum() {
		return 4
	}
	if d.IsSeg() {
		return 12
	}
	if d.IsEan13() || d.IsIsbn() || d.IsIsbn13() || d.IsIsmn() || d.IsIsmn13() || d.IsIssn() || d.IsIssn13() || d.IsUpc() {
		return 8
	}
	if d.IsArray() || d.IsComposite() || d.rangeSubtype != nil || d.multirangeRange != nil {
		return -1
	}
	// Fixed lengths, including lookup-only identities, from PostgreSQL REL_18_0
	// src/include/catalog/pg_type.dat. Unlisted identities are variable-width.
	switch d.id {
	case Bool, Char:
		return 1
	case Int2:
		return 2
	case Int4, Oid, Xid, Cid, Regclass, Regtype, Regproc, Regprocedure, Regrole, Date, Float4, Void,
		269, 325, // table_am_handler, index_am_handler
		2203, 2204, // regoper, regoperator
		2276, 2279, 2280, 2283, // any, trigger, language_handler, anyelement
		2776, 3115, 3310, 3500, // anynonarray, fdw_handler, tsm_handler, anyenum
		3734, 3769, 3838, // regconfig, regdictionary, event_trigger
		Regnamespace, Regcollation,
		5077, 5079: // anycompatible, anycompatiblenonarray
		return 4
	case Tid, Macaddr:
		return 6
	case Int8, Float8, Money, Time, Timestamp, Timestamptz, Xid8,
		Macaddr8, PgLSN:
		return 8
	case 32, 2281: // pg_ddl_command, internal: SIZEOF_POINTER
		return int16(strconv.IntSize / 8)
	case Timetz:
		return 12
	case Interval, UUID, 600, 1033: // point, aclitem
		return 16
	case 628, 718: // line, circle
		return 24
	case 601, 603: // lseg, box
		return 32
	case Name:
		return 64
	case Unknown, 2275: // cstring
		return -2
	default:
		return -1
	}
}

func (d *Descriptor) DiagnosticName() string {
	if IsPseudo(d.id) {
		if d.id == 2276 {
			return `"any"`
		}
		return d.name
	}
	if d.IsUserDefined() {
		return d.sql
	}
	if d.IsCatalogVector() {
		return d.name
	}
	if d.IsArray() {
		return d.element.DiagnosticName() + "[]"
	}
	if d.id == Timestamp {
		return "timestamp without time zone"
	}
	if d.id == Time {
		return "time without time zone"
	}
	if d.id == Timestamptz {
		return "timestamp with time zone"
	}
	if d.id == Timetz {
		return "time with time zone"
	}
	if d.id == BPChar {
		return "character"
	}
	if d.id == VarChar {
		return "character varying"
	}
	return strings.ToLower(d.sql)
}
func (d *Descriptor) DatabaseName() string { return d.database }
func (d *Descriptor) Concrete() bool {
	return d != nil && d.id != 0 && d.id != Unknown && !d.lookupOnly
}
func (d *Descriptor) Usage() Usage {
	if d.IsDomain() {
		return Usage{Type: d, Typmod: -1, Collation: d.base.Collation}
	}
	if d.IsArray() {
		u := d.element.Usage()
		u.Type = d
		u.Typmod = -1
		return u
	}
	u := Usage{Type: d, Typmod: -1}
	if IsCharacter(d.id) || d.IsCitext() || d.id == PgNodeTree || IsStatisticsType(d.id) {
		u.Collation = CollationRef{ID: 100, Derivation: Default}
	} else if d.id == Name {
		u.Collation = CollationRef{ID: 950, Derivation: Default}
	}
	return u
}

// Builtins returns a new immutable generation, not a shared mutable registry.
// It copies a template built once (builtin_template.go).
func Builtins() []*Descriptor { return cloneBuiltins() }

// buildBuiltins constructs a builtin generation from the compiled tables.
func buildBuiltins() []*Descriptor {
	types := []*Descriptor{
		{id: Bool, name: "bool", sql: "BOOLEAN", database: "BOOL", associatedArray: 1000},
		{id: Bytea, name: "bytea", sql: "BYTEA", database: "BYTEA", associatedArray: 1001},
		{id: Char, name: "char", sql: "\"char\"", database: "CHAR"},
		{id: Name, name: "name", sql: "NAME", database: "NAME", associatedArray: NameArray},
		{id: Int8, name: "int8", sql: "BIGINT", database: "INT8", associatedArray: 1016},
		{id: Int2, name: "int2", sql: "SMALLINT", database: "INT2", associatedArray: 1005},
		{id: Int2Vector, name: "int2vector", sql: "INT2VECTOR", database: "INT2VECTOR", associatedArray: 1006},
		{id: Int4, name: "int4", sql: "INTEGER", database: "INT4", associatedArray: 1007},
		{id: Text, name: "text", sql: "TEXT", database: "TEXT", associatedArray: 1009},
		{id: Refcursor, name: "refcursor", sql: "REFCURSOR", database: "REFCURSOR", associatedArray: 2201},
		{id: Oid, name: "oid", sql: "OID", database: "OID", associatedArray: 1028},
		{id: OidVector, name: "oidvector", sql: "OIDVECTOR", database: "OIDVECTOR", associatedArray: 1013},
		{id: Tid, name: "tid", sql: "TID", database: "TID"},
		{id: Macaddr, name: "macaddr", sql: "MACADDR", database: "MACADDR", associatedArray: 1040},
		{id: Macaddr8, name: "macaddr8", sql: "MACADDR8", database: "MACADDR8", associatedArray: 775},
		{id: Xid, name: "xid", sql: "XID", database: "XID", associatedArray: 1011},
		{id: Cid, name: "cid", sql: "CID", database: "CID"},
		{id: Xid8, name: "xid8", sql: "XID8", database: "XID8"},
		{id: PgLSN, name: "pg_lsn", sql: "PG_LSN", database: "PG_LSN", associatedArray: 3221},
		{id: PgSnapshot, name: "pg_snapshot", sql: "PG_SNAPSHOT", database: "PG_SNAPSHOT"},
		{id: JSON, name: "json", sql: "JSON", database: "JSON", associatedArray: 199},
		{id: JSONB, name: "jsonb", sql: "JSONB", database: "JSONB", associatedArray: 3807},
		{id: JSONPath, name: "jsonpath", sql: "JSONPATH", database: "JSONPATH", associatedArray: 4073},
		{id: TSVector, name: "tsvector", sql: "TSVECTOR", database: "TSVECTOR", associatedArray: 3643},
		{id: TSQuery, name: "tsquery", sql: "TSQUERY", database: "TSQUERY", associatedArray: 3645},
		{id: Point, name: "point", sql: "POINT", database: "POINT", associatedArray: 1017},
		{id: Lseg, name: "lseg", sql: "LSEG", database: "LSEG", associatedArray: 1018},
		{id: Path, name: "path", sql: "PATH", database: "PATH", associatedArray: 1019},
		{id: Box, name: "box", sql: "BOX", database: "BOX", associatedArray: 1020, delimiter: ';'},
		{id: Polygon, name: "polygon", sql: "POLYGON", database: "POLYGON", associatedArray: 1027},
		{id: Line, name: "line", sql: "LINE", database: "LINE", associatedArray: 629},
		{id: PgNodeTree, name: "pg_node_tree", sql: "PG_NODE_TREE", database: "PG_NODE_TREE"},
		{id: PgNDistinct, name: "pg_ndistinct", sql: "PG_NDISTINCT", database: "PG_NDISTINCT"},
		{id: PgDependencies, name: "pg_dependencies", sql: "PG_DEPENDENCIES", database: "PG_DEPENDENCIES"},
		{id: PgMCVList, name: "pg_mcv_list", sql: "PG_MCV_LIST", database: "PG_MCV_LIST"},
		{id: BPChar, name: "bpchar", sql: "BPCHAR", database: "BPCHAR", associatedArray: 1014},
		{id: VarChar, name: "varchar", sql: "VARCHAR", database: "VARCHAR", associatedArray: 1015},
		{id: Float4, name: "float4", sql: "REAL", database: "FLOAT4", associatedArray: 1021},
		{id: Float8, name: "float8", sql: "DOUBLE PRECISION", database: "FLOAT8", associatedArray: 1022},
		{id: Circle, name: "circle", sql: "CIRCLE", database: "CIRCLE", associatedArray: 719},
		{id: Money, name: "money", sql: "MONEY", database: "MONEY", associatedArray: 791},
		{id: Cidr, name: "cidr", sql: "CIDR", database: "CIDR", associatedArray: 651},
		{id: Inet, name: "inet", sql: "INET", database: "INET", associatedArray: 1041},
		{id: ACLItem, name: "aclitem", sql: "ACLITEM", database: "ACLITEM", associatedArray: ACLItemArray},
		{id: Date, name: "date", sql: "DATE", database: "DATE", associatedArray: 1182},
		{id: Time, name: "time", sql: "TIME", database: "TIME", associatedArray: 1183},
		{id: Timestamp, name: "timestamp", sql: "TIMESTAMP", database: "TIMESTAMP", associatedArray: 1115},
		{id: Timestamptz, name: "timestamptz", sql: "TIMESTAMP WITH TIME ZONE", database: "TIMESTAMPTZ", associatedArray: 1185},
		{id: Interval, name: "interval", sql: "INTERVAL", database: "INTERVAL", associatedArray: 1187},
		{id: Timetz, name: "timetz", sql: "TIME WITH TIME ZONE", database: "TIMETZ", associatedArray: 1270},
		{id: Bit, name: "bit", sql: "BIT", database: "BIT"},
		{id: VarBit, name: "varbit", sql: "BIT VARYING", database: "VARBIT"},
		{id: Numeric, name: "numeric", sql: "NUMERIC", database: "NUMERIC", associatedArray: 1231},
		{id: UUID, name: "uuid", sql: "UUID", database: "UUID", associatedArray: 2951},
		{id: Regclass, name: "regclass", sql: "REGCLASS", database: "REGCLASS", associatedArray: 2210},
		{id: Regtype, name: "regtype", sql: "REGTYPE", database: "REGTYPE", associatedArray: 2211},
		{id: Regproc, name: "regproc", sql: "REGPROC", database: "REGPROC", associatedArray: 1008},
		{id: Regrole, name: "regrole", sql: "REGROLE", database: "REGROLE", associatedArray: 4097},
		{id: Regoper, name: "regoper", sql: "REGOPER", database: "REGOPER", associatedArray: 2208},
		{id: Regoperator, name: "regoperator", sql: "REGOPERATOR", database: "REGOPERATOR", associatedArray: 2209},
		{id: Regprocedure, name: "regprocedure", sql: "REGPROCEDURE", database: "REGPROCEDURE", associatedArray: 2207},
		{id: Regcollation, name: "regcollation", sql: "REGCOLLATION", database: "REGCOLLATION", associatedArray: 4192},
		{id: Regnamespace, name: "regnamespace", sql: "REGNAMESPACE", database: "REGNAMESPACE", associatedArray: 4090},
		{id: Regconfig, name: "regconfig", sql: "REGCONFIG", database: "REGCONFIG", associatedArray: 3735},
		{id: Regdictionary, name: "regdictionary", sql: "REGDICTIONARY", database: "REGDICTIONARY", associatedArray: 3770},
		{id: Void, name: "void", sql: "VOID", database: "VOID"},
		{id: Unknown, name: "unknown"},
	}
	var networkCIDR, integer, networkBoolean, geometryPoint, circleFloat, ftsText *Descriptor
	for _, d := range types {
		switch d.id {
		case Point:
			geometryPoint = d
		case Cidr:
			networkCIDR = d
		case Int4:
			integer = d
		case Text:
			ftsText = d
		case Bool:
			networkBoolean = d
		case Float8:
			circleFloat = d
		}
	}
	for _, d := range types {
		if d.id == Int2Vector || d.id == OidVector {
			element := Int2
			if d.id == OidVector {
				element = Oid
			}
			for _, candidate := range types {
				if candidate.id == element {
					d.element = candidate
					break
				}
			}
		}
	}
	for _, d := range types {
		if d.id == Box || d.id == Lseg || d.id == Line || d.id == Path {
			d.geometryPoint = geometryPoint
		}
		if d.id == Path {
			d.pathInt = integer
		}
		if IsNetwork(d.id) {
			d.networkCIDR, d.networkMasklen = networkCIDR, integer
			d.networkBoolean = networkBoolean
		}
		if d.id == Circle {
			d.circleBoolean, d.circleFloat = networkBoolean, circleFloat
		}
		if d.id == Point {
			d.pointBoolean, d.pointFloat = networkBoolean, circleFloat
		}
		if d.id == Polygon {
			d.polygonBoolean, d.polygonPoint = networkBoolean, geometryPoint
			d.polygonInt = integer
		}
		if d.id == TSQuery {
			d.ftsBoolean, d.ftsDistance, d.ftsText = networkBoolean, integer, ftsText
		}
	}
	for _, family := range []struct {
		rangeID, subtypeID, companionID ID
		name                            string
	}{
		{Int4Range, Int4, Int4Multirange, "int4range"},
		{NumRange, Numeric, NumMultirange, "numrange"},
		{Int8Range, Int8, Int8Multirange, "int8range"},
		{DateRange, Date, 0, "daterange"},
		{TimestampRange, Timestamp, 0, "tsrange"},
		{TimestamptzRange, Timestamptz, 0, "tstzrange"},
	} {
		name := strings.ToUpper(family.name)
		r := &Descriptor{id: family.rangeID, name: family.name, sql: name, database: name}
		for _, subtype := range types {
			if subtype.id == Bool {
				r.rangeBoolean = subtype
			}
			if subtype.id == family.subtypeID {
				r.rangeSubtype = subtype
			}
		}
		types = append(types, r)
		if family.companionID != 0 {
			companion := strings.TrimSuffix(family.name, "range") + "multirange"
			name = strings.ToUpper(companion)
			m := &Descriptor{id: family.companionID, name: companion, sql: name, database: name, multirangeRange: r}
			r.multirangeCompanion = m
			types = append(types, m)
		}
	}
	types = completeBuiltinIdentities(types)
	var vector *Descriptor
	for _, d := range types {
		if d.id == TSVector {
			vector = d
			break
		}
	}
	for _, d := range types {
		switch d.id {
		case Int4:
			vector.ftsDistance = d
		case Char:
			vector.ftsChar = d
		case Text:
			vector.ftsText = d
		case 1009:
			vector.ftsTextArray = d
		case 1002:
			vector.ftsCharArray = d
		}
	}
	return types
}

// Zero Value is invalid, not NULL. Scalars stay inline; only structural tree
// values own a pointer to an immutable payload. Value remains comparable.
type Value struct {
	typ             *Descriptor
	null            bool
	integer         int64
	west            int32
	oid             uint32
	floating        float64
	float4Raw       uint32 // Authoritative binary-created REAL word; floating is presentation/arithmetic.
	float4RawValid  bool
	boolean         bool
	text            string
	exponent, scale int64
	negative        bool
	special         uint8
	uuid            [16]byte
	months, days    int32
	tree            *treePayload
	treeKey         bool // text is an immutable prepared structural equality/output key.
	array           *arrayPayload
	composite       *compositePayload
	jsonb           *jsonbPayload
	jsonpath        *jsonpathPayload
	rangeDatum      rangeDatum
	multirange      *multirangePayload
	network         networkDatum
	aclitem         aclitemDatum
	queryInt        *queryIntPayload
	seg             SegDatum
	lquery          *lqueryPayload
	ltree           *ltreePayload
	ltxtquery       *ltxtqueryPayload
	hstore          *hstorePayload
	ftsVector       *fts.Vector
	ftsQuery        *fts.Query
	cube            *cubePayload
	geometry        *geometryPayload
	path            *pathPayload
	circle          circleDatum
	polygon         *polygonPayload
	snapshot        *Snapshot
	statistics      *statisticsPayload
	statisticArray  *statisticArrayPayload
}

func (v Value) Type() *Descriptor { return v.typ }
func (v Value) IsNull() bool      { return v.null }

// Retag remints descriptor ownership without changing a concrete scalar datum.
// It preserves logical identity and physical base constraints, not just payload shape.
func (v Value) Retag(d *Descriptor) (Value, error) {
	if v.typ != nil && v.typ.Usage().CatalogNullUsage() {
		return v.retagStatistic(d)
	}
	if v.typ == nil || !v.typ.Concrete() || !d.Concrete() || v.typ.id != d.id || v.typ.IsDomain() != d.IsDomain() || v.typ.BaseType().id != d.BaseType().id {
		return Value{}, fmt.Errorf("incompatible scalar type identity")
	}
	fromEnum, toEnum := v.typ.BaseType(), d.BaseType()
	if fromEnum.IsHstore() != toEnum.IsHstore() || fromEnum.IsHstore() && (fromEnum != toEnum || !v.null && !v.validHstore()) {
		return Value{}, fmt.Errorf("incompatible hstore generation")
	}
	if fromEnum.IsCitext() != toEnum.IsCitext() || fromEnum.IsCitext() && fromEnum != toEnum {
		return Value{}, fmt.Errorf("incompatible citext generation")
	}
	if fromEnum.IsEan13() != toEnum.IsEan13() || fromEnum.IsEan13() && (fromEnum != toEnum || !v.null && !v.validEan13()) {
		return Value{}, fmt.Errorf("incompatible ean13 generation")
	}
	if fromEnum.IsIsbn() != toEnum.IsIsbn() || fromEnum.IsIsbn() && (fromEnum != toEnum || !v.null && !v.validIsbn()) {
		return Value{}, fmt.Errorf("incompatible isbn generation")
	}
	if fromEnum.IsIsbn13() != toEnum.IsIsbn13() || fromEnum.IsIsbn13() && (fromEnum != toEnum || !v.null && !v.validIsbn13()) {
		return Value{}, fmt.Errorf("incompatible isbn13 generation")
	}
	if fromEnum.IsIsmn() != toEnum.IsIsmn() || fromEnum.IsIsmn() && (fromEnum != toEnum || !v.null && !v.validIsmn()) {
		return Value{}, fmt.Errorf("incompatible ismn generation")
	}
	if fromEnum.IsIsmn13() != toEnum.IsIsmn13() || fromEnum.IsIsmn13() && (fromEnum != toEnum || !v.null && !v.validIsmn13()) {
		return Value{}, fmt.Errorf("incompatible ismn13 generation")
	}
	if fromEnum.IsIssn() != toEnum.IsIssn() || fromEnum.IsIssn() && (fromEnum != toEnum || !v.null && !v.validIssn()) {
		return Value{}, fmt.Errorf("incompatible issn generation")
	}
	if fromEnum.IsIssn13() != toEnum.IsIssn13() || fromEnum.IsIssn13() && (fromEnum != toEnum || !v.null && !v.validIssn13()) {
		return Value{}, fmt.Errorf("incompatible issn13 generation")
	}
	if fromEnum.IsUpc() != toEnum.IsUpc() || fromEnum.IsUpc() && (fromEnum != toEnum || !v.null && !v.validUpc()) {
		return Value{}, fmt.Errorf("incompatible upc generation")
	}
	if fromEnum.IsEnum() != toEnum.IsEnum() {
		return Value{}, fmt.Errorf("incompatible enum declaration")
	}
	if fromEnum.IsEnum() && !v.null {
		if _, ok := fromEnum.enum.ranks[v.integer]; !ok {
			return Value{}, fmt.Errorf("invalid enum member")
		}
		if _, ok := toEnum.enum.ranks[v.integer]; !ok {
			return Value{}, fmt.Errorf("foreign enum member")
		}
	}
	if fromEnum.IsSeg() != toEnum.IsSeg() || fromEnum.IsSeg() && !v.null && !v.seg.valid() {
		return Value{}, fmt.Errorf("incompatible seg payload")
	}
	if fromEnum.IsQueryInt() != toEnum.IsQueryInt() {
		return Value{}, fmt.Errorf("incompatible query_int declaration")
	}
	if fromEnum.IsQueryInt() && !v.null && !v.validQueryInt() {
		return Value{}, fmt.Errorf("invalid query_int payload")
	}
	if fromEnum.IsLquery() != toEnum.IsLquery() || fromEnum.IsLquery() && (fromEnum != toEnum || !v.null && !v.validLquery()) {
		return Value{}, fmt.Errorf("incompatible lquery generation or payload")
	}
	if fromEnum.IsLtxtquery() != toEnum.IsLtxtquery() || fromEnum.IsLtxtquery() && (fromEnum != toEnum || !v.null && !v.validLtxtquery()) {
		return Value{}, fmt.Errorf("incompatible ltxtquery generation or payload")
	}
	if fromEnum.IsLtree() != toEnum.IsLtree() || fromEnum.IsLtree() && (fromEnum != toEnum || !v.null && !v.validLtree()) {
		return Value{}, fmt.Errorf("incompatible ltree generation or payload")
	}
	if fromEnum.IsCube() != toEnum.IsCube() || fromEnum.IsCube() && !v.null && !v.validCube() {
		return Value{}, fmt.Errorf("incompatible cube payload")
	}
	if d.IsDomain() {
		from, to := v.typ.Usage().BaseUsage(), d.Usage().BaseUsage()
		if from.Typmod != to.Typmod || from.Collation != to.Collation {
			return Value{}, fmt.Errorf("incompatible domain base constraints")
		}
	}
	if !v.null && d.BaseType().id == JSONB && v.jsonb == nil {
		return Value{}, fmt.Errorf("missing JSONB payload")
	}
	if !v.null && d.BaseType().id == JSONPath && v.jsonpath == nil {
		return Value{}, fmt.Errorf("missing JSONPath payload")
	}
	if !v.null && IsFTS(d.BaseType().id) && !v.validFTS() {
		return Value{}, fmt.Errorf("missing FTS payload")
	}
	if !v.null && (d.BaseType().id == Box || d.BaseType().id == Line || d.BaseType().id == Lseg) && v.geometry == nil {
		return Value{}, fmt.Errorf("missing geometry payload")
	}
	if !v.null && d.BaseType().id == Path && (v.path == nil || len(v.path.points) == 0) {
		return Value{}, fmt.Errorf("missing PATH payload")
	}
	if !v.null && d.BaseType().id == Polygon && !v.validPolygon() {
		return Value{}, fmt.Errorf("missing polygon payload")
	}
	if v.typ.BaseType().IsRange() != d.BaseType().IsRange() || v.typ.BaseType().IsMultirange() != d.BaseType().IsMultirange() {
		return Value{}, fmt.Errorf("incompatible range family identity")
	}
	if !v.null && d.BaseType().IsRange() && !v.validRange() {
		return Value{}, fmt.Errorf("invalid range datum")
	}
	if !v.null && d.BaseType().IsMultirange() {
		valid := false
		if v.typ.BaseType().RangeType().customRange != nil {
			valid = v.validMultirangeShape()
		} else {
			valid = v.validMultirange()
		}
		if !valid || !d.BaseType().validMultirangeAuthority() || d.BaseType().multirangeRange.id != v.typ.BaseType().multirangeRange.id || d.BaseType().multirangeRange.rangeSubtype.id != v.typ.BaseType().multirangeRange.rangeSubtype.id {
			return Value{}, fmt.Errorf("invalid multirange datum")
		}
	}
	if !v.null && IsNetwork(d.BaseType().id) && !v.network.valid(d.BaseType().id == Cidr) {
		return Value{}, fmt.Errorf("invalid network datum")
	}
	if !v.null && d.BaseType().id == ACLItem && !v.aclitem.valid() {
		return Value{}, fmt.Errorf("invalid aclitem datum")
	}
	if v.typ.BaseType().IsComposite() != d.BaseType().IsComposite() {
		return Value{}, fmt.Errorf("incompatible composite declaration")
	}
	if !v.null && v.typ.BaseType().IsComposite() && !v.validCompositePayload() {
		return Value{}, fmt.Errorf("invalid composite payload")
	}
	if !v.null && IsStatisticsType(d.BaseType().id) && !v.validStatistics() {
		return Value{}, fmt.Errorf("invalid statistics payload")
	}
	if !v.null && d.BaseType().IsCatalogVector() && !v.validCatalogVectorPayload() {
		return Value{}, fmt.Errorf("invalid catalog vector payload")
	}
	if v.typ == d {
		if !v.null && d.BaseType().IsArray() && v.array == nil {
			return Value{}, fmt.Errorf("missing array payload")
		}
		return v, nil
	}
	if v.typ.BaseType().IsArray() {
		return v.MapArray(d, func(element Value, target *Descriptor) (Value, error) { return element.Retag(target) })
	}
	if fromEnum.RangeType() != nil && fromEnum.RangeType().customRange != nil {
		return v.MapRange(d, RangeMapGeneration, func(endpoint Value, target *Descriptor) (Value, error) { return endpoint.Retag(target) })
	}
	if v.typ.BaseType().IsComposite() {
		for _, member := range d.BaseType().composite.members {
			if at, found := v.typ.BaseType().composite.attributes[member.Attribute]; found {
				old := v.typ.BaseType().composite.members[at].Usage
				if old.Typmod != member.Usage.Typmod || old.Collation != member.Usage.Collation {
					return Value{}, fmt.Errorf("incompatible composite member usage")
				}
			}
		}
		return v.MapComposite(d, func(field Value, target *Descriptor) (Value, error) { return field.Retag(target) })
	}
	v.typ = d
	return v, nil
}
func Null(d *Descriptor) Value {
	if d == nil || !d.Concrete() && !d.Usage().QueryNullUsage() {
		panic("invalid NULL descriptor")
	}
	return Value{typ: d, null: true}
}

// VoidValue is a concrete nonnull function result, distinct from typed NULL.
func VoidValue(d *Descriptor) Value {
	if d == nil || d.id != Void {
		panic("invalid void descriptor")
	}
	return Value{typ: d}
}
func Integer(d *Descriptor, n int64) (Value, error) {
	if d == nil || (d.id != Int2 && d.id != Int4 && d.id != Int8) {
		return Value{}, fmt.Errorf("invalid integer descriptor")
	}
	if d.id == Int2 && (n < -32768 || n > 32767) {
		return Value{}, failure("22003", "smallint out of range")
	}
	if d.id == Int4 && (n < -2147483648 || n > 2147483647) {
		return Value{}, failure("22003", "integer out of range")
	}
	return Value{typ: d, integer: n}, nil
}
func Boolean(d *Descriptor, b bool) Value {
	if d == nil || d.id != Bool {
		panic("invalid boolean descriptor")
	}
	return Value{typ: d, boolean: b}
}
func String(d *Descriptor, s string) Value {
	if d == nil || (!IsCharacter(d.id) && !d.IsCitext() && d.id != 2275 && d.id != Refcursor && d.id != Name && d.id != Regclass && d.id != Regtype && d.id != Char) {
		panic("invalid string descriptor")
	}
	v := Value{typ: d, text: s}
	if (d.id == Regclass || d.id == Regtype) && len(s) != 0 && (len(s) == 1 || s[0] != '0') {
		// Keep unresolved regclass input text intact. Canonical identities also
		// carry an inline unsigned payload, validated once rather than per
		// sort comparison; integer marks that payload as valid (OID zero too).
		canonical := true
		for i := range len(s) {
			if s[i] < '0' || s[i] > '9' {
				canonical = false
				break
			}
		}
		if canonical {
			if oid, err := strconv.ParseUint(s, 10, 32); err == nil {
				v.oid, v.integer = uint32(oid), 1
				if d.id == Regtype {
					v.text = ""
				}
			}
		}
	}
	return v
}

// Names is a checked NAME[] result constructor through the generic payload.
func Names(d *Descriptor, names []string) (Value, error) {
	if d == nil || !d.IsArray() || d.element.ID() != Name {
		return Value{}, fmt.Errorf("invalid NAME[] descriptor")
	}
	elements := make([]Value, len(names))
	for i, name := range names {
		if err := ValidateTextInput(name); err != nil {
			return Value{}, err
		}
		if len(name) > 63 {
			return Value{}, fmt.Errorf("invalid NAME[] element")
		}
		elements[i] = String(d.element, name)
	}
	var dimensions []ArrayDimension
	if len(names) > 0 {
		dimensions = []ArrayDimension{{1, int32(len(names))}}
	}
	return ownedArray(d, dimensions, elements)
}
func (v Value) NamesOutput() (string, error) {
	if v.null || v.typ == nil || !v.typ.IsArray() || v.typ.Element().ID() != Name {
		return "", fmt.Errorf("not a NAME[] value")
	}
	return v.typ.Output(context.Background(), v)
}
func (v Value) Integer() (int64, error) {
	if v.null || v.typ == nil || (v.typ.id != Int2 && v.typ.id != Int4 && v.typ.id != Int8) {
		return 0, fmt.Errorf("not an integer value")
	}
	return v.integer, nil
}
func (v Value) Boolean() (bool, error) {
	if v.null || v.typ == nil || v.typ.id != Bool {
		return false, fmt.Errorf("not a boolean value")
	}
	return v.boolean, nil
}
func (v Value) Text() (string, error) {
	if v.null || v.typ == nil || (!IsCharacter(v.typ.id) && !v.typ.IsCitext() && v.typ.id != Refcursor && v.typ.id != Name && v.typ.id != Regclass && v.typ.id != Regtype && v.typ.id != Char) {
		return "", fmt.Errorf("not a text value")
	}
	if v.typ.id == Regtype {
		id, err := v.TypeOID()
		if err != nil {
			return "", err
		}
		return strconv.FormatUint(uint64(id), 10), nil
	}
	if v.typ.id == Regclass && v.integer == 1 && v.text == "" {
		return strconv.FormatUint(uint64(v.oid), 10), nil
	}
	return v.text, nil
}

func failure(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
func trim(s string) string {
	for len(s) > 0 && space(s[0]) {
		s = s[1:]
	}
	for len(s) > 0 && space(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

func (d *Descriptor) Input(ctx context.Context, text string, typmod int32) (Value, error) {
	return d.InputWithEnvironment(ctx, text, typmod, InputEnvironment{})
}

func (d *Descriptor) InputWithEnvironment(ctx context.Context, text string, typmod int32, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if d.IsDomain() {
		if typmod != -1 {
			return Value{}, fmt.Errorf("invalid domain input modifier")
		}
		base := d.Usage().BaseUsage()
		value, err := base.Type.InputWithEnvironment(ctx, text, base.Typmod, env)
		if err != nil {
			return Value{}, err
		}
		if err := d.CheckDomainInput(ctx, value, env); err != nil {
			return Value{}, err
		}
		return d.Wrap(value)
	}
	// Dummy pseudo input routines report their SQL diagnostic without granting
	// the lookup-only identity ordinary datum or codec authority.
	if IsPseudo(d.id) && d.id != Void && d.id != Unknown && d.id != 2275 && d.id != 2249 {
		return Value{}, failure("0A000", "cannot accept a value of type "+d.DiagnosticName())
	}
	if !d.Concrete() || !d.validTypmod(typmod) {
		return Value{}, fmt.Errorf("invalid input usage")
	}
	if env.ExactSnapshot && typmod >= 0 {
		value, err := d.InputWithEnvironment(ctx, text, -1, env)
		if err != nil {
			return Value{}, err
		}
		target := d.Usage()
		target.Typmod = typmod
		plan, err := PlanCoercion(d.Usage(), target, Assignment)
		if err != nil {
			return Value{}, err
		}
		return plan.Apply(ctx, value, env, OutputEnvironment{})
	}
	if d.IsCatalogVector() {
		if err := ValidateTextInput(text); err != nil {
			return Value{}, err
		}
		return d.catalogVectorInput(ctx, text)
	}
	if d.IsArray() {
		if err := ValidateTextInput(text); err != nil {
			return Value{}, err
		}
		return d.arrayInput(ctx, text, typmod, env)
	}
	if d.IsDeferredRecord() {
		return Value{}, failure("0A000", "input of anonymous composite types is not implemented")
	}
	if d.IsComposite() {
		if err := validateCompositeInput(ctx, text); err != nil {
			return Value{}, err
		}
		// A negative typmod carries no anonymous record declaration. JSON's
		// fixed RECORD formal may transport NULL on an occurrence-owned shape,
		// but record_in cannot decode text without a named/registered layout.
		if d.id == 2249 && typmod < 0 {
			return Value{}, failure("0A000", "input of anonymous composite types is not implemented")
		}
		return d.compositeInput(ctx, text, env)
	}
	if IsStatisticsType(d.id) {
		return Value{}, failure("0A000", "cannot accept a value of type "+d.name)
	}
	if d.id == PgNodeTree {
		return Value{}, failure("0A000", "cannot accept a value of type pg_node_tree")
	}
	if IsFTS(d.id) || d.IsSeg() || d.IsHstore() || d.IsCube() || d.IsQueryInt() || (d.IsLquery() || d.IsLtree() || d.IsLtxtquery()) || d.IsCitext() || d.IsEan13() || d.IsIsbn() || d.IsIsbn13() || d.IsIsmn() || d.IsIsmn13() || d.IsIssn() || d.IsIssn13() || d.IsUpc() || IsCharacter(d.id) || d.id == Char || d.id == Refcursor || d.id == Name || d.id == JSON || d.id == JSONB || d.id == JSONPath {
		if err := ValidateTextInput(text); err != nil {
			return Value{}, err
		}
	} else if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return Value{}, failure("22P02", "invalid textual input")
	}
	if d.IsEnum() {
		return d.enumInput(text, env)
	}
	if d.IsSeg() {
		return d.segInput(ctx, text)
	}
	if d.IsHstore() {
		return d.hstoreInput(ctx, text)
	}
	if d.IsQueryInt() {
		return d.queryIntInput(ctx, text)
	}
	if d.IsLquery() {
		return d.lqueryInput(ctx, text)
	}
	if d.IsLtxtquery() {
		return d.ltxtqueryInput(ctx, text)
	}
	if d.IsLtree() {
		return d.ltreeInput(ctx, text)
	}
	if d.IsCube() {
		return d.cubeInput(ctx, text)
	}
	if d.IsCitext() {
		return Value{typ: d, text: text}, nil
	}
	if d.IsEan13() {
		return d.ean13Input(text)
	}
	if d.IsIsbn() || d.IsIsbn13() {
		return d.isbnInput(text)
	}
	if d.IsIsmn() || d.IsIsmn13() {
		return d.ismnInput(text)
	}
	if d.IsIssn() || d.IsIssn13() {
		return d.issnInput(text)
	}
	if d.IsUpc() {
		return d.upcInput(text)
	}
	if d.IsRange() {
		return d.rangeInput(ctx, text, env)
	}
	if d.IsMultirange() {
		return d.multirangeInput(ctx, text, env)
	}
	switch d.id {
	case Path:
		return d.pathInput(text)
	case Polygon:
		return d.polygonInput(ctx, text)
	case Line:
		return d.lineInput(text, env)
	case Lseg:
		return d.lsegInput(text)
	case Point:
		return d.pointInput(text)
	case Box:
		return d.geometryInput(text, env)
	case TSVector, TSQuery:
		return d.ftsInput(ctx, text, env)
	case Tid:
		return d.tidInput(ctx, text)
	case Macaddr:
		return d.macaddrInput(ctx, text)
	case Macaddr8:
		return d.macaddr8Input(ctx, text)
	case ACLItem:
		return d.aclitemInput(ctx, text, env)
	case Inet, Cidr:
		return d.networkInput(text)
	case Money:
		return d.moneyInput(text, env.MonetaryLocale)
	case Bit, VarBit:
		return d.bitInput(text, typmod)
	case JSON:
		return d.jsonInput(ctx, text)
	case JSONB:
		return d.jsonInput(ctx, text)
	case JSONPath:
		return d.jsonpathInput(ctx, text)
	case Void:
		return VoidValue(d), nil
	case PgSnapshot:
		return d.snapshotInput(text)
	case PgLSN:
		return d.pgLSNInput(text)
	case Oid, Xid, Cid, Xid8:
		return d.oidInput(text)
	case Char:
		if len(text) == 4 && text[0] == '\\' && text[1] >= '0' && text[1] <= '7' && text[2] >= '0' && text[2] <= '7' && text[3] >= '0' && text[3] <= '7' {
			value := byte((text[1]-'0')<<6 | (text[2]-'0')<<3 | (text[3] - '0'))
			if value == 0 {
				return String(d, ""), nil
			}
			return String(d, string([]byte{value})), nil
		}
		if len(text) > 1 {
			text = text[:1]
		}
		return String(d, text), nil
	case Name:
		return String(d, clipName(text)), nil
	case Bytea:
		return d.byteaInput(ctx, text)
	case UUID:
		return d.uuidInput(text)
	case Date:
		return d.civilInput(text, typmod, env)
	case Time, Timetz:
		return d.timeInput(text, typmod, env)
	case Interval:
		return d.intervalInput(ctx, text, typmod, env)
	case Numeric:
		return d.numericInput(text, typmod)
	case Timestamp, Timestamptz:
		return d.civilInput(text, typmod, env)
	case Refcursor, 2275:
		return String(d, text), nil
	case Text, BPChar, VarChar:
		return sizeCharacter(String(d, text), typmod, false)
	case Regconfig, Regdictionary:
		return regTSInput(ctx, d, text, env)
	case Regoper, Regoperator:
		if env.Operator == nil {
			return Value{}, fmt.Errorf("operator input lacks catalog authority")
		}
		var id uint32
		var err error
		if d.id == Regoper {
			id, err = env.Operator.ResolveRegoper(ctx, text)
		} else {
			id, err = env.Operator.ResolveRegoperator(ctx, text)
		}
		if err != nil {
			return Value{}, err
		}
		return OperatorValue(d, id)
	case Regproc, Regprocedure:
		if env.Regproc == nil {
			return Value{}, fmt.Errorf("routine alias input lacks catalog authority")
		}
		var n uint32
		var err error
		if d.id == Regprocedure {
			n, err = env.Regproc.ResolveRegprocedure(ctx, text)
		} else {
			n, err = env.Regproc.ResolveRegproc(ctx, text)
		}
		if err != nil {
			return Value{}, err
		}
		return RegprocValue(d, n)
	case Regtype:
		if env.Regtype == nil {
			return Value{}, fmt.Errorf("regtype input lacks catalog authority")
		}
		n, err := env.Regtype.ResolveRegtype(ctx, text)
		if err != nil {
			return Value{}, err
		}
		return RegtypeValue(d, n)
	case Regrole:
		return d.regroleInput(ctx, text, env)
	case Regcollation, Regnamespace:
		return d.regObjectInput(ctx, text, env)
	case Regclass:
		if env.Regclass != nil {
			n, err := env.Regclass.ResolveRegclass(ctx, text)
			if err != nil {
				return Value{}, err
			}
			return String(d, strconv.FormatUint(uint64(n), 10)), nil
		}
		n, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return Value{}, fmt.Errorf("regclass input lacks catalog authority")
		}
		return String(d, strconv.FormatUint(n, 10)), nil
	case Bool:
		s := strings.ToLower(trim(text))
		if s == "1" {
			return Boolean(d, true), nil
		}
		if s == "0" {
			return Boolean(d, false), nil
		}
		if s != "" && s != "o" {
			for _, p := range []string{"true", "yes", "on"} {
				if strings.HasPrefix(p, s) {
					return Boolean(d, true), nil
				}
			}
			for _, p := range []string{"false", "no", "off"} {
				if strings.HasPrefix(p, s) {
					return Boolean(d, false), nil
				}
			}
		}
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for type boolean: \"%s\"", text))
	case Int2, Int4, Int8:
		return d.integerInput(text)
	case Circle:
		return d.circleInput(text)
	case Float4, Float8:
		return d.floatInput(text)
	}
	return Value{}, fmt.Errorf("missing input provider")
}

func digit(c byte, base uint64) (uint64, bool) {
	var n uint64
	switch {
	case c >= '0' && c <= '9':
		n = uint64(c - '0')
	case c >= 'a' && c <= 'f':
		n = uint64(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		n = uint64(c - 'A' + 10)
	default:
		return 0, false
	}
	return n, n < base
}

func (d *Descriptor) integerInput(text string) (Value, error) {
	name, limit := "integer", uint64(2147483648)
	if d.id == Int2 {
		name, limit = "smallint", 32768
	}
	if d.id == Int8 {
		name, limit = "bigint", uint64(1)<<63
	}
	invalid := func() (Value, error) {
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for type %s: \"%s\"", name, text))
	}
	overflow := func() (Value, error) {
		return Value{}, failure("22003", fmt.Sprintf("value \"%s\" is out of range for type %s", text, name))
	}
	s := text
	for len(s) > 0 && space(s[0]) {
		s = s[1:]
	}
	negative := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		negative, s = s[0] == '-', s[1:]
	}
	base, prefixed := uint64(10), false
	if len(s) >= 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X':
			base, prefixed = 16, true
		case 'o', 'O':
			base, prefixed = 8, true
		case 'b', 'B':
			base, prefixed = 2, true
		}
		if prefixed {
			s = s[2:]
		}
	}
	var n uint64
	digits, previous := 0, false
	for i := range len(s) {
		c := s[i]
		if c == '_' {
			if (!previous && !(prefixed && i == 0)) || i+1 == len(s) {
				return invalid()
			}
			if _, ok := digit(s[i+1], base); !ok {
				return invalid()
			}
			previous = false
			continue
		}
		v, ok := digit(c, base)
		if !ok {
			if digits == 0 {
				return invalid()
			}
			rest := s[i:]
			for len(rest) > 0 && space(rest[0]) {
				rest = rest[1:]
			}
			if rest != "" {
				return invalid()
			}
			break
		}
		// Match release parsing order: check the accumulated prefix before
		// multiplying, then reject final range only after trailing syntax.
		if n > limit/base {
			return overflow()
		}
		n = n*base + v
		digits++
		previous = true
	}
	if digits == 0 {
		return invalid()
	}
	if n > limit || !negative && n == limit {
		return overflow()
	}
	value := int64(n)
	if negative {
		value = -value
	}
	return Integer(d, value)
}

func (d *Descriptor) Output(ctx context.Context, v Value) (string, error) {
	return d.OutputWithEnvironment(ctx, v, OutputEnvironment{})
}

func (d *Descriptor) OutputWithEnvironment(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if d == nil || v.typ != d || v.null {
		return "", fmt.Errorf("invalid output value")
	}
	if d.id == 2277 && d.Usage().CatalogNullUsage() {
		return v.statisticArrayOutput(ctx, env)
	}
	if d.IsDomain() {
		v.typ = d.BaseType()
		return v.typ.OutputWithEnvironment(ctx, v, env)
	}
	if IsStatisticsType(d.id) {
		return d.statisticsOutput(ctx, v, env)
	}
	if d.IsCatalogVector() {
		return d.catalogVectorOutput(ctx, v)
	}
	if d.IsArray() {
		return d.arrayOutput(ctx, v, env)
	}
	if d.IsDeferredRecord() && v.composite != nil && v.composite.shape != nil {
		v.typ = v.composite.shape
		return v.typ.OutputWithEnvironment(ctx, v, env)
	}
	if d.IsComposite() {
		return d.compositeOutput(ctx, v, env)
	}
	if d.IsEnum() {
		return d.enumOutput(v)
	}
	if d.IsSeg() {
		return d.segOutput(ctx, v)
	}
	if d.IsHstore() {
		return d.hstoreOutput(ctx, v)
	}
	if d.IsQueryInt() {
		return d.queryIntOutput(ctx, v)
	}
	if d.IsLquery() {
		return d.lqueryOutput(ctx, v)
	}
	if d.IsLtxtquery() {
		return d.ltxtqueryOutput(ctx, v)
	}
	if d.IsLtree() {
		return d.ltreeOutput(ctx, v)
	}
	if d.IsCube() {
		return d.cubeOutput(ctx, v, env)
	}
	if d.IsCitext() {
		return v.text, nil
	}
	if d.IsEan13() {
		return d.ean13Output(v)
	}
	if d.IsIsbn() {
		return d.isbnOutput(v)
	}
	if d.IsIsbn13() {
		return d.isbn13Output(v)
	}
	if d.IsIsmn() {
		return d.ismnOutput(v)
	}
	if d.IsIsmn13() {
		return d.ismn13Output(v)
	}
	if d.IsIssn() {
		return d.issnOutput(v)
	}
	if d.IsIssn13() {
		return d.issn13Output(v)
	}
	if d.IsUpc() {
		return d.upcOutput(v)
	}
	if d.IsRange() {
		return v.rangeOutput(ctx, env)
	}
	if d.IsMultirange() {
		return v.multirangeOutput(ctx, env)
	}
	switch d.id {
	case Path:
		return pathOutput(v, env)
	case Polygon:
		return d.polygonOutput(ctx, v, env)
	case Box, Point, Line, Lseg:
		return d.geometryOutput(v, env)
	case Circle:
		return circleOutput(v.circle, env), nil
	case TSVector, TSQuery:
		return d.ftsOutput(v)
	case Tid:
		return tidOutput(v)
	case Macaddr:
		return macaddrOutput(v)
	case Macaddr8:
		return macaddr8Output(v)
	case ACLItem:
		return v.aclitemOutput(env)
	case Inet, Cidr:
		if !v.network.valid(d.id == Cidr) {
			return "", fmt.Errorf("invalid network value")
		}
		return v.network.output(d.id == Cidr), nil
	case Money:
		return moneyOutput(v.integer, env.MonetaryLocale), nil
	case Bit, VarBit:
		return v.text, nil
	case PgNodeTree:
		if v.treeKey {
			return v.text, nil
		}
		if v.tree == nil {
			return "", fmt.Errorf("missing structural tree payload")
		}
		return v.tree.output(ctx)
	case Void:
		return "", nil
	case Oid, Xid, Cid:
		return strconv.FormatUint(uint64(v.oid), 10), nil
	case PgSnapshot:
		s, err := v.Snapshot()
		if err != nil {
			return "", err
		}
		return s.String(), nil
	case PgLSN:
		return pgLSNOutput(uint64(v.integer)), nil
	case Xid8:
		return strconv.FormatUint(uint64(v.integer), 10), nil
	case Bytea:
		return byteaOutput(v.text, env.ByteaOutput)
	case UUID:
		return uuidOutput(v.uuid), nil
	case Date:
		return dateStyledOutput(v.integer, env.DateStyle), nil
	case Time:
		return timeOutput(v.integer), nil
	case Timetz:
		return timeOutput(v.integer) + zoneOffsetOutput(-v.west), nil
	case Interval:
		return intervalOutput(v, env.IntervalStyle)
	case Numeric:
		return numericOutput(v), nil
	case Timestamp:
		return timestampStyledOutput(v.integer, env.DateStyle, "", 0, false), nil
	case Timestamptz:
		return timestamptzOutput(v.integer, env), nil
	case Int2, Int4, Int8:
		return strconv.FormatInt(v.integer, 10), nil
	case Float4, Float8:
		return floatOutputWithEnvironment(v.floating, d.id, env), nil
	case Char:
		if len(v.text) > 0 && v.text[0] >= 128 {
			return fmt.Sprintf("\\%03o", v.text[0]), nil
		}
		return v.text, nil
	case Regtype:
		id, err := v.TypeOID()
		if err != nil {
			return "", err
		}
		if env.Regtype != nil {
			return env.Regtype.FormatRegtype(id)
		}
		return strconv.FormatUint(uint64(id), 10), nil
	case Regrole:
		id, err := v.RoleOID()
		if err != nil {
			return "", err
		}
		if env.Regrole != nil {
			return env.Regrole.FormatRegrole(id)
		}
		return strconv.FormatUint(uint64(id), 10), nil
	case Regconfig, Regdictionary:
		id, err := v.RegTSOID()
		if err != nil {
			return "", err
		}
		if id == 0 || env.RegTS == nil {
			return regTSNumericOutput(id), nil
		}
		if d.id == Regconfig {
			return env.RegTS.FormatRegconfig(id)
		}
		return env.RegTS.FormatRegdictionary(id)
	case Regoper, Regoperator:
		id, err := v.OperatorOID()
		if err != nil {
			return "", err
		}
		if env.Operator == nil {
			return strconv.FormatUint(uint64(id), 10), nil
		}
		if d.id == Regoper {
			return env.Operator.FormatRegoper(id)
		}
		return env.Operator.FormatRegoperator(id)
	case Regcollation, Regnamespace:
		return d.regObjectOutput(v, env)
	case Regproc, Regprocedure:
		id, err := v.ProcedureOID()
		if err != nil {
			return "", err
		}
		if env.Regproc == nil {
			return "", fmt.Errorf("missing regproc output snapshot")
		}
		if d.id == Regprocedure {
			return env.Regproc.FormatRegprocedure(id)
		}
		return env.Regproc.FormatRegproc(id)
	case Regclass:
		if env.Regclass != nil {
			if v.integer == 1 {
				return env.Regclass.FormatRegclass(v.oid)
			}
			n, err := strconv.ParseUint(v.text, 10, 32)
			if err != nil {
				return "", err
			}
			return env.Regclass.FormatRegclass(uint32(n))
		}
		return v.Text()
	case Refcursor, Text, BPChar, VarChar, Name, JSON, 2275:
		return v.text, nil
	case JSONB:
		if v.jsonb == nil {
			return "", fmt.Errorf("missing JSONB payload")
		}
		return v.text, nil
	case JSONPath:
		if v.jsonpath == nil {
			return "", fmt.Errorf("missing JSONPath payload")
		}
		return v.text, nil
	case Bool:
		if v.boolean {
			return "t", nil
		}
		return "f", nil
	}
	return "", fmt.Errorf("missing output provider")
}
func (d *Descriptor) SQLLiteral(ctx context.Context, v Value) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if v.typ != d || !d.Concrete() {
		return "", fmt.Errorf("invalid literal value")
	}
	if v.null {
		if d.BaseType().id == PgSnapshot {
			return "NULL::pg_snapshot", nil
		}
		if d.BaseType().id == PgLSN {
			return "NULL::pg_lsn", nil
		}
		if IsNetwork(d.BaseType().id) || d.BaseType().id == Tid || d.BaseType().id == Macaddr || d.BaseType().id == Macaddr8 {
			return "CAST(NULL AS " + d.BaseType().SQLName() + ")", nil
		}
		if d.BaseType().id == Money {
			return "CAST(NULL AS money)", nil
		}
		return "NULL", nil
	}
	if d.IsDomain() {
		v.typ = d.BaseType()
		return v.typ.SQLLiteral(ctx, v)
	}
	if d.IsComposite() {
		text, err := d.compositeOutput(ctx, v, OutputEnvironment{})
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	}
	if d.IsCatalogVector() {
		text, err := d.catalogVectorOutput(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsArray() || d.IsEnum() {
		var text string
		var err error
		if d.IsArray() {
			text, err = d.arrayOutput(ctx, v, OutputEnvironment{})
		} else {
			text, err = d.enumOutput(v)
		}
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	}
	if d.IsRange() || d.IsMultirange() {
		text, err := d.Output(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	}
	if d.IsSeg() {
		text, err := d.segOutput(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsHstore() {
		text, err := d.hstoreOutput(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	}
	if d.IsQueryInt() {
		text, err := d.queryIntOutput(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsLquery() {
		text, err := d.lqueryOutput(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsLtxtquery() {
		text, err := d.ltxtqueryOutput(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsLtree() {
		text, err := d.ltreeOutput(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsCube() {
		text, err := d.cubeOutput(ctx, v, OutputEnvironment{})
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsCitext() {
		return "'" + strings.ReplaceAll(v.text, "'", "''") + "'", nil
	}
	if d.IsEan13() {
		text, err := d.ean13Output(v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsIsbn() {
		text, err := d.isbnOutput(v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsIsbn13() {
		text, err := d.isbn13Output(v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsIsmn() {
		text, err := d.ismnOutput(v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsIsmn13() {
		text, err := d.ismn13Output(v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	}
	if d.IsIssn() || d.IsIssn13() {
		return d.issnSQLLiteral(v)
	}
	if d.IsUpc() {
		return d.upcSQLLiteral(v)
	}
	switch d.id {
	case Path:
		text, err := formatPath(v, OutputEnvironment{}, true)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	case Polygon:
		text, err := d.formatPolygon(ctx, v, OutputEnvironment{}, true)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	case Box, Point, Line, Lseg:
		text, err := d.geometryOutput(v, OutputEnvironment{})
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	case Circle:
		if v.circle.radius < 0 {
			return circleConstructorLiteral(v.circle), nil
		}
		return "'" + circleOutput(v.circle, OutputEnvironment{}) + "'", nil
	case TSVector, TSQuery:
		text, err := d.ftsOutput(v)
		if err != nil {
			return "", err
		}
		if d.id == TSQuery {
			text, err = v.ftsQuery.InputText(ctx)
			if err != nil {
				return "", err
			}
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	case Tid:
		text, err := tidOutput(v)
		if err != nil {
			return "", err
		}
		return "TID '" + text + "'", nil
	case Macaddr:
		text, err := macaddrOutput(v)
		if err != nil {
			return "", err
		}
		return "MACADDR '" + text + "'", nil
	case Macaddr8:
		text, err := macaddr8Output(v)
		if err != nil {
			return "", err
		}
		return "MACADDR8 '" + text + "'", nil
	case Inet, Cidr:
		text, err := d.Output(ctx, v)
		if err != nil {
			return "", err
		}
		return d.sql + " '" + text + "'", nil
	case Money:
		return "MONEY '" + moneyOutput(v.integer, Locale{}) + "'", nil
	case Bit, VarBit:
		return "B'" + v.text + "'", nil
	case Bytea:
		return byteaHex(v.text, true), nil
	case UUID:
		return "'" + uuidOutput(v.uuid) + "'", nil
	case Date:
		return "'" + dateOutput(v.integer) + "'", nil
	case Time:
		return "'" + timeOutput(v.integer) + "'", nil
	case Timetz:
		return timetzSQLLiteral(v), nil
	case Timestamptz:
		if v.integer == timestampEnd {
			return "TIMESTAMPTZ(0) '294276-12-31 23:59:59.999999+00'", nil
		}
		return "'" + timestamptzOutput(v.integer, OutputEnvironment{}) + "'", nil
	case Interval:
		return "INTERVAL '" + intervalCanonical(v) + "'", nil
	case Timestamp:
		if v.integer == timestampEnd {
			return "TIMESTAMP(0) '294276-12-31 23:59:59.999999'", nil
		}
		return "'" + timestampOutput(v.integer) + "'", nil
	case Char:
		text, err := d.Output(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	case Regtype:
		id, err := v.TypeOID()
		if err != nil {
			return "", err
		}
		return "'" + strconv.FormatUint(uint64(id), 10) + "'", nil
	case Regrole:
		id, err := v.RoleOID()
		if err != nil {
			return "", err
		}
		return "'" + strconv.FormatUint(uint64(id), 10) + "'", nil
	case Regconfig, Regdictionary:
		id, err := v.RegTSOID()
		if err != nil {
			return "", err
		}
		return "'" + regTSNumericOutput(id) + "'", nil
	case Regoper, Regoperator:
		id, err := v.OperatorOID()
		if err != nil {
			return "", err
		}
		return "'" + strconv.FormatUint(uint64(id), 10) + "'", nil
	case Regcollation, Regnamespace:
		id, err := v.ObjectOID()
		if err != nil {
			return "", err
		}
		return "'" + strconv.FormatUint(uint64(id), 10) + "'", nil
	case Regproc, Regprocedure:
		id, err := v.ProcedureOID()
		if err != nil {
			return "", err
		}
		return "'" + strconv.FormatUint(uint64(id), 10) + "'", nil
	case Regclass:
		text, err := v.Text()
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	case Refcursor, Text, BPChar, VarChar, Name, JSON, 2275:
		return "'" + strings.ReplaceAll(v.text, "'", "''") + "'", nil
	case JSONB, JSONPath:
		text, err := d.Output(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	case PgLSN:
		return "'" + pgLSNOutput(uint64(v.integer)) + "'::pg_lsn", nil
	case PgSnapshot:
		s, err := v.Snapshot()
		if err != nil {
			return "", err
		}
		return "'" + s.String() + "'::pg_snapshot", nil
	case Oid, Xid, Cid, Xid8, Float4, Float8, Numeric:
		text, err := d.Output(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	case Bool:
		if v.boolean {
			return "TRUE", nil
		}
		return "FALSE", nil
	}
	return d.Output(ctx, v)
}

var _ Codec = (*Descriptor)(nil)

type CoercionContext uint8

const (
	Implicit CoercionContext = iota
	Assignment
	Explicit
)

type coercionMethod uint8

const (
	identity coercionMethod = iota
	moneyConversion
	integerConversion
	viaIO
	booleanText
	floatConversion
	numericConversion
	integerBoolean
	booleanInteger
	floatIntegerConversion
	stringConversion
	nameConversion
	dateTimestampConversion
	oidConversion
	datetimeConversion
	charTextConversion
	bitStringConversion
	arrayConversion
	networkConversion
	networkTextConversion
	xid8Conversion
	jsonFormattedInput
	jsonReturningOutput
	timeIntervalConversion
	integerByteaConversion
	integerBitConversion
	internalCharConversion
	macConversion
	jsonbScalarConversion
	intervalTimeConversion
	geometryCastConversion
	rangeMultirangeConversion
	upcEanConversion
)

type CoercionPlan struct {
	source, target               Usage
	method                       coercionMethod
	context                      CoercionContext
	logicalSource, logicalTarget *Descriptor
	element                      *CoercionPlan
	binary                       BinaryCoercionPlan
	geometry                     GeometryConversionPlan
	jsonEncoding                 string
	jsonUnique                   bool
	// jsonReturning owns a constructor output conversion, not an enclosing assignment.
	jsonReturning bool
	unknownInput  bool // Genuine output-to-TEXT-input transport for logical UNKNOWN.
}

// ChecksDomain reports runtime CHECK demand, not free VALUE-frame dependence.
// Identity/downcasts do not check; array element conversions share this rule.
func (p CoercionPlan) ChecksDomain() bool {
	return p.logicalTarget != nil && p.logicalTarget != p.logicalSource && p.logicalTarget.HasDomainConstraints() ||
		p.element != nil && p.element.ChecksDomain()
}

func (p CoercionPlan) Equal(other CoercionPlan) bool {
	child, next := p.element, other.element
	p.element, other.element = nil, nil
	if p != other {
		return false
	}
	if child == nil || next == nil {
		return child == next
	}
	return child.Equal(*next)
}

// RequiresCatalogOutput follows the selected operation, including recursive
// array/domain output, rather than the destination's spelling. Identity and
// numeric OID conversions never invoke symbolic output.
func (p CoercionPlan) RequiresCatalogOutput() bool {
	if p.element != nil {
		return p.element.RequiresCatalogOutput()
	}
	return p.method == viaIO && p.source.Type.ContainsCatalogIdentifier()
}

// FunctionCastShortcut reports the existing relabel/I/O paths admitted by
// PostgreSQL's type-name-call step, distinct from ordinary function casts.
func (p CoercionPlan) FunctionCastShortcut() bool {
	switch p.method {
	case identity, bitStringConversion:
		return true
	case viaIO:
		return !p.source.Type.IsComposite()
	case oidConversion:
		return p.source.Type.id != Int2 && p.source.Type.id != Int8 && p.target.Type.id != Int8
	case networkConversion:
		return p.source.Type.id == Cidr && p.target.Type.id == Inet
	case stringConversion:
		return p.source.Type.id != BPChar && p.source.Type.id != Name
	}
	return false
}

// FunctionExpression reports a cast/typmod function in the selected coercion.
// SQL/JSON DEFAULT validation visits an array coercion's element expression as
// well as its input, so an element function can establish an accepted shape.
func (p CoercionPlan) FunctionExpression() bool {
	switch p.method {
	case upcEanConversion:
		return p.source.Type.IsEan13() && p.target.Type.IsUpc()
	case arrayConversion:
		return p.element != nil && p.element.FunctionExpression()
	case viaIO, jsonFormattedInput, jsonReturningOutput:
		return false
	case identity:
		return p.target.Typmod >= 0 && p.target.Typmod != p.source.Typmod
	case bitStringConversion:
		return p.target.Typmod >= 0
	case networkConversion:
		return !(p.source.Type.id == Cidr && p.target.Type.id == Inet)
	case oidConversion:
		// OID aliases share the four-byte representation. Only widening or
		// narrowing an actual integer needs a cast function.
		return p.source.Type.id == Int2 || p.source.Type.id == Int8 || p.target.Type.id == Int8
	case stringConversion:
		// TEXT/VARCHAR are binary relabels except for a requested typmod;
		// BPCHAR removal and NAME conversion use real functions.
		return p.source.Type.id == BPChar || p.source.Type.id == Name ||
			p.target.Typmod >= 0
	default:
		return true
	}
}

// EvaluationFunctionCount exposes only the existing coercion representation:
// a relabel, one cast function, or the output/input pair of an IO coercion.
// OVERLAPS uses it for the released duplicated-argument inline threshold.
func (p CoercionPlan) EvaluationFunctionCount() int {
	if p.element != nil {
		return p.element.EvaluationFunctionCount()
	}
	if p.method == networkConversion && p.source.Type.id == Cidr && p.target.Type.id == Inet {
		return 0 // Binary-compatible relabel, not a cast function.
	}
	if p.method == viaIO {
		return 2
	}
	if !p.FunctionExpression() {
		return 0
	}
	return 1
}

type castSpec struct {
	source, target ID
	minimum        CoercionContext
	method         coercionMethod
	catalog        castCatalogFact
}

// Fixed provider definitions are read-only; identity/ownership comes from the
// source and target catalog descriptors retained by each bound plan.
var baselineCasts = [...]castSpec{
	{Int2, Oid, Implicit, oidConversion, castCatalogFact{20030, 313, 'i', 'f'}},
	{Int4, Oid, Implicit, oidConversion, castCatalogFact{20031, 0, 'i', 'b'}},
	{Int8, Oid, Implicit, oidConversion, castCatalogFact{20032, 1287, 'i', 'f'}},
	{Regclass, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regclass, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regclass, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regclass, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regclass, Implicit, oidConversion, castCatalogFact{}},
	{Regclass, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regclass, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Text, Regclass, Implicit, viaIO, castCatalogFact{}},
	{VarChar, Regclass, Implicit, viaIO, castCatalogFact{}},
	{Oid, Int4, Assignment, oidConversion, castCatalogFact{20033, 0, 'a', 'b'}},
	{Oid, Int8, Assignment, oidConversion, castCatalogFact{20034, 1288, 'a', 'f'}},
	{Xid8, Xid, Explicit, xid8Conversion, castCatalogFact{20069, 5071, 'e', 'f'}},
	{Regtype, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regtype, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regtype, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regtype, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regtype, Implicit, oidConversion, castCatalogFact{}},
	{Regtype, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regtype, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Regrole, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regrole, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regrole, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regrole, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regrole, Implicit, oidConversion, castCatalogFact{}},
	{Regrole, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regrole, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Regoper, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regoper, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regoper, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regoper, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regoper, Implicit, oidConversion, castCatalogFact{}},
	{Regoper, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regoper, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Regoperator, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regoperator, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regoperator, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regoperator, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regoperator, Implicit, oidConversion, castCatalogFact{}},
	{Regoperator, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regoperator, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Regoper, Regoperator, Implicit, oidConversion, castCatalogFact{}},
	{Regoperator, Regoper, Implicit, oidConversion, castCatalogFact{}},
	{Regcollation, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regcollation, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regcollation, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regcollation, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regcollation, Implicit, oidConversion, castCatalogFact{}},
	{Regcollation, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regcollation, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Regnamespace, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regnamespace, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regnamespace, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regnamespace, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regnamespace, Implicit, oidConversion, castCatalogFact{}},
	{Regnamespace, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regnamespace, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Regconfig, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regconfig, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regconfig, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regconfig, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regconfig, Implicit, oidConversion, castCatalogFact{}},
	{Regconfig, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regconfig, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Regdictionary, Oid, Implicit, oidConversion, castCatalogFact{}},
	{Oid, Regdictionary, Implicit, oidConversion, castCatalogFact{}},
	{Int2, Regdictionary, Implicit, oidConversion, castCatalogFact{}},
	{Int4, Regdictionary, Implicit, oidConversion, castCatalogFact{}},
	{Int8, Regdictionary, Implicit, oidConversion, castCatalogFact{}},
	{Regdictionary, Int4, Assignment, oidConversion, castCatalogFact{}},
	{Regdictionary, Int8, Assignment, oidConversion, castCatalogFact{}},
	{Date, Timestamp, Implicit, dateTimestampConversion, castCatalogFact{20035, 2024, 'i', 'f'}},
	{Date, Timestamptz, Implicit, datetimeConversion, castCatalogFact{20036, 1174, 'i', 'f'}},
	{Time, Timetz, Implicit, datetimeConversion, castCatalogFact{20037, 2047, 'i', 'f'}},
	{Time, Interval, Implicit, timeIntervalConversion, castCatalogFact{20072, 1370, 'i', 'f'}},
	{Timestamp, Date, Assignment, datetimeConversion, castCatalogFact{20038, 2029, 'a', 'f'}},
	{Timestamp, Time, Assignment, datetimeConversion, castCatalogFact{20039, 1316, 'a', 'f'}},
	{Timestamp, Timestamptz, Implicit, datetimeConversion, castCatalogFact{20040, 2028, 'i', 'f'}},
	{Timestamptz, Date, Assignment, datetimeConversion, castCatalogFact{20041, 1178, 'a', 'f'}},
	{Timestamptz, Time, Assignment, datetimeConversion, castCatalogFact{20042, 2019, 'a', 'f'}},
	{Timestamptz, Timestamp, Assignment, datetimeConversion, castCatalogFact{20043, 2027, 'a', 'f'}},
	{Timestamptz, Timetz, Assignment, datetimeConversion, castCatalogFact{20044, 1388, 'a', 'f'}},
	{Timetz, Time, Assignment, datetimeConversion, castCatalogFact{20045, 2046, 'a', 'f'}},
	{Name, Text, Implicit, stringConversion, castCatalogFact{}},
	{Name, BPChar, Assignment, stringConversion, castCatalogFact{}},
	{Name, VarChar, Assignment, stringConversion, castCatalogFact{}},
	{Text, Name, Implicit, nameConversion, castCatalogFact{}},
	{BPChar, Name, Implicit, nameConversion, castCatalogFact{}},
	{VarChar, Name, Implicit, nameConversion, castCatalogFact{}},
	{Char, Text, Implicit, charTextConversion, castCatalogFact{20070, 946, 'i', 'f'}},
	{PgNodeTree, Text, Implicit, viaIO, castCatalogFact{20071, 0, 'i', 'b'}},
	{Int2, Int4, Implicit, integerConversion, castCatalogFact{20000, 313, 'i', 'f'}},
	{Int2, Int8, Implicit, integerConversion, castCatalogFact{20001, 754, 'i', 'f'}},
	{Int4, Int2, Assignment, integerConversion, castCatalogFact{20005, 314, 'a', 'f'}},
	{Int8, Int2, Assignment, integerConversion, castCatalogFact{20010, 714, 'a', 'f'}},
	{Int2, Float4, Implicit, floatConversion, castCatalogFact{20002, 236, 'i', 'f'}},
	{Int2, Float8, Implicit, floatConversion, castCatalogFact{20003, 235, 'i', 'f'}},
	{Int4, Int8, Implicit, integerConversion, castCatalogFact{20006, 481, 'i', 'f'}},
	{Int8, Int4, Assignment, integerConversion, castCatalogFact{20011, 480, 'a', 'f'}},
	{Int4, Float4, Implicit, floatConversion, castCatalogFact{20007, 318, 'i', 'f'}},
	{Int4, Float8, Implicit, floatConversion, castCatalogFact{20008, 316, 'i', 'f'}},
	{Int8, Float4, Implicit, floatConversion, castCatalogFact{20012, 652, 'i', 'f'}},
	{Int8, Float8, Implicit, floatConversion, castCatalogFact{20013, 482, 'i', 'f'}},
	{Float4, Float8, Implicit, floatConversion, castCatalogFact{20018, 311, 'i', 'f'}},
	{Float8, Float4, Assignment, floatConversion, castCatalogFact{20023, 312, 'a', 'f'}},
	{Float4, Int2, Assignment, floatIntegerConversion, castCatalogFact{20015, 238, 'a', 'f'}},
	{Float4, Int4, Assignment, floatIntegerConversion, castCatalogFact{20016, 319, 'a', 'f'}},
	{Float4, Int8, Assignment, floatIntegerConversion, castCatalogFact{20017, 653, 'a', 'f'}},
	{Float8, Int2, Assignment, floatIntegerConversion, castCatalogFact{20020, 237, 'a', 'f'}},
	{Float8, Int4, Assignment, floatIntegerConversion, castCatalogFact{20021, 317, 'a', 'f'}},
	{Float8, Int8, Assignment, floatIntegerConversion, castCatalogFact{20022, 483, 'a', 'f'}},
	{Float4, Text, Assignment, viaIO, castCatalogFact{}},
	{Float8, Text, Assignment, viaIO, castCatalogFact{}},
	{Int4, Numeric, Implicit, numericConversion, castCatalogFact{20009, 1740, 'i', 'f'}},
	{Int8, Numeric, Implicit, numericConversion, castCatalogFact{20014, 1781, 'i', 'f'}},
	{Int2, Numeric, Implicit, numericConversion, castCatalogFact{20004, 1782, 'i', 'f'}},
	{Numeric, Int2, Assignment, numericConversion, castCatalogFact{20025, 1783, 'a', 'f'}},
	{Numeric, Int4, Assignment, numericConversion, castCatalogFact{20026, 1744, 'a', 'f'}},
	{Numeric, Int8, Assignment, numericConversion, castCatalogFact{20027, 1779, 'a', 'f'}},
	{Numeric, Float4, Implicit, numericConversion, castCatalogFact{20028, 1745, 'i', 'f'}},
	{Numeric, Float8, Implicit, numericConversion, castCatalogFact{20029, 1746, 'i', 'f'}},
	{Float4, Numeric, Assignment, numericConversion, castCatalogFact{20019, 1742, 'a', 'f'}},
	{Float8, Numeric, Assignment, numericConversion, castCatalogFact{20024, 1743, 'a', 'f'}},
	{Money, Numeric, Assignment, moneyConversion, castCatalogFact{20065, 3823, 'a', 'f'}},
	{Numeric, Money, Assignment, moneyConversion, castCatalogFact{20066, 3824, 'a', 'f'}},
	{Int4, Money, Assignment, moneyConversion, castCatalogFact{20067, 3811, 'a', 'f'}},
	{Int8, Money, Assignment, moneyConversion, castCatalogFact{20068, 3812, 'a', 'f'}},
}

func BaselineCastType(id ID) bool {
	return id == Int4 || id == Int8 || id == Text || id == Bool
}

// StoredScalarCastType is the concrete annotation surface of stored expressions.
// Admission of a type does not add conversion edges to PlanCoercion.
func StoredScalarCastType(id ID) bool {
	switch id {
	case Refcursor, TSVector, TSQuery, Box, Point, Lseg, Circle, Line, Path, Polygon:
		return true
	case PgNDistinct, PgDependencies, PgMCVList:
		return true
	case PgSnapshot, PgLSN:
		return true
	case Bool, Int2, Int2Vector, OidVector, Int4, Int8, Oid, Tid, Macaddr, Macaddr8, Xid, Cid, Xid8, Regclass, Text, Name, BPChar, VarChar, Bytea, UUID,
		Numeric, Money, Float4, Float8, Date, Time, Timetz, Timestamp, Timestamptz, Interval, JSON, JSONB, JSONPath, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary, Inet, Cidr, ACLItem:
		return true
	}
	return false
}

// PlanCoercion selects a direct released edge before generic array or string I/O
// rules. An ineligible direct edge is a barrier, never a transitive invitation.
func PlanCoercion(source, target Usage, context CoercionContext) (CoercionPlan, error) {
	if source.CatalogNullUsage() && target.Valid() && target.Type.ID() == Text && context == Explicit {
		return CoercionPlan{source: source, target: target, context: context, method: viaIO}, nil
	}
	if !source.Valid() || !target.Valid() {
		return CoercionPlan{}, fmt.Errorf("invalid coercion usage")
	}
	if source.Type.IsDomain() || target.Type.IsDomain() {
		p, err := PlanCoercion(source.BaseUsage(), target.BaseUsage(), context)
		p.logicalSource, p.logicalTarget = source.Type, target.Type
		return p, err
	}
	p := CoercionPlan{source: source, target: target, context: context}
	if source.Type.IsUpc() && target.Type.IsEan13() || source.Type.IsEan13() && target.Type.IsUpc() {
		if context < Assignment || target.Type.IsUpc() && context != Explicit {
			return p, failure("42804", "incompatible source and destination types")
		}
		p.method = upcEanConversion
		return p, nil
	}
	if source.Type == target.Type {
		return p, nil
	}
	if source.Type.IsCitext() || target.Type.IsCitext() {
		return p.planCitext()
	}
	if IsRoutineAlias(source.Type.id) || IsRoutineAlias(target.Type.id) {
		from, to := source.Type.id, target.Type.id
		if from == Oid || from == Int2 || from == Int4 || from == Int8 {
			p.method = oidConversion
			return p, nil
		}
		if to == Oid || context >= Assignment && (to == Int4 || to == Int8) {
			p.method = oidConversion
			return p, nil
		}
	}
	for _, specs := range [][]castSpec{baselineCasts[:], releasedCasts[:]} {
		for _, spec := range specs {
			if spec.source == source.Type.id && spec.target == target.Type.id {
				if context < spec.minimum {
					return p, failure("42804", "incompatible source and destination types")
				}
				p.method = spec.method
				if p.method == geometryCastConversion {
					for op := BoxCircle; op <= PolygonPath; op++ {
						if op.Arity() == 1 && op.Formal(0) == source.Type.id && op.ResultID() == target.Type.id {
							bound, err := BindGeometryConversion(op, target.Type.Usage(), [2]Usage{source.Type.Usage()})
							p.geometry = bound
							return p, err
						}
					}
					return p, fmt.Errorf("missing geometry cast provider")
				}
				return p, nil
			}
		}
	}
	if source.Type.IsRange() && target.Type.IsMultirange() && source.Type.multirangeCompanion == target.Type && target.Type.validMultirangeAuthority() {
		if context == Explicit {
			p.method = rangeMultirangeConversion
			return p, nil
		}
		return p, failure("42804", "incompatible source and destination types")
	}
	if source.Type.IsArray() && target.Type.IsArray() {
		if target.Type.IsCatalogVector() {
			return p, failure("42846", "cannot cast type "+source.Type.DiagnosticName()+" to "+target.Type.DiagnosticName())
		}
		from, to := source.Type.Element().Usage(), target.Type.Element().Usage()
		from.Typmod, to.Typmod = source.Typmod, target.Typmod
		from.Collation, to.Collation = source.Collation, target.Collation
		child, err := PlanCoercion(from, to, context)
		if err != nil {
			return p, err
		}
		p.method, p.element = arrayConversion, &child
		return p, nil
	}
	if context >= Assignment && castStringCategory(target.Type) || context == Explicit && castStringCategory(source.Type) {
		p.method = viaIO
		return p, nil
	}
	return p, failure("42804", "incompatible source and destination types")
}
func (p CoercionPlan) applyBase(ctx context.Context, v Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if p.binary.method != binaryCoercionNone {
		return p.binary.Apply(ctx, v)
	}
	if input.ExactSnapshot {
		return p.applyExactSnapshot(ctx, v, input, output)
	}
	return p.applyBaseUnchecked(ctx, v, input, output)
}

func (p CoercionPlan) applyBaseUnchecked(ctx context.Context, v Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if v.typ != p.source.Type || !p.target.Type.Concrete() {
		return Value{}, fmt.Errorf("foreign coercion value")
	}
	if v.null {
		return Null(p.target.Type), nil
	}
	switch p.method {
	case upcEanConversion:
		return p.applyUpcEan(v)
	case integerByteaConversion, integerBitConversion, internalCharConversion,
		macConversion, jsonbScalarConversion, intervalTimeConversion,
		geometryCastConversion, rangeMultirangeConversion:
		return p.applyReleased(ctx, v, input, output)
	case xid8Conversion:
		return XIDValue(p.target.Type, uint32(v.integer))
	case jsonFormattedInput:
		text := v.text
		var err error
		if p.source.Type.id == Bytea {
			encoding := p.jsonEncoding
			if encoding == "" {
				encoding = "UTF8"
			}
			text, err = ConvertBinaryEncoding(ctx, text, encoding, "UTF8")
		} else {
			text, err = p.source.Type.OutputWithEnvironment(ctx, v, output)
		}
		if err != nil {
			return Value{}, err
		}
		if p.jsonUnique {
			scan := jsonScanResult{classify: true, unique: true}
			if _, err := p.target.Type.scanJSON(ctx, text, &scan); err != nil {
				return Value{}, err
			}
			if scan.duplicate {
				return Value{}, failure("22030", "duplicate JSON object key value")
			}
			return Value{typ: p.target.Type, text: text}, nil
		}
		return p.target.Type.InputWithEnvironment(ctx, text, p.target.Typmod, input)
	case jsonReturningOutput:
		text, err := p.source.Type.OutputWithEnvironment(ctx, v, output)
		if err != nil {
			return Value{}, err
		}
		if p.target.Type.id == Bytea {
			return Binary(p.target.Type, text)
		}
		return p.target.Type.InputWithEnvironment(ctx, text, p.target.Typmod, input)
	case networkConversion:
		v.typ = p.target.Type
		if v.typ.id == Cidr {
			v.network = v.network.masked()
		}
		return v, nil
	case networkTextConversion:
		return sizeCharacter(String(p.target.Type, v.network.output(true)), p.target.Typmod, p.context == Explicit)
	case moneyConversion:
		return moneyCoercion(v, p.target, input.MonetaryLocale)
	case arrayConversion:
		// PostgreSQL's array_map normalizes special empty headers for an
		// evaluated element expression, unlike a binary relabel. An already
		// rank-zero empty payload remains immutable and can be shared.
		if v.array != nil && len(v.array.dimensions) != 0 && len(v.array.elements) == 0 && !p.element.arrayElementRelabel() {
			return ownedArray(p.target.Type, nil, nil)
		}
		return v.MapArray(p.target.Type, func(element Value, _ *Descriptor) (Value, error) {
			return p.element.Apply(ctx, element, input, output)
		})
	case bitStringConversion:
		v.typ = p.target.Type
		return sizeCastBits(v, p.target.Typmod, p.context == Explicit)
	case timeIntervalConversion:
		return IntervalValue(p.target.Type, 0, 0, v.integer)
	case charTextConversion:
		text, err := p.source.Type.ConcatOutput(ctx, v, output)
		if err != nil {
			return Value{}, err
		}
		return String(p.target.Type, text), nil
	case oidConversion:
		return oidCoercion(v, p.target.Type)
	case datetimeConversion:
		return temporalConversion(v, p.target, input)
	case dateTimestampConversion:
		if v.integer == DateNegativeInfinity || v.integer == DatePositiveInfinity {
			return TimestampValue(p.target.Type, v.integer)
		}
		if v.integer < timestampMinimum/microsPerDay || v.integer >= timestampEnd/microsPerDay {
			return Value{}, failure("22008", "date out of range for timestamp")
		}
		return TimestampValue(p.target.Type, v.integer*microsPerDay)
	case identity:
		if p.target.Type.IsArray() {
			return p.sizeArray(ctx, v, input, output)
		}
		if p.target.Type.id == Interval {
			return adjustInterval(v, p.target.Typmod)
		}
		if isBitString(p.target.Type.id) {
			return sizeCastBits(v, p.target.Typmod, p.context == Explicit)
		}
		if IsCharacter(p.target.Type.id) {
			return sizeCharacter(v, p.target.Typmod, p.context == Explicit)
		}
		if p.target.Type.id == Numeric {
			return sizeNumeric(v, p.target.Typmod)
		}
		if p.target.Type.id == Timestamp || p.target.Type.id == Time || p.target.Type.id == Timestamptz || p.target.Type.id == Timetz {
			v.integer = roundTimestamp(v.integer, p.target.Typmod)
		}
		return v, nil
	case nameConversion:
		text := clipName(v.text)
		if v.typ.id == BPChar {
			text = strings.TrimRight(text, " ")
		}
		return String(p.target.Type, text), nil
	case stringConversion:
		if v.typ.id == BPChar && p.target.Type.id != BPChar {
			v.text = strings.TrimRight(v.text, " ")
		}
		v.typ = p.target.Type
		return sizeCharacter(v, p.target.Typmod, p.context == Explicit)
	case numericConversion:
		return numericCoercion(ctx, v, p.target)
	case integerConversion:
		return Integer(p.target.Type, v.integer)
	case integerBoolean:
		return Boolean(p.target.Type, v.integer != 0), nil
	case booleanInteger:
		var n int64
		if v.boolean {
			n = 1
		}
		return Integer(p.target.Type, n)
	case floatIntegerConversion:
		n := math.RoundToEven(v.floating)
		limit, name := float64(2147483648), "integer"
		switch p.target.Type.id {
		case Int2:
			limit, name = 32768, "smallint"
		case Int8:
			limit, name = 9223372036854775808, "bigint"
		}
		// Check the rounded float before converting: +2^63 cannot be represented
		// by int64, while the exactly represented -2^63 is a valid minimum.
		if math.IsNaN(n) || n < -limit || n >= limit {
			return Value{}, failure("22003", name+" out of range")
		}
		return Integer(p.target.Type, int64(n))
	case floatConversion:
		n := v.floating
		if v.typ.id == Int2 || v.typ.id == Int4 || v.typ.id == Int8 {
			n = float64(v.integer)
			if p.target.Type.id == Float4 {
				n = float64(float32(v.integer))
			}
		}
		return Float(p.target.Type, n)
	case viaIO:
		text, err := p.source.Type.OutputWithEnvironment(ctx, v, output)
		if err != nil {
			return Value{}, err
		}
		value, err := p.target.Type.InputWithEnvironment(ctx, text, -1, input)
		if err != nil {
			return Value{}, err
		}
		sizing := CoercionPlan{source: p.target, target: p.target, context: p.context}
		return sizing.applyBaseUnchecked(ctx, value, input, output)
	case booleanText:
		// Boolean string conversion uses true/false rather than boolout's t/f.
		return sizeCharacter(String(p.target.Type, strconv.FormatBool(v.boolean)), p.target.Typmod, p.context == Explicit)
	}
	return Value{}, fmt.Errorf("invalid coercion plan")
}

type Comparison struct {
	left, right            *Descriptor
	defaultBtreeOpclass    uint32
	orderingBound          bool
	inputCollation         CollationRef
	collationIndeterminate bool
	element                *Comparison
	composite              *compositeComparison
	missingEquality        bool
	missingOrdering        bool
}

// DefaultBtreeOpclass identifies the provider admitted by BindOrdering.
// Comparisons bound for scalar equality do not imply an ordering provider.
func (p Comparison) DefaultBtreeOpclass() uint32 { return p.defaultBtreeOpclass }

// OrderingBound distinguishes an admitted sort provider from plain comparison.
// Occurrence records and bounded vector sorting have no physical index opclass.
func (p Comparison) OrderingBound() bool { return p.orderingBound }

// InputCollation is the retained comparison authority, independent of BOOL
// result metadata. The flag defers implicit conflicts until a comparison occurs.
func (p Comparison) InputCollation() (CollationRef, bool) {
	return p.inputCollation, p.collationIndeterminate
}

// BindOrdering validates the actual builtin provider and bounded C collation
// before any rows are read, including empty and all-NULL inputs.
func BindOrdering(u Usage) (Comparison, error) {
	if !u.Type.Concrete() {
		return Comparison{}, fmt.Errorf("invalid ordering usage")
	}
	if !u.validCollation() {
		return Comparison{}, fmt.Errorf("invalid ordering collation")
	}
	if u.Type.BaseType().IsCatalogVector() || u.Type.id == 2249 && u.Type.IsComposite() || u.Type.id == 2287 && u.Type.IsArray() && u.Type.Element().id == 2249 && u.Type.Element().IsComposite() {
		p, err := BindComparison(u.Type, u.Type, u.Collation, false)
		if err != nil {
			return Comparison{}, err
		}
		p.orderingBound = true
		return p, nil
	}
	if u.Type.BaseType().IsArray() || u.Type.BaseType().id == Box || u.Type.BaseType().id == Point || u.Type.BaseType().id == Line || u.Type.BaseType().id == Lseg || u.Type.BaseType().id == Path || u.Type.BaseType().id == JSON || u.Type.BaseType().id == JSONPath || u.Type.BaseType().id == Xid || u.Type.BaseType().id == Cid {
		return Comparison{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type " + u.Type.DiagnosticName(), Hint: "Use an explicit ordering operator or modify the query."}
	}
	if !u.Type.BaseType().IsEnum() {
		switch u.Type.BaseType().id {
		case Bool, BPChar, Bytea, Char, Date, Float4, Float8, Int2, Int4, Int8, Money,
			Interval, Name, Numeric, Oid, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary, Text, VarChar, Time,
			Timestamp, Timestamptz, Timetz, UUID, JSONB, Inet, Cidr, Xid8, PgLSN, Tid, Macaddr, PgNodeTree:
		default:
			message := "could not identify an ordering operator for type " + u.Type.DiagnosticName()
			return Comparison{}, &sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883",
				Message: message,
				Hint:    "Use an explicit ordering operator or modify the query.",
			}
		}
	}
	class, ok := DefaultIndexClass(u.Type)
	if !ok || class.OID() == 0 {
		return Comparison{}, fmt.Errorf("invalid ordering class identity")
	}
	if u.Type.BaseType().id == PgNodeTree && u.Collation.ID == 0 {
		u.Collation = CollationRef{ID: 100}
	}
	return Comparison{left: u.Type, right: u.Type, defaultBtreeOpclass: class.OID(), orderingBound: true, inputCollation: u.Collation}, nil
}

func (p Comparison) SessionDependent() bool {
	return p.left != nil && p.right != nil && p.left.BaseType().id != p.right.BaseType().id && (p.left.BaseType().id == Timestamptz || p.right.BaseType().id == Timestamptz)
}

// CheckComparisonTypes validates SQL operand compatibility during analysis,
// before contextual input conversion. Array signatures are independent of
// demand-driven element capabilities; BindComparison retains their authority.
func CheckComparisonTypes(left, right *Descriptor) error {
	_, err := bindComparisonTypes(left, right)
	return err
}

// CheckComparisonOperator separates SQL operator admission from datum providers.
func CheckComparisonOperator(operator ComparisonOperator, left, right *Descriptor) error {
	_, err := ResolveComparison(operator, left, right)
	return err
}

func BindComparison(left, right *Descriptor, collation CollationRef, indeterminate bool) (Comparison, error) {
	p, err := bindComparisonTypes(left, right)
	if err != nil {
		return p, err
	}
	collatable := left.Usage().Collation.ID != 0 || right.Usage().Collation.ID != 0
	if collatable && !collation.Valid() || !collatable && (collation != (CollationRef{}) || indeterminate) {
		return Comparison{}, fmt.Errorf("invalid comparison collation")
	}
	p.inputCollation, p.collationIndeterminate = collation, indeterminate
	if left.BaseType().IsArray() {
		p.element, err = bindArrayComparisonElement(left.BaseType(), collation, indeterminate)
		if err != nil {
			return Comparison{}, err
		}
	}
	if left.BaseType().IsComposite() && right.BaseType().IsComposite() {
		p.composite = bindCompositeComparison(left, right, collation, indeterminate)
	}
	return p, nil
}

func bindComparisonTypes(left, right *Descriptor) (Comparison, error) {
	p := Comparison{left: left, right: right}
	if !left.Concrete() || !right.Concrete() {
		return p, fmt.Errorf("invalid comparison usage")
	}
	if left.BaseType().IsComposite() || right.BaseType().IsComposite() || left.IsDeferredRecord() || right.IsDeferredRecord() {
		if !(left.BaseType().IsComposite() || left.IsDeferredRecord()) || !(right.BaseType().IsComposite() || right.IsDeferredRecord()) {
			return p, failure("42883", "incompatible comparison operands")
		}
		return p, nil
	}
	if left.BaseType().IsArray() || right.BaseType().IsArray() {
		if !left.BaseType().IsArray() || left.BaseType() != right.BaseType() {
			return p, failure("42883", "incompatible comparison operands")
		}
		return p, nil
	}
	// Enum operators are polymorphic over actual enum types. Domains are not
	// enum inputs, even when both operands name the same domain over an enum.
	if left.BaseType().IsEnum() || right.BaseType().IsEnum() {
		if !left.IsEnum() || !right.IsEnum() || left != right {
			return p, failure("42883", "incompatible comparison operands")
		}
		return p, nil
	}
	if left.IsDomain() || right.IsDomain() {
		_, err := bindComparisonTypes(left.BaseType(), right.BaseType())
		return p, err
	}
	if left.id == JSON || right.id == JSON || left.id == JSONPath || right.id == JSONPath {
		return p, failure("42883", "incompatible comparison operands")
	}
	if left.IsRange() || right.IsRange() || left.IsMultirange() || right.IsMultirange() {
		if left != right {
			return p, failure("42883", "incompatible comparison operands")
		}
		return p, nil
	}
	if IsTransactionIdentifier(left.id) || IsTransactionIdentifier(right.id) {
		if left == right || left.id == Xid && right.id == Int4 {
			return p, nil
		}
		return p, failure("42883", "incompatible comparison operands")
	}
	if IsNetwork(left.id) && IsNetwork(right.id) {
		return p, nil
	}
	if dateTimestampType(left.id) && dateTimestampType(right.id) {
		return p, nil
	}
	if IsCharacter(left.id) && IsCharacter(right.id) {
		return p, nil
	}
	if left.id == Name && right.id == Text || left.id == Text && right.id == Name {
		return p, nil
	}
	if exactNumber(left.id) && exactNumber(right.id) {
		return p, nil
	}
	if (isFloat(left.id) && numericFloatOperand(right.id)) || (isFloat(right.id) && numericFloatOperand(left.id)) {
		return p, nil
	}
	if left.id == Box && right.id == Box || left.id == Lseg && right.id == Lseg {
		return p, nil
	}
	if left == right {
		equality, ordering := ComparisonCapabilities(left)
		if equality || ordering {
			p.missingOrdering = !ordering
			return p, nil
		}
	}
	return p, failure("42883", "incompatible comparison operands")
}
func (p Comparison) Compare(ctx context.Context, a, b Value, input InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.typ != p.left || b.typ != p.right || a.null || b.null || !p.left.Concrete() {
		return 0, fmt.Errorf("invalid comparison values")
	}
	if err := p.demandOrdering(input); err != nil {
		return 0, err
	}
	if p.element != nil {
		return p.compareArrays(ctx, a, b, input)
	}
	if p.composite != nil {
		return p.compareComposites(ctx, a, b, input, true)
	}
	if p.collationIndeterminate {
		return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
	}
	a.typ, b.typ = p.left.BaseType(), p.right.BaseType()
	p.left, p.right = a.typ, b.typ
	if p.left.IsHstore() {
		if !a.validHstore() || !b.validHstore() {
			return 0, fmt.Errorf("invalid hstore comparison payload")
		}
		return compareHstore(ctx, a.hstore, b.hstore)
	}
	if p.left.IsCube() {
		if !a.validCube() || !b.validCube() {
			return 0, fmt.Errorf("invalid cube comparison payload")
		}
		return compareCube(a.cube, b.cube), nil
	}
	if p.left.IsRange() {
		return p.left.compareRangeValues(ctx, a.rangeDatum, b.rangeDatum)
	}
	if p.left.IsMultirange() {
		if a.multirange == nil || b.multirange == nil {
			return 0, fmt.Errorf("missing multirange comparison payload")
		}
		return p.left.multirangeRange.compareMultirangeValues(ctx, a.multirange.members, b.multirange.members)
	}
	if p.left.IsEnum() {
		if _, err := p.left.enumOutput(a); err != nil {
			return 0, err
		}
		if _, err := p.right.enumOutput(b); err != nil {
			return 0, err
		}
		if p.left.enum.ranks[a.integer] < p.right.enum.ranks[b.integer] {
			return -1, nil
		}
		if p.left.enum.ranks[a.integer] > p.right.enum.ranks[b.integer] {
			return 1, nil
		}
		return 0, nil
	}
	if p.left.id == JSONB {
		if a.jsonb == nil || b.jsonb == nil {
			return 0, fmt.Errorf("missing JSONB payload")
		}
		return compareJSONB(ctx, &a.jsonb.root, &b.jsonb.root, false)
	}
	if p.left.id == PgNodeTree {
		// Reuse the represented binary TEXT conversion, including prepared
		// grouping keys. No input parser, descriptor lookup or retagging is
		// needed, and structural-output failures are not overload retries.
		left, err := p.left.Output(ctx, a)
		if err != nil {
			return 0, err
		}
		right, err := p.right.Output(ctx, b)
		if err != nil {
			return 0, err
		}
		return p.inputCollation.CompareText(ctx, left, right)
	}
	if p.left.id != p.right.id && dateTimestampType(p.left.id) && dateTimestampType(p.right.id) {
		return compareDatetime(a, b, input), nil
	}
	if IsCharacter(p.left.id) && IsCharacter(p.right.id) {
		left, right := a.text, b.text
		if p.left.id == BPChar {
			left = strings.TrimRight(left, " ")
		}
		if p.right.id == BPChar {
			right = strings.TrimRight(right, " ")
		}
		if p.left.id == BPChar && p.right.id == VarChar || p.right.id == BPChar && p.left.id == VarChar {
			left, right = strings.TrimRight(left, " "), strings.TrimRight(right, " ")
		}
		return p.inputCollation.CompareText(ctx, left, right)
	}
	if p.left.id == Name && p.right.id == Text || p.left.id == Text && p.right.id == Name {
		return p.inputCollation.CompareText(ctx, a.text, b.text)
	}
	if isFloat(p.left.id) || isFloat(p.right.id) {
		left, err := checkedFloatOperand(ctx, a)
		if err != nil {
			return 0, err
		}
		right, err := checkedFloatOperand(ctx, b)
		if err != nil {
			return 0, err
		}
		return compareFloats(left, right), nil
	}
	if p.left.id == Numeric || p.right.id == Numeric {
		return compareNumeric(a, b), nil
	}
	if p.left.id == Timetz {
		ak, bk := a.integer+int64(a.west)*1000000, b.integer+int64(b.west)*1000000
		if ak < bk {
			return -1, nil
		}
		if ak > bk {
			return 1, nil
		}
		if a.west < b.west {
			return -1, nil
		}
		if a.west > b.west {
			return 1, nil
		}
		return 0, nil
	}
	switch p.left.id {
	case Macaddr8:
		left, err := a.MACADDR8()
		if err != nil {
			return 0, err
		}
		right, err := b.MACADDR8()
		if err != nil {
			return 0, err
		}
		if left < right {
			return -1, nil
		}
		if left > right {
			return 1, nil
		}
	case Macaddr:
		left, err := a.MACADDR()
		if err != nil {
			return 0, err
		}
		right, err := b.MACADDR()
		if err != nil {
			return 0, err
		}
		if left < right {
			return -1, nil
		}
		if left > right {
			return 1, nil
		}
	case Tid:
		ab, ao, err := a.TID()
		if err != nil {
			return 0, err
		}
		bb, bo, err := b.TID()
		if err != nil {
			return 0, err
		}
		if ab < bb || ab == bb && ao < bo {
			return -1, nil
		}
		if ab > bb || ab == bb && ao > bo {
			return 1, nil
		}
	case Xid, Cid:
		right := b.oid
		if p.right.id == Int4 {
			right = uint32(b.integer)
		}
		if a.oid < right {
			return -1, nil
		}
		if a.oid > right {
			return 1, nil
		}
	case Xid8, PgLSN:
		if uint64(a.integer) < uint64(b.integer) {
			return -1, nil
		}
		if uint64(a.integer) > uint64(b.integer) {
			return 1, nil
		}
	case Inet, Cidr:
		return compareNetwork(a.network, b.network), nil
	case Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		if a.integer != 1 || b.integer != 1 {
			return 0, fmt.Errorf("invalid regclass comparison value")
		}
		if a.oid < b.oid {
			return -1, nil
		}
		if a.oid > b.oid {
			return 1, nil
		}
	case Interval:
		return compareIntervals(a, b), nil
	case Oid:
		if a.oid < b.oid {
			return -1, nil
		}
		if a.oid > b.oid {
			return 1, nil
		}
	case UUID:
		for i, x := range a.uuid {
			if x < b.uuid[i] {
				return -1, nil
			}
			if x > b.uuid[i] {
				return 1, nil
			}
		}
	case Int2, Int4, Int8, Money, Timestamp, Timestamptz, Time, Date:
		if a.integer < b.integer {
			return -1, nil
		}
		if a.integer > b.integer {
			return 1, nil
		}
	case Name:
		return p.inputCollation.CompareText(ctx, a.text, b.text)
	case Bytea, Char, Bit, VarBit:
		return strings.Compare(a.text, b.text), nil
	case Bool:
		if !a.boolean && b.boolean {
			return -1, nil
		}
		if a.boolean && !b.boolean {
			return 1, nil
		}
	default:
		return 0, failure("0A000", "scalar comparison provider is unsupported in embedded mode")
	}
	return 0, nil
}

// ComparisonPredicate applies strict whole-value SQL NULL policy over a bound
// equality or ordering provider.
type ComparisonOperator uint8

const (
	CompareEqual ComparisonOperator = iota
	CompareNotEqual
	CompareLess
	CompareLessEqual
	CompareGreater
	CompareGreaterEqual
)

type ComparisonPredicate struct {
	operands Comparison
	result   *Descriptor
	operator ComparisonOperator
	// Circle's scalar area predicates have no default comparator or hash law.
	circle CirclePlan
	point  PointPlan
}

func (p ComparisonPredicate) SessionDependent() bool { return p.operands.SessionDependent() }

// EqualityFor proves the owned strict same-type predicate used by hash probes.
func (p ComparisonPredicate) EqualityFor(u Usage) bool {
	t := u.Type
	return p.circle.input == nil && t.Concrete() && t.BaseType().id != Lseg && t.BaseType().id != Path && u.validCollation() && !p.operands.collationIndeterminate && p.operands.inputCollation == u.Collation && p.operator == CompareEqual && p.operands.left == t && p.operands.right == t && p.result.Concrete() && p.result.id == Bool
}

func (p ComparisonPredicate) InputCollation() (CollationRef, bool) {
	return p.operands.InputCollation()
}

// DemandCollation initializes an equality key strategy independently of tuple
// population. Scalar strict NULL policy remains in Evaluate.
func (p ComparisonPredicate) DemandCollation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.operands.collationIndeterminate {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
	}
	return nil
}

func BindComparisonPredicate(operator ComparisonOperator, left, right, result *Descriptor, collation CollationRef, indeterminate bool) (ComparisonPredicate, error) {
	if operator > CompareGreaterEqual || !left.Concrete() || !right.Concrete() || result == nil || result.id != Bool || !result.Concrete() {
		return ComparisonPredicate{}, fmt.Errorf("invalid comparison predicate")
	}
	if left.BaseType().ID() == ACLItem || right.BaseType().ID() == ACLItem || left.BaseType().ID() == Line || right.BaseType().ID() == Line {
		if left.BaseType() != right.BaseType() || collation != (CollationRef{}) || indeterminate || operator != CompareEqual {
			return ComparisonPredicate{}, failure("42883", "incompatible comparison operands")
		}
		return ComparisonPredicate{operands: Comparison{left: left, right: right}, result: result, operator: operator}, nil
	}
	if left.BaseType().id == Path || right.BaseType().id == Path {
		if left.BaseType() != right.BaseType() || operator == CompareNotEqual || collation != (CollationRef{}) || indeterminate {
			return ComparisonPredicate{}, failure("42883", "incompatible comparison operands")
		}
		return ComparisonPredicate{operands: Comparison{left: left, right: right}, result: result, operator: operator}, nil
	}
	if err := CheckComparisonOperator(operator, left, right); err != nil {
		return ComparisonPredicate{}, err
	}
	if left.id == Point && right.id == Point {
		if collation != (CollationRef{}) || indeterminate {
			return ComparisonPredicate{}, fmt.Errorf("invalid point comparison collation")
		}
		provider, err := BindPoint(PointNotEqual, result.Usage(), left.Usage(), right.Usage())
		if err != nil {
			return ComparisonPredicate{}, err
		}
		return ComparisonPredicate{operands: Comparison{left: left, right: right}, result: result, operator: operator, point: provider}, nil
	}
	if left.id == Circle && right.id == Circle {
		if collation != (CollationRef{}) || indeterminate {
			return ComparisonPredicate{}, fmt.Errorf("invalid circle comparison collation")
		}
		operations := [...]CircleOperator{CircleEqual, CircleNotEqual, CircleLess, CircleLessEqual, CircleGreater, CircleGreaterEqual}
		provider, err := BindCircle(operations[operator], result.Usage(), left.Usage(), right.Usage())
		if err != nil {
			return ComparisonPredicate{}, err
		}
		return ComparisonPredicate{operands: Comparison{left: left, right: right}, result: result, operator: operator, circle: provider}, nil
	}
	operands, err := BindComparison(left, right, collation, indeterminate)
	if err != nil {
		return ComparisonPredicate{}, err
	}
	return ComparisonPredicate{operands: operands, result: result, operator: operator}, nil
}

func (p ComparisonPredicate) Evaluate(ctx context.Context, a, b Value, input InputEnvironment) (Value, error) {
	if p.operator > CompareGreaterEqual || a.typ != p.operands.left || b.typ != p.operands.right || !p.operands.left.Concrete() || !p.operands.right.Concrete() || !p.result.Concrete() || p.result.id != Bool {
		return Value{}, fmt.Errorf("invalid comparison predicate values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.result), nil
	}
	if a.typ.BaseType().id == Path && b.typ.BaseType().id == Path {
		truth, err := pathPredicate(p.operator, a, b)
		if err != nil {
			return Value{}, err
		}
		return Boolean(p.result, truth), nil
	}
	if a.typ.BaseType().id == Line && b.typ.BaseType().id == Line {
		truth, err := lineEqual(a, b)
		if err != nil {
			return Value{}, err
		}
		return Boolean(p.result, truth), nil
	}
	if a.typ.BaseType().id == Box && b.typ.BaseType().id == Box {
		truth, err := boxPredicate(p.operator, a, b)
		if err != nil {
			return Value{}, err
		}
		return Boolean(p.result, truth), nil
	}
	if a.typ.BaseType().id == Lseg && b.typ.BaseType().id == Lseg {
		truth, err := lsegPredicate(p.operator, a, b)
		if err != nil {
			return Value{}, err
		}
		return Boolean(p.result, truth), nil
	}
	if p.circle.input != nil {
		return p.circle.Evaluate(ctx, [2]Value{a, b})
	}
	if p.point.input != nil {
		return p.point.Evaluate(ctx, [2]Value{a, b})
	}
	if p.operator == CompareEqual || p.operator == CompareNotEqual {
		equal, err := p.operands.EqualValues(ctx, a, b, input)
		if err != nil {
			return Value{}, err
		}
		if p.operator == CompareNotEqual {
			equal = !equal
		}
		return Boolean(p.result, equal), nil
	}
	sign, err := p.operands.Compare(ctx, a, b, input)
	if err != nil {
		return Value{}, err
	}
	var truth bool
	switch p.operator {
	case CompareLess:
		truth = sign < 0
	case CompareLessEqual:
		truth = sign <= 0
	case CompareGreater:
		truth = sign > 0
	case CompareGreaterEqual:
		truth = sign >= 0
	}
	return Boolean(p.result, truth), nil
}
