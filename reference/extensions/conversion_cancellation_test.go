package reference_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConversionCancellation(t *testing.T) {
	conversionScenario(t, []string{"conv_cancel_hidden"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, `CREATE SCHEMA conv_cancel_hidden`)
		conversionHideNamedSeed(t, p, b, "conv_cancel_hidden", "iso_8859_1_to_utf8")
		conversionError(t, p, b, `SELECT convert_from(decode('e9','hex'),'LATIN1')`, conversionDiagnostic("42883", `default conversion function for encoding "LATIN1" to "UTF8" does not exist`), false)
		conversionExec(t, p, b, `CREATE DEFAULT CONVERSION public.conv_cancel_default FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8`, `CREATE TABLE conv_cancel_input(id integer,b bytea)`, `CREATE TABLE conv_cancel_out(id integer,b bytea)`)
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`SET search_path=public,pg_catalog`, 0))
		run(fixture.Exec(`INSERT INTO conv_cancel_out VALUES(0,decode('ff','hex'))`, 1))
		large := bytes.Repeat([]byte{0xe9}, 32<<20)
		result, err := conn.ExecContext(p.Ctx, `INSERT INTO conv_cancel_input VALUES(1,$1),(2,$2)`, []byte{0xe9}, large)
		fixture.DeleteReferenceCount(t, result, err, 2)
		readSQL := `SELECT sum(octet_length(convert(b,'LATIN1','UTF8'))) AS n FROM conv_cancel_input`
		writeSQL := `INSERT INTO conv_cancel_out SELECT id,convert(b,'LATIN1','UTF8') FROM conv_cancel_input ORDER BY id`
		read, err := conn.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := conn.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		fixture.BinaryConversionActiveCancel(t, p, b, conn, readSQL, "sqltype.binaryEncodingPass", func(ctx context.Context) fixture.RelationOutcome {
			rows, err := read.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(fixture.Exec(`UPDATE conv_cancel_input SET b=decode('e9','hex') WHERE id=2`, 1))
		heldRead := fixture.HeldStatement{Executor: conn, Statement: read, SQL: readSQL}
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		result, err = conn.ExecContext(p.Ctx, `UPDATE conv_cancel_input SET b=$1 WHERE id=2`, large)
		fixture.DeleteReferenceCount(t, result, err, 1)
		fixture.BinaryConversionActiveCancel(t, p, b, conn, writeSQL, "sqltype.binaryEncodingPass", func(ctx context.Context) fixture.RelationOutcome {
			result, err := write.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(fixture.Select(`SELECT id,b FROM conv_cancel_out ORDER BY id`, []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "\xff"}}))
		run(fixture.Exec(`UPDATE conv_cancel_input SET b=decode('e9','hex') WHERE id=2`, 1))
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 2))
		run(fixture.Select(`SELECT id,b FROM conv_cancel_out ORDER BY id`, []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "\xff"}, {int64(1), "é"}, {int64(2), "é"}}))
	})
}
