package reference_test

import (
	"bytes"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyBinaryAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name, ddl  string
		setup      []string
		first, bad []byte
	}{
		{name: "not_null", ddl: "CREATE TABLE copy_binary_atomic(i int4 NOT NULL,t text)", first: copyBinaryRow(copyBinary32(42), []byte("valid")), bad: copyBinaryRow(nil, []byte("bad"))},
		{name: "unique", ddl: "CREATE TABLE copy_binary_atomic(i int4 UNIQUE,t text)", first: copyBinaryRow(copyBinary32(42), []byte("valid")), bad: copyBinaryRow(copyBinary32(99), []byte("duplicate"))},
		{name: "check", ddl: "CREATE TABLE copy_binary_atomic(i int4 CHECK(i>0),t text)", first: copyBinaryRow(copyBinary32(42), []byte("valid")), bad: copyBinaryRow(copyBinary32(-1), []byte("bad"))},
		{name: "domain_not_null", setup: []string{"CREATE DOMAIN copy_required AS int4 NOT NULL"}, ddl: "CREATE TABLE copy_binary_atomic(i copy_required,t text)", first: copyBinaryRow(copyBinary32(42), []byte("valid")), bad: copyBinaryRow(nil, []byte("bad"))},
		{name: "partition_missing", ddl: "CREATE TABLE copy_binary_atomic(i int4,t text) PARTITION BY RANGE(i)", setup: []string{}, first: copyBinaryRow(copyBinary32(42), []byte("valid")), bad: copyBinaryRow(copyBinary32(101), []byte("outside"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
				for _, q := range tc.setup {
					c.exec(t, q)
				}
				c.exec(t, tc.ddl)
				if tc.name == "partition_missing" {
					c.exec(t, "CREATE TABLE copy_binary_atomic_child PARTITION OF copy_binary_atomic FOR VALUES FROM(0) TO(100)")
				}
				c.exec(t, "INSERT INTO copy_binary_atomic VALUES(99,'sentinel')")
				result, err := c.from("COPY copy_binary_atomic FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(tc.first, tc.bad)))
				copyBinaryAdditionalOutcome(t, result, err, nil)
				if err == nil {
					t.Error("independently invalid constraint input succeeded")
					return
				}
				c.selectRows(t, "SELECT * FROM copy_binary_atomic", []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
			})
		})
	}
}

func TestReferenceCopyBinaryTransactions(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_binary_tx(i int4,t text)")
		c.exec(t, "INSERT INTO copy_binary_tx VALUES(99,'sentinel')")
		db := p.Native
		if b.PG {
			db = p.CopyPostgres
		}
		peer, err := db.Conn(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		observer := copyTextConnection{conn: peer, ctx: c.ctx, pg: b.PG}
		names, types := []string{"i", "t"}, []string{"INT4", "TEXT"}
		sentinel := [][]any{{int64(99), "sentinel"}}
		query := "COPY copy_binary_tx FROM STDIN WITH(FORMAT binary)"
		first := copyBinaryRow(copyBinary32(1), []byte("one"))
		c.exec(t, "BEGIN")
		result, err := c.from(query, bytes.NewReader(copyBinaryStream(first)))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT * FROM copy_binary_tx ORDER BY i", names, types, [][]any{{int64(1), "one"}, {int64(99), "sentinel"}})
		observer.selectRows(t, "SELECT * FROM copy_binary_tx", names, types, sentinel)
		c.exec(t, "ROLLBACK")
		observer.selectRows(t, "SELECT * FROM copy_binary_tx", names, types, sentinel)
		c.exec(t, "BEGIN")
		c.exec(t, "SAVEPOINT binary_mark")
		result, err = c.from(query, bytes.NewReader(copyBinaryStream(first, copyBinaryRow([]byte{0}, []byte("bad")))))
		copyBinaryAdditionalOutcome(t, result, err, nil, "savepoint")
		if err == nil {
			t.Fatal("short integer in explicit transaction succeeded")
		}
		_, err = c.conn.ExecContext(c.ctx, "SELECT * FROM copy_binary_tx")
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		observer.selectRows(t, "SELECT * FROM copy_binary_tx", names, types, sentinel)
		c.exec(t, "ROLLBACK TO binary_mark")
		result, err = c.from(query, bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(3), []byte("three")))))
		copyTextSuccess(t, result, err, 1)
		observer.selectRows(t, "SELECT * FROM copy_binary_tx", names, types, sentinel)
		c.exec(t, "COMMIT")
		committed := [][]any{{int64(3), "three"}, {int64(99), "sentinel"}}
		observer.selectRows(t, "SELECT * FROM copy_binary_tx ORDER BY i", names, types, committed)
		c.exec(t, "BEGIN READ ONLY")
		c.output(t, "COPY (SELECT * FROM copy_binary_tx ORDER BY i) TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow(copyBinary32(3), []byte("three")), copyBinaryRow(copyBinary32(99), []byte("sentinel")))), 2)
		result, err = c.from(query, bytes.NewReader(copyBinaryStream(first)))
		copyBinaryAdditionalOutcome(t, result, err, nil, "readonly")
		c.exec(t, "ROLLBACK")
		observer.selectRows(t, "SELECT * FROM copy_binary_tx ORDER BY i", names, types, committed)
	})
}
