package sqltype

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

type castUnitFact struct {
	typ                                      *Descriptor
	name, category, base, element, companion string
}
type castUnitEdge struct {
	source, target  string
	context, method byte
}

// REL_18_6 pg_type.dat and extension SQL, not Descriptor category/admission.
func castUnitUniverse(t *testing.T) []castUnitFact {
	t.Helper()
	types := testTypes()
	var facts []castUnitFact
	add := func(d *Descriptor, name, category, base, element, companion string) {
		if d == nil || !d.Concrete() {
			t.Fatalf("missing real descriptor %s", name)
		}
		facts = append(facts, castUnitFact{d, name, category, base, element, companion})
	}
	for category, list := range map[string]string{
		"B": "bool:16", "Z": "char:18 pg_node_tree:194", "S": "name:19 text:25 varchar:1043 bpchar:1042",
		"N": "int2:21 int4:23 int8:20 float4:700 float8:701 numeric:1700 money:790 oid:26 regclass:2205 regtype:2206 regproc:24 regprocedure:2202 regrole:4096 regoper:2203 regoperator:2204 regcollation:4191 regnamespace:4089 regconfig:3734 regdictionary:3769",
		"U": "bytea:17 uuid:2950 refcursor:1790 tid:27 xid:28 cid:29 xid8:5069 pg_lsn:3220 pg_snapshot:5038 inet:869 cidr:650 macaddr:829 macaddr8:774 aclitem:1033 json:114 jsonb:3802 jsonpath:4072 tsvector:3614 tsquery:3615",
		"D": "date:1082 time:1083 timetz:1266 timestamp:1114 timestamptz:1184", "T": "interval:1186", "V": "bit:1560 varbit:1562", "G": "point:600 lseg:601 path:602 box:603 polygon:604 line:628 circle:718", "P": "cstring:2275 void:2278",
	} {
		for _, item := range strings.Fields(list) {
			name, id, _ := strings.Cut(item, ":")
			n, e := strconv.Atoi(id)
			if e != nil {
				t.Fatal(e)
			}
			cat := category
			if name == "inet" || name == "cidr" {
				cat = "I"
			}
			add(types[ID(n)], name, cat, "", "", "")
		}
	}
	for _, item := range strings.Fields("int4range:3904:int4multirange int8range:3926:int8multirange numrange:3906:nummultirange daterange:3912: tsrange:3908: tstzrange:3910: int4multirange:4451: int8multirange:4536: nummultirange:4532:") {
		p := strings.Split(item, ":")
		id, _ := strconv.Atoi(p[1])
		add(types[ID(id)], p[0], "R", "", "", p[2])
	}
	add(types[1002], "char[]", "A", "", "char", "")
	for _, item := range strings.Fields("bool:1000 bytea:1001 name:1003 int2:1005 int4:1007 int8:1016 text:1009 oid:1028 json:199 jsonb:3807 jsonpath:4073 bpchar:1014 varchar:1015 float4:1021 float8:1022 date:1182 time:1183 timetz:1270 timestamp:1115 timestamptz:1185 interval:1187 numeric:1231 uuid:2951 regclass:2210 regtype:2211 regproc:1008 regprocedure:2207 regrole:4097 regcollation:4192 regnamespace:4090 regconfig:3735 regdictionary:3770 aclitem:1034 xid:1011") {
		name, id, _ := strings.Cut(item, ":")
		n, _ := strconv.Atoi(id)
		add(types[ID(n)], name+"[]", "A", "", name, "")
	}
	find := func(name string) castUnitFact {
		for _, f := range facts {
			if f.name == name {
				return f
			}
		}
		t.Fatalf("missing unit fact %s", name)
		return castUnitFact{}
	}
	for _, item := range [][3]string{{"cardinal_number", "int4", "N"}, {"character_data", "varchar", "S"}, {"sql_identifier", "name", "S"}, {"time_stamp", "timestamptz", "D"}, {"yes_or_no", "varchar", "S"}} {
		var d *Descriptor
		for _, candidate := range types {
			if candidate.namespace == "information_schema" && candidate.name == item[0] {
				d = candidate
			}
		}
		add(d, "information_schema."+item[0], item[2], item[1], "", "")
	}
	next := ID(48000)
	own := func(d *Descriptor, e error) *Descriptor {
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	for _, name := range []string{"cube", "query_int", "citext", "ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
		var d *Descriptor
		var e error
		switch name {
		case "cube":
			d, e = Cube(next, name, name)
		case "query_int":
			d, e = QueryInt(next, name, name)
		case "citext":
			d, e = Citext(next, name, name)
		case "ean13":
			d, e = Ean13(next, name, name)
		case "isbn":
			d, e = Isbn(next, name, name)
		case "isbn13":
			d, e = Isbn13(next, name, name)
		case "ismn":
			d, e = Ismn(next, name, name)
		case "ismn13":
			d, e = Ismn13(next, name, name)
		case "issn":
			d, e = Issn(next, name, name)
		case "issn13":
			d, e = Issn13(next, name, name)
		}
		next++
		cat := "U"
		if name == "citext" {
			cat = "S"
		}
		add(own(d, e), name, cat, "", "", "")
	}
	for _, item := range [][3]string{{"cast_int_domain", "int4", "N"}, {"cast_positive", "int4", "N"}, {"cast_nested", "cast_positive", "N"}, {"cast_required", "int4", "N"}, {"cast_short", "varchar", "S"}, {"cast_array_domain", "int4[]", "A"}, {"earth", "cube", "U"}} {
		base := find(item[1]).typ.Usage()
		if item[0] == "cast_short" {
			base.Typmod = 7
		}
		d, e := Domain(next, item[0], item[0], base, item[0] == "cast_positive" || item[0] == "cast_array_domain" || item[0] == "earth", item[0] == "cast_required")
		next++
		add(own(d, e), item[0], item[2], item[1], "", "")
	}
	for _, name := range []string{"cast_enum_a", "cast_enum_b"} {
		d, e := Enum(next, name, name, []string{"red", "blue"})
		next++
		add(own(d, e), name, "E", "", "", "")
	}
	members := []CompositeMember{{Attribute: 1, Name: "n", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "s", Usage: types[Text].Usage()}}
	for _, name := range []string{"cast_pair", "cast_pair_peer", "cast_row"} {
		d, e := Composite(next, name, name, next+100, members)
		next++
		add(own(d, e), name, "C", "", "", "")
	}
	record, e := RecordShape(members)
	add(own(record, e), "record-shape", "C", "", "", "")
	add(DeferredRecord(), "record-null", "P", "", "", "")
	class, ok := LookupIndexClass("int4_ops")
	if !ok {
		t.Fatal("missing existing range ordering")
	}
	class = selectedTestIndexClass(class)
	ordering, e := BindIndexOrdering(types[Int4].Usage(), class)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"cast_range", "cast_other_range"} {
		multi := strings.TrimSuffix(name, "range") + "multirange"
		r, m, e := NewRangeFamily(next, name, name, next+1, multi, multi, RangeDefinition{Subtype: types[Int4].Usage(), Class: class, Ordering: ordering}, types[Bool])
		if e != nil {
			t.Fatal(e)
		}
		next += 2
		add(r, name, "R", "", "", multi)
		add(m, multi, "R", "", "", "")
	}
	for _, name := range strings.Fields("cast_int_domain cast_positive cast_nested cast_required cast_short cast_array_domain cast_enum_a cast_enum_b cast_pair cast_pair_peer cast_row cast_range cast_multirange cast_other_range cast_other_multirange record-shape") {
		leaf := find(name)
		arrayName := name + "[]"
		if name == "record-shape" {
			arrayName = "record[]"
		}
		id := next
		if name == "record-shape" {
			id = 2287
		}
		d, e := Array(id, "_"+name, leaf.typ)
		next++
		add(own(d, e), arrayName, "A", "", name, "")
	}
	return facts
}

