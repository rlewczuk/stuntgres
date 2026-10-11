package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryConversionActiveCancellation(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE bc_active_input(id integer,b bytea)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE bc_active_out(id integer,b bytea)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO bc_active_out VALUES(0,'\xff')`, 1))
		// Both providers must scan a genuine large stored payload. Do not use a
		// substitute pg_sleep query, production delay, hook or context-check count.
		large := bytes.Repeat([]byte{0xe9}, 32<<20)
		inserted, err := c.ExecContext(p.Ctx, "INSERT INTO bc_active_input VALUES(1,$1),(2,$2)", []byte{0xe9}, large)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := inserted.RowsAffected(); err != nil || n != 2 {
			t.Fatal(n, err)
		}
		readSQL := "SELECT sum(octet_length(encode(b,'hex'))) AS n FROM bc_active_input"
		writeSQL := "INSERT INTO bc_active_out(id,b) SELECT id,convert(b,'LATIN1','UTF8') FROM bc_active_input ORDER BY id"
		read, err := c.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := c.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		fixture.BinaryConversionActiveCancel(t, p, b, c, readSQL, "sqltype.binaryFormatPass", func(ctx context.Context) fixture.RelationOutcome {
			rows, err := read.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		// SAME pre-held owner recovers after admitted read interruption.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE bc_active_input SET b='\xe9' WHERE id=2`, 1))
		heldRead := fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		changed, err := c.ExecContext(p.Ctx, "UPDATE bc_active_input SET b=$1 WHERE id=2", large)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := changed.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		fixture.BinaryConversionActiveCancel(t, p, b, c, writeSQL, "sqltype.binaryEncodingPass", func(ctx context.Context) fixture.RelationOutcome {
			result, err := write.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		// The small first row precedes the large conversion in the ordered staged
		// write; interruption must publish neither it nor any partial large value.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,b FROM bc_active_out ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "\xff"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE bc_active_input SET b='\xe9' WHERE id=2`, 1))
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,b FROM bc_active_out ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "\xff"}, {int64(1), "é"}, {int64(2), "é"}}))
	})
}
