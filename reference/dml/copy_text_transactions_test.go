package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyTextTransactions(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_tx(id int,v text)")
		c.exec(t, "INSERT INTO copy_tx VALUES(99,'sentinel')")
		peerDB := p.Native
		if b.PG {
			peerDB = p.CopyPostgres
		}
		peer, err := peerDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		observer := copyTextConnection{conn: peer, pg: b.PG, ctx: p.Ctx}
		sentinel := [][]any{{int64(99), "sentinel"}}
		names, types := []string{"id", "v"}, []string{"INT4", "TEXT"}
		c.exec(t, "BEGIN")
		result, err := c.from("COPY copy_tx FROM STDIN", strings.NewReader("1\tone\n2\ttwo\n"))
		copyTextSuccess(t, result, err, 2)
		c.selectRows(t, "SELECT * FROM copy_tx ORDER BY id", names, types, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(99), "sentinel"}})
		observer.selectRows(t, "SELECT * FROM copy_tx ORDER BY id", names, types, sentinel)
		c.exec(t, "ROLLBACK")
		observer.selectRows(t, "SELECT * FROM copy_tx ORDER BY id", names, types, sentinel)
		c.exec(t, "BEGIN")
		c.exec(t, "SAVEPOINT copy_mark")
		result, err = c.from("COPY copy_tx FROM STDIN", strings.NewReader("1\tone\nbad\ttwo\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: `COPY copy_tx, line 2, column id: "bad"`})
		_, err = c.conn.ExecContext(c.ctx, "SELECT * FROM copy_tx")
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		c.exec(t, "ROLLBACK TO copy_mark")
		result, err = c.from("COPY copy_tx FROM STDIN", strings.NewReader("3\tthree\n"))
		copyTextSuccess(t, result, err, 1)
		observer.selectRows(t, "SELECT * FROM copy_tx ORDER BY id", names, types, sentinel)
		c.exec(t, "COMMIT")
		observer.selectRows(t, "SELECT * FROM copy_tx ORDER BY id", names, types, [][]any{{int64(3), "three"}, {int64(99), "sentinel"}})
		c.exec(t, "BEGIN READ ONLY")
		c.output(t, "COPY (SELECT * FROM copy_tx ORDER BY id) TO STDOUT", "3\tthree\n99\tsentinel\n", 2)
		result, err = c.from("COPY copy_tx FROM STDIN", strings.NewReader("4\tfour\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "25006", Message: "cannot execute COPY FROM in a read-only transaction"})
		c.exec(t, "ROLLBACK")
		observer.selectRows(t, "SELECT * FROM copy_tx ORDER BY id", names, types, [][]any{{int64(3), "three"}, {int64(99), "sentinel"}})
	})
}
