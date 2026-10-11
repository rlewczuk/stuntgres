package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyCSVInputConstraints(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_encoding(id int,v text)")
		c.exec(t, "INSERT INTO copy_csv_encoding VALUES(99,'sentinel')")
		for _, tc := range []struct{ input, message string }{
			{"1,ok\n2,\xff\n", "invalid byte sequence for encoding \"UTF8\": 0xff"},
			{"1,ok\n2,\x00\n", "invalid byte sequence for encoding \"UTF8\": 0x00"},
		} {
			result, err := c.from("COPY copy_csv_encoding FROM STDIN WITH(FORMAT csv)", strings.NewReader(tc.input))
			copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22021", Message: tc.message, Context: "COPY copy_csv_encoding, line 2"})
			c.selectRows(t, "SELECT * FROM copy_csv_encoding ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		}
		c.exec(t, "CREATE TABLE copy_csv_nn(id int,v text NOT NULL)")
		c.exec(t, "INSERT INTO copy_csv_nn VALUES(99,'sentinel')")
		result, err := c.from("COPY copy_csv_nn FROM STDIN WITH(FORMAT csv)", strings.NewReader("1,ok\n2,\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"copy_csv_nn\" violates not-null constraint", Detail: "Failing row contains (2, null).", Schema: "public", Table: "copy_csv_nn", Column: "v", Context: "COPY copy_csv_nn, line 2: \"2,\""})
		c.selectRows(t, "SELECT * FROM copy_csv_nn ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
	})
}
