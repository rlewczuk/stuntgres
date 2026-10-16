package reference_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspCopyBinary(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		if b.PG {
			clean := func() { c.exec(t, "DROP EXTENSION IF EXISTS \"uuid-ossp\" CASCADE") }
			clean()
			defer clean()
		}
		c.exec(t, "CREATE EXTENSION \"uuid-ossp\"")
		c.exec(t, "CREATE TABLE uuid_ossp_binary(u uuid)")
		c.exec(t, "INSERT INTO uuid_ossp_binary VALUES(uuid_generate_v4())")
		rows, err := c.conn.QueryContext(c.ctx, "SELECT u FROM uuid_ossp_binary")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != 1 || cols[0].Name() != "u" || cols[0].DatabaseTypeName() != "UUID" {
			t.Fatal("UUID metadata", cols, err)
		}
		if !rows.Next() {
			t.Fatal("missing generated UUID", rows.Err())
		}
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("UUID rows tail", rows.Err())
		}
		rows.Close()
		if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' || strings.ToLower(text) != text {
			t.Fatalf("noncanonical UUID %q", text)
		}
		raw, err := hex.DecodeString(strings.ReplaceAll(text, "-", ""))
		if err != nil || len(raw) != 16 {
			t.Fatal(text, err)
		}
		if raw[6]>>4 != 4 || raw[8]>>6 != 2 {
			t.Fatal("UUID version/variant", text)
		}
		want := copyBinaryStream(copyBinaryRow(raw))
		var out bytes.Buffer
		result, err := c.to("COPY uuid_ossp_binary TO STDOUT WITH(FORMAT binary)", &out)
		copyTextSuccess(t, result, err, 1)
		if !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("binary UUID frame=%x want=%x", out.Bytes(), want)
		}
		c.exec(t, "CREATE TABLE uuid_ossp_binary_reimport(u uuid)")
		result, err = c.from("COPY uuid_ossp_binary_reimport FROM STDIN WITH(FORMAT binary)", bytes.NewReader(want))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT u FROM uuid_ossp_binary_reimport", []string{"u"}, []string{"UUID"}, [][]any{{text}})
		c.output(t, "COPY uuid_ossp_binary_reimport TO STDOUT WITH(FORMAT binary)", string(want), 1)
	})
}
