package sqltype

import (
	"strings"
	"testing"
)

// This is the intentional completeness guard, not cast admission authority.
// OIDs/name literals come from REL_18_6 pg_type.dat and the independently scoped
// executable-role inventory. Newly executable companions must not silently evade
// the ordered-pair reference corpus. unknown and lookup-only identities are out.
func TestCastImplementedInventory(t *testing.T) {
	scalarIDs := map[ID]string{
		16: "bool", 17: "bytea", 18: "char", 19: "name", 20: "int8", 21: "int2", 23: "int4", 25: "text", 1790: "refcursor",
		26: "oid", 27: "tid", 28: "xid", 29: "cid", 5069: "xid8", 5038: "pg_snapshot", 24: "regproc", 2202: "regprocedure", 2203: "regoper", 2204: "regoperator", 2205: "regclass", 2206: "regtype", 4096: "regrole", 4191: "regcollation", 4089: "regnamespace", 3734: "regconfig", 3769: "regdictionary",
		1042: "bpchar", 1043: "varchar", 700: "float4", 701: "float8", 1700: "numeric", 790: "money", 1560: "bit", 1562: "varbit", 2950: "uuid",
		1082: "date", 1083: "time", 1266: "timetz", 1114: "timestamp", 1184: "timestamptz", 1186: "interval",
		869: "inet", 650: "cidr", 829: "macaddr", 774: "macaddr8", 1033: "aclitem", 114: "json", 3802: "jsonb", 4072: "jsonpath", 3614: "tsvector", 3615: "tsquery",
		600: "point", 601: "lseg", 602: "path", 603: "box", 604: "polygon", 628: "line", 718: "circle", 194: "pg_node_tree", 2278: "void",
		3904: "int4range", 3926: "int8range", 3906: "numrange", 3912: "daterange", 3908: "tsrange", 3910: "tstzrange", 4451: "int4multirange", 4536: "int8multirange", 4532: "nummultirange",
	}
	arrayIDs := map[ID]ID{
		1002: 18,
		1000: 16, 1001: 17, 1003: 19, 1005: 21, 1007: 23, 1016: 20, 1009: 25, 1028: 26, 199: 114, 3807: 3802, 4073: 4072, 1014: 1042, 1015: 1043, 1021: 700, 1022: 701,
		1182: 1082, 1183: 1083, 1270: 1266, 1115: 1114, 1185: 1184, 1187: 1186, 1231: 1700, 2951: 2950, 2210: 2205, 2211: 2206, 1008: 24, 2207: 2202, 4097: 4096, 4192: 4191, 4090: 4089, 1034: 1033, 1011: 28,
	}
	domains := map[string]ID{"cardinal_number": 23, "character_data": 1043, "sql_identifier": 19, "time_stamp": 1184, "yes_or_no": 1043}
	seen := map[ID]bool{}
	seenDomains := map[string]bool{}
	for _, d := range Builtins() {
		if !d.Concrete() {
			if _, ok := scalarIDs[d.ID()]; ok {
				t.Errorf("inventoried scalar %d lost execution", d.ID())
			}
			if _, ok := arrayIDs[d.ID()]; ok {
				t.Errorf("inventoried array %d lost execution", d.ID())
			}
			continue
		}
		if seen[d.ID()] {
			t.Errorf("duplicate concrete OID %d", d.ID())
		}
		seen[d.ID()] = true
		if d.namespace == "information_schema" {
			base, ok := domains[d.name]
			if !ok {
				t.Errorf("new executable builtin domain %s missing from cast inventory", d.name)
				continue
			}
			seenDomains[d.name] = true
			if !d.IsDomain() || d.BaseType().ID() != base {
				t.Errorf("domain %s lost base identity %d", d.name, base)
			}
			continue
		}
		if element, ok := arrayIDs[d.ID()]; ok {
			if !d.IsArray() || d.Element().ID() != element || !d.Element().Concrete() {
				t.Errorf("array %d lost element role %d", d.ID(), element)
			}
			continue
		}
		name, ok := scalarIDs[d.ID()]
		if !ok {
			t.Errorf("new executable builtin %d/%s missing from cast inventory", d.ID(), d.name)
			continue
		}
		if d.name != name {
			t.Errorf("OID %d name %q want %q", d.ID(), d.name, name)
		}
	}
	for id := range scalarIDs {
		if !seen[id] {
			t.Errorf("missing scalar %d", id)
		}
	}
	for id := range arrayIDs {
		if !seen[id] {
			t.Errorf("missing array %d", id)
		}
	}
	for name := range domains {
		if !seenDomains[name] {
			t.Errorf("missing builtin domain %s", name)
		}
	}
	// These are explicit lookup-only boundaries, not inferred from a companion ID.
	forbidden := "_money _macaddr _macaddr8 _tid _inet _cidr _refcursor _tsvector _tsquery _point _lseg _path _box _polygon _line _circle _pg_snapshot _xid8 _cid _regoper _regoperator _regconfig _regdictionary datemultirange tsmultirange tstzmultirange"
	boundaries := map[string]bool{}
	for _, name := range strings.Fields(forbidden) {
		boundaries[name] = false
	}
	for _, d := range Builtins() {
		if _, ok := boundaries[d.name]; ok {
			boundaries[d.name] = true
			if d.Concrete() {
				t.Errorf("lookup-only boundary %s unexpectedly executable", d.name)
			}
		}
	}
	for name, found := range boundaries {
		if !found {
			t.Errorf("lookup-only boundary identity %s missing", name)
		}
	}
}
