package sqltype

// PGTypeMetadata is immutable family-owned catalog evidence, never input,
// storage, cast or function-call authority. Identity/ownership stay in catalogs.
type PGTypeMetadata struct {
	PostgreSQLAttributeFacts
	Kind, Category                                                  string
	Preferred                                                       bool
	Element, Base                                                   ID
	Subscript, Input, Output, Receive, Send, ModIn, ModOut, Analyze uint32
	Modifier                                                        int32
	Collation                                                       uint32
	DefaultTree, Default                                            string
	HasDefaultTree, HasDefault                                      bool
}

// CompositePGTypeMetadata also describes real registered relation identities
// whose lookup-only sqltype descriptor intentionally has no composite codec.
func CompositePGTypeMetadata() PGTypeMetadata {
	return PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{Length: -1, Alignment: "d", Storage: "x"}, Kind: "c", Category: "C", Input: 2290, Output: 2291, Receive: 2402, Send: 2403, Modifier: -1}
}

// PGTypeArrayMetadata requires independently admitted canonical element facts.
// It does not grant codecs or decide which element identities a catalog owns.
func (d *Descriptor) PGTypeArrayMetadata(element PGTypeMetadata) PGTypeMetadata {
	f := PGTypeMetadata{PostgreSQLAttributeFacts: d.AttributeFacts(), Kind: "b", Category: "A", Element: d.element.ID(), Subscript: 6179, Input: 750, Output: 751, Receive: 2400, Send: 2401, Analyze: 3816, Modifier: -1, Collation: element.Collation}
	f.Alignment = "i"
	if element.Alignment == "d" {
		f.Alignment = "d"
	}
	if d.IsBuiltin() {
		f.ModIn, f.ModOut = element.ModIn, element.ModOut
	}
	if d.id == 2287 {
		f.Kind, f.Category = "p", "P"
	}
	return f
}

// PGTypeDomainMetadata requires complete, independently admitted immediate-base
// facts. Catalog-owned routines must be resolved before applying inheritance.
func (d *Descriptor) PGTypeDomainMetadata(base PGTypeMetadata) PGTypeMetadata {
	base.Kind, base.Preferred, base.Element, base.Base = "d", false, 0, d.base.Type.ID()
	base.Subscript, base.Input, base.Receive, base.ModIn, base.ModOut = 0, 2597, 2598, 0, 0
	base.Modifier, base.Collation = d.base.Typmod, d.base.Collation.ID
	if d.IsBuiltin() && d.id == 13254 {
		// Preserve the pinned18.6 builtin inventory tree alongside its cooked recipe.
		base.DefaultTree = "{SQLVALUEFUNCTION :op 4 :type 1184 :typmod 2 :location -1}"
		base.Default = "CURRENT_TIMESTAMP(2)"
		base.HasDefaultTree, base.HasDefault = true, true
	}
	return base
}

