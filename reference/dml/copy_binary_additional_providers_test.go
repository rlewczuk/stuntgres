package reference_test

import (
	"bytes"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyBinaryForbiddenInput(t *testing.T) {
	for _, phase := range []string{"catalog_receive", "table_receive"} {
		t.Run(phase, func(t *testing.T) {
			copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_binary_tree_source(i int4 DEFAULT 42)")
				if phase == "table_receive" {
					_, err := c.conn.ExecContext(c.ctx, "CREATE TABLE copy_binary_tree_target(v pg_node_tree)")
					if err != nil {
						t.Fatal("tree-typed table creation", err)
					}
					result, err := c.from("COPY copy_binary_tree_target FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow([]byte("not a node tree")))))
					copyBinaryAdditionalOutcome(t, result, err, nil)
					return
				}
				// A genuine catalog target supplies an existing tree-typed input surface.
				// Transaction rollback prevents publication even on surprising acceptance.
				c.exec(t, "BEGIN")
				reader := &copyTextChunks{text: string(copyBinaryStream(copyBinaryRow([]byte("not a node tree")))), widths: []int{1}}
				result, err := c.from("COPY pg_catalog.pg_attrdef(adbin) FROM STDIN WITH(FORMAT binary)", reader)
				if c.pg {
					copyBinaryAdditionalOutcome(t, result, err, nil)
				} else {
					// Catalog mutation is not an executable table surface in native.
					// Keep its existing kind boundary, not an invented receiver path.
					copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42809", Message: "cannot copy to non-table relation \"pg_attrdef\""})
					if reader.offset != 0 {
						t.Fatalf("catalog admission consumed %d bytes", reader.offset)
					}
				}
				c.exec(t, "ROLLBACK")
				// Both branches inspect the genuine unchanged catalog expression.
				c.selectRows(t, "SELECT pg_get_expr(d.adbin,d.adrelid) AS expression FROM pg_attrdef d JOIN pg_class c ON d.adrelid=c.oid WHERE c.relname='copy_binary_tree_source'", []string{"expression"}, []string{"TEXT"}, [][]any{{"42"}})
			})
		})
	}
}

