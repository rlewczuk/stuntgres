package reference_test

import (
	"bytes"
	"testing"

	"stuntgres/reference/fixture"
)

func copyLegacyBinaryScenario(t *testing.T, body func(*testing.T, copyTextConnection)) {
	t.Helper()
	copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
		body(t, c)
	})
}

func TestReferenceCopyLegacyBinaryRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
	}{
		{"bare", `COPY public."Legacy Rows" FROM STDIN BINARY`},
		{"with", `COPY public."Legacy Rows" FROM STDIN WITH BINARY`},
		{"mixed_bare_columns", `cOpY public."Legacy Rows" ("I", "t""ext", "b") FrOm StDiN BiNaRy`},
		{"mixed_with_columns", `cOpY public."Legacy Rows" ("I", "t""ext", "b") FrOm StDiN WiTh BiNaRy`},
		{"modern_parenthesized", `COPY public."Legacy Rows" FROM STDIN (FORMAT binary)`},
		{"modern_with", `COPY public."Legacy Rows" FROM STDIN WITH(FORMAT binary)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyLegacyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, `CREATE TABLE public."Legacy Rows" ("I" int4,"t""ext" text,"b" bool)`)
				// PostgreSQL's documented binary framing, independently written:
				// (42,"hi",true), (NULL,"",false), then the end marker.
				input := copyBinaryHex("5047434f50590aff0d0a0000000000000000000003000000040000002a00000002686900000001010003ffffffff000000000000000100ffff")
				result, err := c.from(tc.sql, &copyTextChunks{text: string(input), widths: []int{1, 3, 7}})
				copyTextSuccess(t, result, err, 2)
				c.selectRows(t, `SELECT * FROM public."Legacy Rows" ORDER BY "I" NULLS LAST`,
					[]string{"I", `t"ext`, "b"}, []string{"INT4", "TEXT", "BOOL"},
					[][]any{{int64(42), "hi", true}, {nil, "", false}})
				for _, format := range []string{"(FORMAT binary)", "WITH(FORMAT binary)"} {
					c.output(t, `COPY (SELECT * FROM public."Legacy Rows" ORDER BY "I" NULLS LAST) TO STDOUT `+format, string(input), 2)
				}
			})
		})
	}
}

func TestReferenceCopyLegacyBinaryColumns(t *testing.T) {
	for _, spelling := range []string{"BINARY", "WITH BiNaRy"} {
		t.Run(spelling, func(t *testing.T) {
			copyLegacyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, `CREATE TABLE public."Legacy Columns" (i int4,t text,b bool DEFAULT true)`)
				empty := copyBinaryHex("5047434f50590aff0d0a000000000000000000ffff")
				result, err := c.from(`COPY public."Legacy Columns" FROM STDIN `+spelling, bytes.NewReader(empty))
				copyTextSuccess(t, result, err, 0)
				c.selectRows(t, `SELECT * FROM public."Legacy Columns" ORDER BY i`,
					[]string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{})
				c.output(t, `COPY public."Legacy Columns" TO STDOUT WITH(FORMAT binary)`, string(empty), 0)

				input := copyBinaryStream(copyBinaryRow([]byte("reordered"), copyBinary32(7)))
				result, err = c.from(`COPY public."Legacy Columns" ("t","i") FROM STDIN `+spelling, bytes.NewReader(input))
				copyTextSuccess(t, result, err, 1)
				c.selectRows(t, `SELECT * FROM public."Legacy Columns" ORDER BY i`,
					[]string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "reordered", true}})
				want := copyBinaryStream(copyBinaryRow(copyBinary32(7), []byte("reordered"), []byte{1}))
				c.output(t, `COPY (SELECT * FROM public."Legacy Columns" ORDER BY i) TO STDOUT (FORMAT binary)`, string(want), 1)

				c.exec(t, `CREATE TABLE public."Legacy Nullable" (i int4,t text,b bool DEFAULT true)`)
				result, err = c.from(`COPY public."Legacy Nullable" ("i") FROM STDIN `+spelling,
					bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(8)))))
				copyTextSuccess(t, result, err, 1)
				c.selectRows(t, `SELECT * FROM public."Legacy Nullable" ORDER BY i`,
					[]string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(8), nil, true}})
				want = copyBinaryStream(copyBinaryRow(copyBinary32(8), nil, []byte{1}))
				c.output(t, `COPY public."Legacy Nullable" TO STDOUT WITH(FORMAT binary)`, string(want), 1)
			})
		})
	}
}
