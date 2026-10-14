package reference_test

import (
	"bytes"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDateRangeBinary(t *testing.T) {
	copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_date_range(k integer NOT NULL,v daterange)")
		// Inclusive singleton day zero becomes finite [0,1). DATE infinities
		// are present four-byte signed sentinels, not unbounded range flags.
		finiteInput := copyBinaryRange(6, copyBinary32(0), copyBinary32(0))
		finiteOutput := copyBinaryRange(2, copyBinary32(0), copyBinary32(1))
		infinity := copyBinaryRange(6, copyBinary32(math.MinInt32), copyBinary32(math.MaxInt32))
		input := copyBinaryStream(
			copyBinaryRow(copyBinary32(1), finiteInput),
			copyBinaryRow(copyBinary32(2), infinity),
			copyBinaryRow(copyBinary32(3), []byte{0x18}),
			copyBinaryRow(copyBinary32(4), []byte{0x01}),
			copyBinaryRow(copyBinary32(5), nil),
		)
		result, err := c.from("COPY copy_date_range FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
		copyTextSuccess(t, result, err, 5)
		want := [][]any{{int64(1), "[2000-01-01,2000-01-02)"}, {int64(2), "[-infinity,infinity]"}, {int64(3), "(,)"}, {int64(4), "empty"}, {int64(5), nil}}
		query := "SELECT k,v FROM copy_date_range ORDER BY k"
		c.selectRows(t, query, []string{"k", "v"}, []string{"INT4", "DATERANGE"}, want)
		wire := copyBinaryStream(
			copyBinaryRow(copyBinary32(1), finiteOutput),
			copyBinaryRow(copyBinary32(2), infinity),
			copyBinaryRow(copyBinary32(3), []byte{0x18}),
			copyBinaryRow(copyBinary32(4), []byte{0x01}),
			copyBinaryRow(copyBinary32(5), nil),
		)
		output := "COPY ("+query+") TO STDOUT WITH(FORMAT binary)"
		c.output(t, output, string(wire), 5)
		for _, tc := range []struct{ payload []byte; code, message string }{
			{copyBinaryRange(2, copyBinary32(2), copyBinary32(1)), "22000", "range lower bound must be less than or equal to range upper bound"},
			{copyBinaryRange(6, copyBinary32(2145031948), copyBinary32(2145031948)), "22008", "date out of range"},
		} {
			bad := copyBinaryStream(copyBinaryRow(copyBinary32(90), finiteInput), copyBinaryRow(copyBinary32(91), tc.payload))
			result, err := c.from("COPY copy_date_range FROM STDIN WITH(FORMAT binary)", bytes.NewReader(bad))
			wantError := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			wantError.Context = "COPY copy_date_range, line 2, column v"
			copyTextDiagnostic(t, result, err, wantError)
			c.selectRows(t, query, []string{"k", "v"}, []string{"INT4", "DATERANGE"}, want)
			c.output(t, output, string(wire), 5)
		}
	})
}