func TestReferenceCopyBinaryAdditionalProviders(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		payload   []byte
	}{
		{name: "array_null_flag_zero", typ: "int4[]", payload: copyBinaryHex("0000000100000000000000170000000100000001ffffffff")},
		{name: "array_zero_dimension", typ: "int4[]", payload: copyBinaryHex("0000000100000000000000170000000000000001")},
		{name: "array_lower_bound_overflow", typ: "int4[]", payload: copyBinaryHex("000000010000000000000017000000027fffffff")},
		{name: "array_rank_before_flags", typ: "int4[]", payload: copyBinaryHex("ffffffff0000000200000019")},
		{name: "array_flags_before_oid", typ: "int4[]", payload: copyBinaryHex("000000000000000200000019")},
		{name: "composite_missing_oid", typ: "copy_pair", payload: copyBinary32(2)},
		{name: "composite_negative_member_length", typ: "copy_pair", payload: copyBinaryHex("0000000200000017fffffffe")},
		{name: "range_lower_before_upper_underflow", typ: "int4range", payload: copyBinaryRange(2, []byte{0})},
		{name: "multirange_member_trailing", typ: "int4multirange", payload: copyBinaryMultirange(append(copyBinaryRange(2, copyBinary32(1), copyBinary32(3)), 0))},
		{name: "name_length_vs_nul", typ: "name", payload: append(bytes.Repeat([]byte{'a'}, 63), 0)},
		{name: "varchar_typmod_vs_utf8", typ: "varchar(3)", payload: []byte{'a', 'b', 'c', 'd', 255}},
		{name: "jsonb_version_vs_utf8", typ: "jsonb", payload: []byte{2, 255}},
		{name: "jsonpath_version_vs_nul", typ: "jsonpath", payload: []byte{2, 0}},
		{name: "numeric_sign_vs_missing_digit", typ: "numeric", payload: copyBinaryHex("0001000020000000")},
		{name: "numeric_sign_scale_digit_precedence", typ: "numeric", payload: copyBinaryHex("00010000200040002710")},
		{name: "composite_oid_vs_missing_length", typ: "copy_pair", payload: copyBinaryHex("0000000200000019")},
		{name: "array_oid_vs_missing_dimensions", typ: "int4[]", payload: copyBinaryHex("000000010000000000000019")},
		{name: "bit_typmod_vs_trailing", typ: "bit(4)", payload: copyBinaryHex("00000003a0ff")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
				if tc.typ == "copy_pair" {
					c.exec(t, "CREATE TYPE copy_pair AS(i int4,t text)")
				}
				c.exec(t, "CREATE TABLE copy_binary_extra(v "+tc.typ+")")
				result, err := c.from("COPY copy_binary_extra FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(tc.payload))))
				if tc.name != "array_null_flag_zero" && tc.name != "array_zero_dimension" {
					copyBinaryAdditionalOutcome(t, result, err, nil)
					var count int64
					if err := c.conn.QueryRowContext(c.ctx, "SELECT count(*) FROM copy_binary_extra").Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != 0 {
						t.Errorf("failed nested receive published %d rows", count)
					}
					return
				}
				copyTextSuccess(t, result, err, 1)
				wantValue := "{NULL}"
				wantPayload := copyBinaryArray(23, [][2]int32{{1, 1}}, nil)
				if tc.name == "array_zero_dimension" {
					wantValue = "{}"
					wantPayload = copyBinaryArray(23, nil)
				}
				c.selectRows(t, "SELECT v FROM copy_binary_extra", []string{"v"}, []string{"_INT4"}, [][]any{{wantValue}})
				c.output(t, "COPY copy_binary_extra TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow(wantPayload))), 1)
			})
		})
	}
	for _, length := range []int32{1073741824, 2147483646} {
		t.Run(fmt.Sprintf("outer_length_%d", length), func(t *testing.T) {
			copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_binary_extra(v int4)")
				c.exec(t, "INSERT INTO copy_binary_extra VALUES(99)")
				input := append(copyBinaryHeader(0, 0, nil), copyBinaryRow(copyBinary32(42))...)
				input = append(input, copyBinary16(1)...)
				input = append(input, copyBinary32(length)...)
				// Both values exceed the observed MaxAllocSize. No near-valid 1GB
				// request and no materialized large payload is submitted to the oracle.
				result, err := c.from("COPY copy_binary_extra FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
				copyBinaryAdditionalOutcome(t, result, err, nil)
				if err != nil {
					c.selectRows(t, "SELECT * FROM copy_binary_extra", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
				}
			})
		})
	}
	t.Run("nested_domain_not_null", func(t *testing.T) {
		copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
			if copyBinaryDomainBoundary(t, c, "CREATE DOMAIN copy_required AS int4 NOT NULL") {
				return
			}
			c.exec(t, "CREATE DOMAIN copy_required AS int4 NOT NULL")
			c.exec(t, "CREATE TABLE copy_binary_required(v copy_required[])")
			c.exec(t, "INSERT INTO copy_binary_required VALUES('{99}')")
			oid := copyBinaryTypeOID(t, c, "copy_required")
			valid := copyBinaryArray(oid, [][2]int32{{1, 1}}, copyBinary32(42))
			invalid := copyBinaryArray(oid, [][2]int32{{1, 1}}, nil)
			var before bytes.Buffer
			result, err := c.to("COPY copy_binary_required TO STDOUT WITH(FORMAT binary)", &before)
			copyTextSuccess(t, result, err, 1)
			result, err = c.from("COPY copy_binary_required FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(valid), copyBinaryRow(invalid))))
			copyBinaryAdditionalOutcome(t, result, err, nil)
			if err != nil {
				var after bytes.Buffer
				result, err = c.to("COPY copy_binary_required TO STDOUT WITH(FORMAT binary)", &after)
				copyTextSuccess(t, result, err, 1)
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Error("NULL domain member failure mutated committed array sentinel")
				}
			}
		})
	})
}
