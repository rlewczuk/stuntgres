package reference_test

import (
	"bytes"
	"testing"
)

type copyBinaryProviderCase struct {
	name, typ string
	payload   []byte
	// valid records inputs already justified before oracle discovery. The
	// expected map now fixes success/rejection for every formerly unknown input.
	// wire is an optional independently specified canonical payload override.
	valid         bool
	wire          []byte
	setup         []string
	build         func(*testing.T, copyTextConnection) []byte
	expectedBuild func(*testing.T, copyTextConnection) []byte
	identity      bool
}

func copyBinaryProviders(t *testing.T, cases []copyBinaryProviderCase, expected map[string]copyBinaryProviderExpected) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, ok := expected[tc.name]
			if !ok {
				t.Fatalf("unobserved provider case %s; pinned discovery required", tc.name)
			}
			copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, "SET TIME ZONE 'UTC'")
				for _, sql := range tc.setup {
					c.exec(t, sql)
				}
				tableSQL := "CREATE TABLE copy_binary_probe(v " + tc.typ + ")"
				if copyBinaryTableBoundary(t, c, tableSQL, tc.typ) {
					return
				}
				c.exec(t, tableSQL)
				payload := tc.payload
				if tc.build != nil {
					payload = tc.build(t, c)
				}
				result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(payload))))
				diagnostic := want.diagnostic
				if c.pg && want.postgresDiagnostic != nil {
					diagnostic = want.postgresDiagnostic
				}
				if diagnostic != nil {
					if tc.valid {
						t.Fatal("oracle rejection conflicts with independently valid fixture")
					}
					copyTextDiagnostic(t, result, err, *diagnostic)
					copyBinaryCount(t, c, 0)
					return
				}
				copyTextSuccess(t, result, err, 1)
				copyBinaryRawProvider(t, c, want)
				var output, text bytes.Buffer
				exported, exportErr := c.to("COPY copy_binary_probe TO STDOUT WITH(FORMAT binary)", &output)
				copyTextSuccess(t, exported, exportErr, 1)
				wire := copyBinaryCanonical(tc.name, payload)
				if tc.wire != nil {
					wire = tc.wire
				}
				if tc.identity {
					wire = payload
				}
				if tc.expectedBuild != nil {
					wire = tc.expectedBuild(t, c)
				}
				copyBinaryProviderWire(t, output.Bytes(), wire)
				textual, textErr := c.to("COPY copy_binary_probe TO STDOUT", &text)
				copyTextSuccess(t, textual, textErr, 1)
				if text.String() != want.text {
					t.Errorf("text output=%q want=%q", text.String(), want.text)
				}
			})
		})
	}
}

func TestReferenceCopyBinaryCodecsScalar(t *testing.T) {
	h := copyBinaryHex
	cases := []copyBinaryProviderCase{
		{name: "int2_min", typ: "int2", payload: h("8000"), valid: true, wire: h("8000")},
		{name: "int4_42", typ: "int4", payload: copyBinary32(42), valid: true, wire: copyBinary32(42)},
		{name: "int8_min", typ: "int8", payload: h("8000000000000000"), valid: true, wire: h("8000000000000000")},
		{name: "bool_true", typ: "bool", payload: []byte{1}, valid: true, wire: []byte{1}},
		{name: "bool_noncanonical", typ: "bool", payload: []byte{255}},
		{name: "bool_empty", typ: "bool", payload: []byte{}},
		{name: "char_zero", typ: `"char"`, payload: []byte{0}, valid: true, wire: []byte{0}},
		{name: "char_ff", typ: `"char"`, payload: []byte{255}},
		{name: "char_empty", typ: `"char"`, payload: []byte{}},
		{name: "oid", typ: "oid", payload: h("ffffffff"), valid: true, wire: h("ffffffff")},
		{name: "xid", typ: "xid", payload: copyBinary32(42), valid: true, wire: copyBinary32(42)},
		{name: "regclass_raw", typ: "regclass", payload: copyBinary32(42), valid: true, wire: copyBinary32(42)},
		{name: "regtype_int4", typ: "regtype", payload: copyBinary32(23), valid: true, wire: copyBinary32(23)},
		{name: "float4", typ: "float4", payload: h("3fc00000"), valid: true, wire: h("3fc00000")},
		{name: "float8", typ: "float8", payload: h("3ff8000000000000"), valid: true, wire: h("3ff8000000000000")},
		{name: "float4_negative_zero", typ: "float4", payload: h("80000000"), valid: true, wire: h("80000000")},
		{name: "float8_infinity", typ: "float8", payload: h("7ff0000000000000"), valid: true, wire: h("7ff0000000000000")},
		{name: "float8_nan", typ: "float8", payload: h("7ff8000000000000")},
		{name: "money", typ: "money", payload: copyBinary64(1234), valid: true, wire: copyBinary64(1234)},
		{name: "text_utf8", typ: "text", payload: []byte("hé\\llo\n"), valid: true, wire: []byte("hé\\llo\n")},
		{name: "text_empty", typ: "text", payload: []byte{}, valid: true, wire: []byte{}},
		{name: "text_null", typ: "text", payload: nil, valid: true},
		{name: "text_nul", typ: "text", payload: []byte{'a', 0, 'b'}},
		{name: "text_invalid_utf8", typ: "text", payload: []byte{0xc3, 0x28}},
		{name: "text_nul_and_invalid_utf8", typ: "text", payload: []byte{0, 0xff}},
		{name: "varchar", typ: "varchar(3)", payload: []byte("hé"), valid: true, wire: []byte("hé")},
		{name: "varchar_overlong", typ: "varchar(3)", payload: []byte("four")},
		{name: "bpchar_padding", typ: "char(3)", payload: []byte("a")},
		{name: "bpchar_overlong", typ: "char(3)", payload: []byte("four")},
		{name: "name", typ: "name", payload: []byte("hello"), valid: true, wire: []byte("hello")},
		{name: "name_63", typ: "name", payload: bytes.Repeat([]byte{'a'}, 63)},
		{name: "name_64", typ: "name", payload: bytes.Repeat([]byte{'a'}, 64)},
		{name: "name_nul", typ: "name", payload: []byte{'a', 0, 'b'}},
		{name: "bytea", typ: "bytea", payload: h("00ff5c"), valid: true, wire: h("00ff5c")},
		{name: "uuid", typ: "uuid", payload: h("00112233445566778899aabbccddeeff"), valid: true, wire: h("00112233445566778899aabbccddeeff")},
		{name: "uuid_short", typ: "uuid", payload: make([]byte, 15)},
		{name: "uuid_trailing", typ: "uuid", payload: make([]byte, 17)},
	}
	// Bounded underflow/leftover probes for every fixed-width scalar receiver.
	for _, spec := range []struct {
		typ   string
		width int
	}{
		{"int2", 2}, {"int4", 4}, {"int8", 8}, {"oid", 4}, {"xid", 4}, {"regclass", 4}, {"regtype", 4}, {"float4", 4}, {"float8", 8}, {"money", 8}, {"bool", 1}, {`"char"`, 1},
	} {
		cases = append(cases, copyBinaryProviderCase{name: spec.typ + "_short", typ: spec.typ, payload: make([]byte, spec.width-1)}, copyBinaryProviderCase{name: spec.typ + "_trailing", typ: spec.typ, payload: make([]byte, spec.width+1)})
	}
	copyBinaryProviders(t, cases, copyBinaryScalarExpected)
}