// PGTypeMetadata supplies family facts without catalog-owned routine addresses.
// Catalog projections compose domains through PGTypeDomainMetadata only after
// resolving complete immediate-base facts in their captured catalog.
func (d *Descriptor) PGTypeMetadata() (PGTypeMetadata, bool) {
	if d == nil {
		return PGTypeMetadata{}, false
	}
	if d.IsArray() {
		element, ok := d.element.PGTypeMetadata()
		if !ok {
			return PGTypeMetadata{}, false
		}
		return d.PGTypeArrayMetadata(element), true
	}
	if d.IsDomain() {
		if !d.Concrete() {
			return PGTypeMetadata{}, false
		}
		f, ok := d.base.Type.PGTypeMetadata()
		if !ok {
			return PGTypeMetadata{}, false
		}
		return d.PGTypeDomainMetadata(f), true
	}
	if d.IsEnum() {
		return PGTypeMetadata{PostgreSQLAttributeFacts: d.AttributeFacts(), Kind: "e", Category: "E", Input: 3506, Output: 3507, Receive: 3532, Send: 3533, Modifier: -1}, true
	}
	if d.IsComposite() {
		return CompositePGTypeMetadata(), true
	}
	if d.IsCitext() {
		return PGTypeMetadata{PostgreSQLAttributeFacts: d.AttributeFacts(), Kind: "b", Category: "S", Collation: 100, Modifier: -1}, true
	}
	if d.IsQueryInt() || d.IsCube() {
		return PGTypeMetadata{PostgreSQLAttributeFacts: d.AttributeFacts(), Kind: "b", Category: "U", Modifier: -1}, true
	}
	if !d.IsBuiltin() {
		return PGTypeMetadata{}, false
	}
	var f PGTypeMetadata
	// REL_18_6 pg_type.dat and pg_proc.dat. No lookup-identity fallback.
	switch d.id {
	case Point:
		f = PGTypeMetadata{Kind: "b", Category: "G", Element: Float8, Subscript: 6180, Input: 117, Output: 118, Receive: 2428, Send: 2429}
	case Box:
		f = PGTypeMetadata{Kind: "b", Category: "G", Element: Point, Subscript: 6180, Input: 123, Output: 124, Receive: 2484, Send: 2485}
	case Lseg:
		f = PGTypeMetadata{Kind: "b", Category: "G", Element: Point, Subscript: 6180, Input: 119, Output: 120, Receive: 2480, Send: 2481}
	case Circle:
		f = PGTypeMetadata{Kind: "b", Category: "G", Input: 1450, Output: 1451, Receive: 2490, Send: 2491}
	case Polygon:
		f = PGTypeMetadata{Kind: "b", Category: "G", Input: 347, Output: 348, Receive: 2486, Send: 2487}
	case Line:
		f = PGTypeMetadata{Kind: "b", Category: "G", Element: Float8, Subscript: 6180, Input: 1490, Output: 1491, Receive: 2488, Send: 2489}
	case Path:
		f = PGTypeMetadata{Kind: "b", Category: "G", Input: 121, Output: 122, Receive: 2482, Send: 2483}
	case TSVector:
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 3610, Output: 3611, Receive: 3639, Send: 3638, Analyze: 3688}
	case TSQuery:
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 3612, Output: 3613, Receive: 3641, Send: 3640}
	case 16: // bool
		f = PGTypeMetadata{Kind: "b", Category: "B", Preferred: true, Input: 1242, Output: 1243, Receive: 2436, Send: 2437}
	case 17: // bytea
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 1244, Output: 31, Receive: 2412, Send: 2413}
	case 18: // char
		f = PGTypeMetadata{Kind: "b", Category: "Z", Input: 1245, Output: 33, Receive: 2434, Send: 2435}
	case 19: // name
		f = PGTypeMetadata{Kind: "b", Category: "S", Element: 18, Subscript: 6180, Input: 34, Output: 35, Receive: 2422, Send: 2423}
	case 20: // int8
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 460, Output: 461, Receive: 2408, Send: 2409}
	case 21: // int2
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 38, Output: 39, Receive: 2404, Send: 2405}
	case 23: // int4
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 42, Output: 43, Receive: 2406, Send: 2407}
	case 25: // text
		f = PGTypeMetadata{Kind: "b", Category: "S", Preferred: true, Input: 46, Output: 47, Receive: 2414, Send: 2415}
	case 26: // oid
		f = PGTypeMetadata{Kind: "b", Category: "N", Preferred: true, Input: 1798, Output: 1799, Receive: 2418, Send: 2419}
	case 27: // tid
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 48, Output: 49, Receive: 2438, Send: 2439}
	case Macaddr:
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 436, Output: 437, Receive: 2494, Send: 2495}
	case 28: // xid
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 50, Output: 51, Receive: 2440, Send: 2441}
	case 29: // cid
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 52, Output: 53, Receive: 2442, Send: 2443}
	case 5069: // xid8
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 5070, Output: 5081, Receive: 5082, Send: 5083}
	case 114: // json
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 321, Output: 322, Receive: 323, Send: 324}
	case 194: // pg_node_tree
		f = PGTypeMetadata{Kind: "b", Category: "Z", Input: 195, Output: 196, Receive: 197, Send: 198}
	case 3802: // jsonb
		f = PGTypeMetadata{Kind: "b", Category: "U", Subscript: 6098, Input: 3806, Output: 3804, Receive: 3805, Send: 3803}
	case 4072: // jsonpath
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 4001, Output: 4003, Receive: 4002, Send: 4004}
	case 1042: // bpchar
		f = PGTypeMetadata{Kind: "b", Category: "S", Input: 1044, Output: 1045, Receive: 2430, Send: 2431, ModIn: 2913, ModOut: 2914}
	case 1043: // varchar
		f = PGTypeMetadata{Kind: "b", Category: "S", Input: 1046, Output: 1047, Receive: 2432, Send: 2433, ModIn: 2915, ModOut: 2916}
	case 700: // float4
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 200, Output: 201, Receive: 2424, Send: 2425}
	case 701: // float8
		f = PGTypeMetadata{Kind: "b", Category: "N", Preferred: true, Input: 214, Output: 215, Receive: 2426, Send: 2427}
	case 1700: // numeric
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 1701, Output: 1702, Receive: 2460, Send: 2461, ModIn: 2917, ModOut: 2918}
	case 790: // money
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 886, Output: 887, Receive: 2492, Send: 2493}
	case 650: // cidr
		f = PGTypeMetadata{Kind: "b", Category: "I", Input: 1267, Output: 1427, Receive: 2498, Send: 2499}
	case 869: // inet
		f = PGTypeMetadata{Kind: "b", Category: "I", Preferred: true, Input: 910, Output: 911, Receive: 2496, Send: 2497}
	case 1033: // aclitem
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 1031, Output: 1032}
	case 2950: // uuid
		f = PGTypeMetadata{Kind: "b", Category: "U", Input: 2952, Output: 2953, Receive: 2961, Send: 2962}
	case 1560: // bit
		f = PGTypeMetadata{Kind: "b", Category: "V", Input: 1564, Output: 1565, Receive: 2456, Send: 2457, ModIn: 2919, ModOut: 2920}
	case 1562: // varbit
		f = PGTypeMetadata{Kind: "b", Category: "V", Preferred: true, Input: 1579, Output: 1580, Receive: 2458, Send: 2459, ModIn: 2902, ModOut: 2921}
	case 1082: // date
		f = PGTypeMetadata{Kind: "b", Category: "D", Input: 1084, Output: 1085, Receive: 2468, Send: 2469}
	case 1083: // time
		f = PGTypeMetadata{Kind: "b", Category: "D", Input: 1143, Output: 1144, Receive: 2470, Send: 2471, ModIn: 2909, ModOut: 2910}
	case 1114: // timestamp
		f = PGTypeMetadata{Kind: "b", Category: "D", Input: 1312, Output: 1313, Receive: 2474, Send: 2475, ModIn: 2905, ModOut: 2906}
	case 1184: // timestamptz
		f = PGTypeMetadata{Kind: "b", Category: "D", Preferred: true, Input: 1150, Output: 1151, Receive: 2476, Send: 2477, ModIn: 2907, ModOut: 2908}
	case 1186: // interval
		f = PGTypeMetadata{Kind: "b", Category: "T", Preferred: true, Input: 1160, Output: 1161, Receive: 2478, Send: 2479, ModIn: 2903, ModOut: 2904}
	case 1266: // timetz
		f = PGTypeMetadata{Kind: "b", Category: "D", Input: 1350, Output: 1351, Receive: 2472, Send: 2473, ModIn: 2911, ModOut: 2912}
	case 24: // regproc
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 44, Output: 45, Receive: 2444, Send: 2445}
	case 2202: // regprocedure
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 2212, Output: 2213, Receive: 2446, Send: 2447}
	case 2203: // regoper
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 2214, Output: 2215, Receive: 2448, Send: 2449}
	case 2204: // regoperator
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 2216, Output: 2217, Receive: 2450, Send: 2451}
	case 2205: // regclass
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 2218, Output: 2219, Receive: 2452, Send: 2453}
	case 2206: // regtype
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 2220, Output: 2221, Receive: 2454, Send: 2455}
	case 4089: // regnamespace
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 4084, Output: 4085, Receive: 4087, Send: 4088}
	case 4096: // regrole
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 4098, Output: 4092, Receive: 4094, Send: 4095}
	case 4191: // regcollation
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 4193, Output: 4194, Receive: 4196, Send: 4197}
	case 3734: // regconfig
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 3736, Output: 3737, Receive: 3738, Send: 3739}
	case 3769: // regdictionary
		f = PGTypeMetadata{Kind: "b", Category: "N", Input: 3771, Output: 3772, Receive: 3773, Send: 3774}
	case 3904: // int4range
		f = PGTypeMetadata{Kind: "r", Category: "R", Input: 3834, Output: 3835, Receive: 3836, Send: 3837, Analyze: 3916}
	case 3906: // numrange
		f = PGTypeMetadata{Kind: "r", Category: "R", Input: 3834, Output: 3835, Receive: 3836, Send: 3837, Analyze: 3916}
	case 3908: // tsrange
		f = PGTypeMetadata{Kind: "r", Category: "R", Input: 3834, Output: 3835, Receive: 3836, Send: 3837, Analyze: 3916}
	case 3910: // tstzrange
		f = PGTypeMetadata{Kind: "r", Category: "R", Input: 3834, Output: 3835, Receive: 3836, Send: 3837, Analyze: 3916}
	case 3912: // daterange
		f = PGTypeMetadata{Kind: "r", Category: "R", Input: 3834, Output: 3835, Receive: 3836, Send: 3837, Analyze: 3916}
	case 3926: // int8range
		f = PGTypeMetadata{Kind: "r", Category: "R", Input: 3834, Output: 3835, Receive: 3836, Send: 3837, Analyze: 3916}
	case 4451: // int4multirange
		f = PGTypeMetadata{Kind: "m", Category: "R", Input: 4231, Output: 4232, Receive: 4233, Send: 4234, Analyze: 4242}
	case 4532: // nummultirange
		f = PGTypeMetadata{Kind: "m", Category: "R", Input: 4231, Output: 4232, Receive: 4233, Send: 4234, Analyze: 4242}
	case 4536: // int8multirange
		f = PGTypeMetadata{Kind: "m", Category: "R", Input: 4231, Output: 4232, Receive: 4233, Send: 4234, Analyze: 4242}
	case 705: // unknown
		f = PGTypeMetadata{Kind: "p", Category: "X", Input: 109, Output: 110, Receive: 2416, Send: 2417}
	case 2278: // void
		f = PGTypeMetadata{Kind: "p", Category: "P", Input: 2298, Output: 2299, Receive: 3120, Send: 3121}
	case 2249: // record
		f = PGTypeMetadata{Kind: "p", Category: "P", Input: 2290, Output: 2291, Receive: 2402, Send: 2403}
	case 2277: // anyarray
		f = PGTypeMetadata{Kind: "p", Category: "P", Input: 2296, Output: 2297, Receive: 2502, Send: 2503}
	default:
		return PGTypeMetadata{}, false
	}
	f.PostgreSQLAttributeFacts = d.AttributeFacts()
	f.Modifier = -1
	f.Collation = d.Usage().Collation.ID
	return f, true
}
