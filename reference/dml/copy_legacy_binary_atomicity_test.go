package reference_test

import (
	"bytes"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyLegacyBinaryAtomicity(t *testing.T) {
	for _, spelling := range []string{"BINARY", "WITH BINARY"} {
		t.Run(spelling, func(t *testing.T) {
			copyLegacyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_legacy_binary_atomic(i int4)")
				c.exec(t, "INSERT INTO copy_legacy_binary_atomic VALUES (99)")
				// Row 1 is valid and distinct from the committed sentinel. Row 2
				// has a complete field frame, but five bytes for its int4 value.
				// This reaches the receiver, not an incomplete-stream error.
				input := copyBinaryStream(
					copyBinaryRow(copyBinary32(1)),
					copyBinaryRow([]byte{0, 0, 0, 2, 0}),
				)
				sql := "COPY copy_legacy_binary_atomic FROM STDIN " + spelling
				result, err := c.from(sql, &copyTextChunks{text: string(input), widths: []int{1, 3, 2, 7}})
				copyTextDiagnostic(t, result, err, fixture.Diagnostic{
					Code: "22P03", Message: "incorrect binary data format",
					Context: "COPY copy_legacy_binary_atomic, line 2, column i",
				})
				c.selectRows(t, "SELECT * FROM copy_legacy_binary_atomic ORDER BY i",
					[]string{"i"}, []string{"INT4"}, [][]any{{int64(99)}})
				// Independently specified bytes prove the sentinel value, not just
				// the row count or equality with a previously captured export.
				c.output(t, "COPY copy_legacy_binary_atomic TO STDOUT WITH(FORMAT binary)",
					string(copyBinaryStream(copyBinaryRow(copyBinary32(99)))), 1)
				c.selectRows(t, "SELECT 42::int4 AS recovered",
					[]string{"recovered"}, []string{"INT4"}, [][]any{{int64(42)}})

				result, err = c.from(sql, bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(2)))))
				copyTextSuccess(t, result, err, 1)
				c.selectRows(t, "SELECT * FROM copy_legacy_binary_atomic ORDER BY i",
					[]string{"i"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(99)}})
			})
		})
	}
}