// Closed direct facts from REL_18_6 pg_cast.dat and citext--1.4.sql.
func castUnitEdges() []castUnitEdge {
	var edges []castUnitEdge
	add := func(s, t string, c, m byte) { edges = append(edges, castUnitEdge{s, t, c, m}) }
	numeric := strings.Fields("int2 int4 int8 numeric float4 float8")
	for i, s := range numeric {
		for j, t := range numeric {
			if i != j {
				c := byte('i')
				if j < i {
					c = 'a'
				}
				add(s, t, c, 'f')
			}
		}
	}
	for _, row := range strings.Fields("money:numeric:a numeric:money:a int4:money:a int8:money:a int4:bool:e bool:int4:e xid8:xid:e text:regclass:i varchar:regclass:i bpchar:text:i bpchar:varchar:i char:text:i char:bpchar:a char:varchar:a name:text:i name:bpchar:a name:varchar:a text:char:a varchar:char:a bpchar:char:a text:name:i varchar:name:i bpchar:name:i char:int4:e int4:char:e date:timestamp:i date:timestamptz:i time:interval:i time:timetz:i timestamp:date:a timestamp:time:a timestamp:timestamptz:i timestamptz:date:a timestamptz:time:a timestamptz:timestamp:a timestamptz:timetz:a interval:time:a timetz:time:a point:box:a lseg:point:e path:polygon:a box:point:e box:lseg:e box:polygon:a box:circle:e polygon:point:e polygon:path:a polygon:box:e polygon:circle:e circle:point:e circle:box:e circle:polygon:e macaddr:macaddr8:i macaddr8:macaddr:i inet:cidr:a int4:bit:e int8:bit:e bit:int4:e bit:int8:e") {
		p := strings.Split(row, ":")
		add(p[0], p[1], p[2][0], 'f')
	}
	for _, row := range strings.Fields("regproc:regprocedure regprocedure:regproc regoper:regoperator regoperator:regoper text:bpchar text:varchar varchar:text varchar:bpchar pg_node_tree:text cidr:inet bit:varbit varbit:bit") {
		s, t, _ := strings.Cut(row, ":")
		add(s, t, 'i', 'b')
	}
	aliases := strings.Fields("oid regclass regtype regproc regprocedure regrole regoper regoperator regcollation regnamespace regconfig regdictionary")
	for _, a := range aliases {
		add("int2", a, 'i', 'f')
		add("int4", a, 'i', 'b')
		add("int8", a, 'i', 'f')
		add(a, "int4", 'a', 'b')
		add(a, "int8", 'a', 'f')
		if a != "oid" {
			add("oid", a, 'i', 'b')
			add(a, "oid", 'i', 'b')
		}
	}
	for _, n := range strings.Fields("int2 int4 int8") {
		add(n, "bytea", 'e', 'f')
		add("bytea", n, 'e', 'f')
	}
	for _, s := range strings.Fields("cidr inet bool") {
		for _, t := range strings.Fields("text varchar bpchar") {
			add(s, t, 'a', 'f')
		}
	}
	for _, t := range strings.Fields("bool int2 int4 int8 numeric float4 float8") {
		add("jsonb", t, 'e', 'f')
	}
	add("json", "jsonb", 'a', 'i')
	add("jsonb", "json", 'a', 'i')
	for _, s := range strings.Fields("int4 int8 num cast_ cast_other_") {
		r, m := s+"range", s+"multirange"
		add(r, m, 'e', 'f')
	}
	for _, row := range strings.Fields("citext:text:i citext:varchar:i citext:bpchar:a text:citext:a varchar:citext:a bpchar:citext:a bool:citext:a inet:citext:a") {
		p := strings.Split(row, ":")
		method := byte('b')
		if p[0] == "bpchar" || p[0] == "bool" || p[0] == "inet" {
			method = 'f'
		}
		add(p[0], p[1], p[2][0], method)
	}
	return edges
}
func castUnitRank(c CoercionContext) int {
	switch c {
	case Implicit:
		return 0
	case Assignment:
		return 1
	case Explicit:
		return 2
	}
	panic("unknown context")
}
func castUnitExpected(source, target castUnitFact, context CoercionContext, all map[string]castUnitFact, edges []castUnitEdge) bool {
	if source.typ == target.typ {
		return true
	}
	if source.base != "" {
		source = all[source.base]
		return castUnitExpected(source, target, context, all, edges)
	}
	if target.base != "" {
		target = all[target.base]
		return castUnitExpected(source, target, context, all, edges)
	}
	for _, e := range edges {
		if e.source == source.name && e.target == target.name {
			rank := 0
			if e.context == 'a' {
				rank = 1
			}
			if e.context == 'e' {
				rank = 2
			}
			return castUnitRank(context) >= rank
		}
	}
	// Type-only RECORD values have no RowExpr permission, and concrete target
	// shapes are not syntax's generic RECORD passthrough alias.
	if source.element != "" && target.element != "" {
		return castUnitExpected(all[source.element], all[target.element], context, all, edges)
	}
	// parse_coerce.c's CoerceViaIO fallback uses category S, not pseudo kind:
	// explicit string input can reach real CSTRING/VOID codecs, while their
	// result carriers do not acquire implicit string conversion or storage.
	return target.category == "S" && context != Implicit || source.category == "S" && context == Explicit
}
func castUnitLabel(source, target castUnitFact, c CoercionContext) string {
	return fmt.Sprintf("%s/%s/%d", source.name, target.name, c)
}
