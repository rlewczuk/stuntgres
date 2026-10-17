package reference_test

import (
	"bytes"
	"testing"
)

func copyBinaryArray(oid int32, dimensions [][2]int32, fields ...[]byte) []byte {
	flags := int32(0)
	for _, field := range fields {
		if field == nil {
			flags = 1
		}
	}
	b := append(copyBinary32(int32(len(dimensions))), copyBinary32(flags)...)
	b = append(b, copyBinary32(oid)...)
	for _, dimension := range dimensions {
		b = append(b, copyBinary32(dimension[0])...)
		b = append(b, copyBinary32(dimension[1])...)
	}
	for _, field := range fields {
		if field == nil {
			b = append(b, copyBinary32(-1)...)
			continue
		}
		b = append(b, copyBinary32(int32(len(field)))...)
		b = append(b, field...)
	}
	return b
}
func copyBinaryComposite(oids []int32, fields ...[]byte) []byte {
	b := copyBinary32(int32(len(fields)))
	for i, field := range fields {
		b = append(b, copyBinary32(oids[i])...)
		if field == nil {
			b = append(b, copyBinary32(-1)...)
			continue
		}
		b = append(b, copyBinary32(int32(len(field)))...)
		b = append(b, field...)
	}
	return b
}
func copyBinaryRange(flags byte, bounds ...[]byte) []byte {
	b := []byte{flags}
	for _, bound := range bounds {
		b = append(b, copyBinary32(int32(len(bound)))...)
		b = append(b, bound...)
	}
	return b
}
func copyBinaryMultirange(ranges ...[]byte) []byte {
	b := copyBinary32(int32(len(ranges)))
	for _, r := range ranges {
		b = append(b, copyBinary32(int32(len(r)))...)
		b = append(b, r...)
	}
	return b
}
func copyBinaryTypeOID(t *testing.T, c copyTextConnection, name string) int32 {
	t.Helper()
	var oid int64
	query := "SELECT oid FROM pg_type WHERE typname=$1"
	if err := c.conn.QueryRowContext(c.ctx, query, name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 0x7fffffff {
		t.Fatalf("invalid independently queried type OID %d", oid)
	}
	return int32(oid)
}

func TestReferenceCopyBinaryCodecsNested(t *testing.T) {
	h := copyBinaryHex
	dimensions := [][2]int32{{3, 1}}
	array := copyBinaryArray(23, dimensions, copyBinary32(1), nil, copyBinary32(3))
	compositeSetup := []string{"CREATE TYPE copy_pair AS(i int4,t text)"}
	enumSetup := []string{"CREATE TYPE copy_mood AS ENUM('sad','happy')"}
	domainSetup := []string{"CREATE DOMAIN copy_integer AS int4 CHECK(VALUE>0)"}
	intRange := copyBinaryRange(2, copyBinary32(1), copyBinary32(3))
	longRange := copyBinaryRange(2, copyBinary64(1), copyBinary64(3))
	numberRange := copyBinaryRange(2, h("00010000000000000001"), h("00010000000000000003"))
	cases := []copyBinaryProviderCase{
		{name: "enum", typ: "copy_mood", setup: enumSetup, payload: []byte("happy"), valid: true, wire: []byte("happy")},
		{name: "enum_unknown", typ: "copy_mood", setup: enumSetup, payload: []byte("missing")},
		{name: "enum_invalid_utf8", typ: "copy_mood", setup: enumSetup, payload: []byte{255}},
		{name: "domain", typ: "copy_integer", setup: domainSetup, payload: copyBinary32(42), valid: true, wire: copyBinary32(42)},
		{name: "domain_check", typ: "copy_integer", setup: domainSetup, payload: copyBinary32(-1)},
		{name: "domain_short", typ: "copy_integer", setup: domainSetup, payload: []byte{0}},
		{name: "domain_null", typ: "copy_integer", setup: domainSetup, payload: nil, valid: true},
		{name: "array_null", typ: "int4[]", payload: array, valid: true, wire: array},
		{name: "array_empty", typ: "int4[]", payload: copyBinaryArray(23, nil), valid: true, wire: copyBinaryArray(23, nil)},
		{name: "array_multidimensional", typ: "int4[]", payload: copyBinaryArray(23, [][2]int32{{2, -2}, {2, 5}}, copyBinary32(1), copyBinary32(2), copyBinary32(3), copyBinary32(4)), valid: true, identity: true},
		{name: "array_builtin_oid_mismatch", typ: "int4[]", payload: copyBinaryArray(25, [][2]int32{{1, 1}}, copyBinary32(1))},
		{name: "array_user_oid_for_builtin", typ: "int4[]", payload: copyBinaryArray(10001, [][2]int32{{1, 1}}, copyBinary32(1))},
		{name: "array_bad_rank", typ: "int4[]", payload: h("ffffffff0000000000000017")},
		{name: "array_rank_seven", typ: "int4[]", payload: h("000000070000000000000017")},
		{name: "array_bad_flags", typ: "int4[]", payload: h("000000000000000200000017")},
		{name: "array_negative_dimension", typ: "int4[]", payload: h("000000010000000000000017ffffffff00000001")},
		{name: "array_huge_dimension", typ: "int4[]", payload: h("0000000100000000000000177fffffff00000001")},
		{name: "array_short_element", typ: "int4[]", payload: copyBinaryArray(23, [][2]int32{{1, 1}}, []byte{0})},
		{name: "array_trailing_element", typ: "int4[]", payload: copyBinaryArray(23, [][2]int32{{1, 1}}, []byte{0, 0, 0, 1, 0})},
		{name: "array_trailing_container", typ: "int4[]", payload: append(append([]byte{}, array...), 0)},
		{name: "array_negative_element_length", typ: "int4[]", payload: h("0000000100000000000000170000000100000001fffffffe")},
		{name: "array_missing_element", typ: "int4[]", payload: h("0000000100000000000000170000000100000001")},
		{name: "composite", typ: "copy_pair", setup: compositeSetup, payload: copyBinaryComposite([]int32{23, 25}, copyBinary32(42), []byte("hi")), valid: true, identity: true},
		{name: "composite_null", typ: "copy_pair", setup: compositeSetup, payload: copyBinaryComposite([]int32{23, 25}, nil, []byte{}), valid: true, identity: true},
		{name: "composite_builtin_oid_mismatch", typ: "copy_pair", setup: compositeSetup, payload: copyBinaryComposite([]int32{25, 25}, copyBinary32(42), []byte("hi"))},
		{name: "composite_user_oid_for_builtin", typ: "copy_pair", setup: compositeSetup, payload: copyBinaryComposite([]int32{10001, 25}, copyBinary32(42), []byte("hi"))},
		{name: "composite_wrong_count", typ: "copy_pair", setup: compositeSetup, payload: copyBinary32(1)},
		{name: "composite_negative_count", typ: "copy_pair", setup: compositeSetup, payload: copyBinary32(-1)},
		{name: "composite_short_member", typ: "copy_pair", setup: compositeSetup, payload: copyBinaryComposite([]int32{23, 25}, []byte{0}, []byte("hi"))},
		{name: "composite_member_trailing", typ: "copy_pair", setup: compositeSetup, payload: copyBinaryComposite([]int32{23, 25}, []byte{0, 0, 0, 42, 0}, []byte("hi"))},
		{name: "composite_text_nul", typ: "copy_pair", setup: compositeSetup, payload: copyBinaryComposite([]int32{23, 25}, copyBinary32(42), []byte{0})},
		{name: "composite_trailing", typ: "copy_pair", setup: compositeSetup, payload: append(copyBinaryComposite([]int32{23, 25}, copyBinary32(42), []byte("hi")), 0)},
		{name: "composite_dropped", typ: "copy_pair", setup: []string{"CREATE TYPE copy_pair AS(i int4,gone bool,t text)", "ALTER TYPE copy_pair DROP ATTRIBUTE gone"}, payload: copyBinaryComposite([]int32{23, 25}, copyBinary32(42), []byte("hi"))},
		{name: "composite_member_typmod", typ: "copy_pair", setup: []string{"CREATE TYPE copy_pair AS(i int4,t varchar(2))"}, payload: copyBinaryComposite([]int32{23, 1043}, copyBinary32(42), []byte("long"))},
		{name: "int4range", typ: "int4range", payload: intRange, valid: true, wire: intRange},
		{name: "int8range", typ: "int8range", payload: longRange, valid: true, wire: longRange},
		{name: "numrange", typ: "numrange", payload: numberRange, valid: true, wire: numberRange},
		{name: "range_empty", typ: "int4range", payload: []byte{1}, valid: true, wire: []byte{1}},
		{name: "range_unbounded", typ: "int4range", payload: []byte{24}, valid: true, wire: []byte{24}},
		{name: "range_discrete_canonicalization", typ: "int4range", payload: copyBinaryRange(4, copyBinary32(1), copyBinary32(3))},
		{name: "range_unknown_flags", typ: "int4range", payload: []byte{0x99}},
		{name: "range_empty_payload", typ: "int4range", payload: []byte{}},
		{name: "range_negative_bound_length", typ: "int4range", payload: append([]byte{2}, copyBinary32(-1)...)},
		{name: "range_short_bound", typ: "int4range", payload: copyBinaryRange(2, []byte{0}, copyBinary32(3))},
		{name: "range_bound_trailing", typ: "int4range", payload: copyBinaryRange(2, []byte{0, 0, 0, 1, 0}, copyBinary32(3))},
		{name: "range_container_trailing", typ: "int4range", payload: append(append([]byte{}, intRange...), 0)},
		{name: "int4multirange", typ: "int4multirange", payload: copyBinaryMultirange(intRange, copyBinaryRange(2, copyBinary32(5), copyBinary32(7))), valid: true, identity: true},
		{name: "int8multirange", typ: "int8multirange", payload: copyBinaryMultirange(longRange), valid: true, identity: true},
		{name: "nummultirange", typ: "nummultirange", payload: copyBinaryMultirange(numberRange), valid: true, identity: true},
		{name: "multirange_reorder_merge", typ: "int4multirange", payload: copyBinaryMultirange(copyBinaryRange(2, copyBinary32(3), copyBinary32(5)), intRange)},
		{name: "multirange_empty", typ: "int4multirange", payload: copyBinary32(0), valid: true, wire: copyBinary32(0)},
		{name: "multirange_negative_count", typ: "int4multirange", payload: copyBinary32(-1)},
		{name: "multirange_huge_count", typ: "int4multirange", payload: copyBinary32(0x7fffffff)},
		{name: "multirange_negative_length", typ: "int4multirange", payload: append(copyBinary32(1), copyBinary32(-1)...)},
		{name: "multirange_truncated_member", typ: "int4multirange", payload: append(append(copyBinary32(1), copyBinary32(10)...), 2)},
		{name: "multirange_trailing", typ: "int4multirange", payload: append(copyBinaryMultirange(intRange), 0)},
	}
	// Every currently admitted built-in array element family, excluding absent
	// network/composite/char/xid/tree/void/bit/range arrays. Element OIDs below
	// are PostgreSQL's documented built-in identities, not user-defined OIDs.
	for _, element := range []struct {
		typ     string
		oid     int32
		payload []byte
	}{
		{"bool", 16, []byte{1}}, {"bytea", 17, h("00ff")}, {"name", 19, []byte("a")}, {"int8", 20, copyBinary64(42)}, {"int2", 21, copyBinary16(42)}, {"int4", 23, copyBinary32(42)}, {"text", 25, []byte("hi")}, {"oid", 26, copyBinary32(42)},
		{"json", 114, []byte(`{"a":1}`)}, {"float4", 700, h("3fc00000")}, {"float8", 701, h("3ff8000000000000")}, {"money", 790, copyBinary64(1234)},
		{"bpchar", 1042, []byte("a")}, {"varchar", 1043, []byte("hi")}, {"date", 1082, copyBinary32(0)}, {"time", 1083, copyBinary64(0)}, {"timestamp", 1114, copyBinary64(0)}, {"timestamptz", 1184, copyBinary64(0)}, {"interval", 1186, make([]byte, 16)}, {"timetz", 1266, make([]byte, 12)},
		{"numeric", 1700, h("0002000000000002000c0d48")}, {"regclass", 2205, copyBinary32(42)}, {"regtype", 2206, copyBinary32(23)}, {"uuid", 2950, h("00112233445566778899aabbccddeeff")}, {"jsonb", 3802, []byte{1, '{', '}'}}, {"jsonpath", 4072, []byte{1, '$'}},
	} {
		cases = append(cases, copyBinaryProviderCase{name: "array_family_" + element.typ, typ: element.typ + "[]", payload: copyBinaryArray(element.oid, [][2]int32{{2, 1}}, element.payload, nil), valid: true})
	}
	cases = append(cases,
		copyBinaryProviderCase{name: "array_enum", typ: "copy_mood[]", setup: enumSetup, valid: true, identity: true, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_mood"), [][2]int32{{2, 1}}, []byte("happy"), nil)
		}},
		copyBinaryProviderCase{name: "array_domain", typ: "copy_integer[]", setup: domainSetup, valid: true, identity: true, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_integer"), [][2]int32{{1, 1}}, copyBinary32(42))
		}},
		copyBinaryProviderCase{name: "composite_dynamic_members", typ: "copy_pair", setup: []string{"CREATE TYPE copy_mood AS ENUM('happy')", "CREATE DOMAIN copy_integer AS int4", "CREATE TYPE copy_pair AS(e copy_mood,d copy_integer)"}, valid: true, identity: true, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryComposite([]int32{copyBinaryTypeOID(t, c, "copy_mood"), copyBinaryTypeOID(t, c, "copy_integer")}, []byte("happy"), copyBinary32(42))
		}},
		copyBinaryProviderCase{name: "array_enum_user_oid_mismatch", typ: "copy_mood[]", setup: []string{"CREATE TYPE copy_mood AS ENUM('happy')", "CREATE TYPE copy_other AS ENUM('happy')"}, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_other"), [][2]int32{{1, 1}}, []byte("happy"))
		}, expectedBuild: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_mood"), [][2]int32{{1, 1}}, []byte("happy"))
		}},
		copyBinaryProviderCase{name: "composite_user_oid_mismatch", typ: "copy_pair", setup: []string{"CREATE TYPE copy_mood AS ENUM('happy')", "CREATE TYPE copy_other AS ENUM('happy')", "CREATE TYPE copy_pair AS(e copy_mood)"}, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryComposite([]int32{copyBinaryTypeOID(t, c, "copy_other")}, []byte("happy"))
		}, expectedBuild: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryComposite([]int32{copyBinaryTypeOID(t, c, "copy_mood")}, []byte("happy"))
		}},
		copyBinaryProviderCase{name: "domain_array", typ: "copy_array", setup: []string{"CREATE DOMAIN copy_array AS int4[]"}, payload: array, valid: true, wire: array},
		copyBinaryProviderCase{name: "domain_composite", typ: "copy_record", setup: []string{"CREATE TYPE copy_pair AS(i int4,t text)", "CREATE DOMAIN copy_record AS copy_pair"}, payload: copyBinaryComposite([]int32{23, 25}, copyBinary32(42), []byte("hi")), valid: true, identity: true},
		copyBinaryProviderCase{name: "composite_array_member", typ: "copy_pair", setup: []string{"CREATE TYPE copy_pair AS(a int4[],r int4range)"}, payload: copyBinaryComposite([]int32{1007, 3904}, array, intRange), valid: true, identity: true},
		copyBinaryProviderCase{name: "composite_nested_member", typ: "copy_outer", setup: []string{"CREATE TYPE copy_pair AS(i int4,t text)", "CREATE TYPE copy_outer AS(p copy_pair)"}, valid: true, identity: true, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryComposite([]int32{copyBinaryTypeOID(t, c, "copy_pair")}, copyBinaryComposite([]int32{23, 25}, copyBinary32(42), []byte("hi")))
		}},
		copyBinaryProviderCase{name: "array_nested_text_utf8", typ: "text[]", payload: copyBinaryArray(25, [][2]int32{{1, 1}}, []byte{255})},
		copyBinaryProviderCase{name: "array_nested_text_nul", typ: "text[]", payload: copyBinaryArray(25, [][2]int32{{1, 1}}, []byte{0})},
		copyBinaryProviderCase{name: "domain_typmod", typ: "copy_text", setup: []string{"CREATE DOMAIN copy_text AS varchar(2)"}, payload: []byte("long")},
	)
	copyBinaryProviders(t, cases, copyBinaryNestedExpected)
}

func TestReferenceCopyBinaryOutputOnly(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"void", "SELECT setseed(0) AS v"},
		{"node_tree", "SELECT d.adbin AS v FROM pg_attrdef d JOIN pg_class c ON d.adrelid=c.oid WHERE c.relname='copy_binary_default'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_binary_default(i int4 DEFAULT 42)")
				var output bytes.Buffer
				result, err := c.to("COPY ("+tc.query+") TO STDOUT WITH(FORMAT binary)", &output)
				if !c.pg && tc.name == "node_tree" {
					// Native boundTree is a semantic serialization, not PG heap Datum.
					// deparse_tree.go renders a typed int4 Const with quoted payload.
					const tree = `{type 23 typmod -1 collation 0 derivation 0 node Const value "42"}`
					copyTextSuccess(t, result, err, 1)
					copyBinaryProviderWire(t, output.Bytes(), []byte(tree))
					c.selectRows(t, tc.query, []string{"v"}, []string{"PG_NODE_TREE"}, [][]any{{tree}})
					c.output(t, "COPY ("+tc.query+") TO STDOUT", tree+"\n", 1)
					return
				}
				copyBinaryOutcome(t, "OutputOnly", tc.name, result, err, output.Bytes())
			})
		})
	}
}
