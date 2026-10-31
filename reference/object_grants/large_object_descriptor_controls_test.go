package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectDescriptorControls(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910189)
		run(largeObjectValue("lo_from_bytea(910189::oid,decode('616263646566','hex'))", "OID", int64(910189)))
		q := "SELECT lo_open($1::oid,$2::integer) AS v"
		open, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer open.Close()
		readQ := "SELECT loread($1::integer,$2::integer) AS v"
		read, err := c.PrepareContext(p.Ctx, readQ)
		if err != nil { t.Fatal(err) }
		defer read.Close()
		heldOpen := fixture.HeldStatement{Executor:c, Statement:open, SQL:q}
		heldRead := fixture.HeldStatement{Executor:c, Statement:read, SQL:readQ}
		run(fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, heldOpen, b.Recorder, largeObjectValue("lo_open($1::oid,$2::integer)", "INT4", int64(0), int64(910189), descriptorRead))
		w := descriptorOpen(t, p, c, prepared, 910189, descriptorWrite, 1)
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, descriptorReadStep(0, 2, "ab"))
		run(descriptorWriteStep(w, []byte("XY")))
		// Retain actual client byte storage and metadata beyond subsequent controls.
		rows, err := read.QueryContext(p.Ctx, 0, 2)
		if err != nil { t.Fatal(err) }
		columns, err := rows.Columns()
		if err != nil { rows.Close(); t.Fatal(err) }
		types, err := rows.ColumnTypes()
		if err != nil { rows.Close(); t.Fatal(err) }
		if !rows.Next() { rows.Close(); t.Fatal("missing retained byte row") }
		var retained []byte
		if err := rows.Scan(&retained); err != nil { rows.Close(); t.Fatal(err) }
		if rows.Next() { rows.Close(); t.Fatal("extra retained row") }
		if err := rows.Err(); err != nil { rows.Close(); t.Fatal(err) }
		if err := rows.Close(); err != nil { t.Fatal(err) }
		assertRetained := func() {
			t.Helper()
			if string(retained) != "cd" || len(columns) != 1 || columns[0] != "v" || len(types) != 1 || types[0].DatabaseTypeName() != "BYTEA" {
				t.Fatalf("retained result bytes=%q columns=%v types=%v", retained, columns, types)
			}
		}
		assertRetained()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
			for _, call := range []string{"lo_open(","lo_close(","loread(","lowrite("} {
				if strings.Contains(dump.String(), call) { t.Fatal("dump serialized descriptor operation", call) }
			}
			fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, descriptorReadStep(0, 20, "ef"))
			run(descriptorReadStep(w, 20, "cdef"))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			_, err := read.ExecContext(p.Ctx, 0, 1)
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			run(fixture.Exec("ROLLBACK", 0))
			run(descriptorInvalid("SELECT loread(0,1)", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			assertRetained()
			run(fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, heldOpen, b.Recorder, largeObjectValue("lo_open($1::oid,$2::integer)", "INT4", int64(0), int64(910189), descriptorRead))
			fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, descriptorReadStep(0, 20, "abcdef"))
			// Restore itself also fences a transaction with already-open resources.
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			_, err = read.ExecContext(p.Ctx, 0, 1)
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			run(fixture.Exec("ROLLBACK", 0))
		} else {
			run(fixture.Exec("ROLLBACK", 0))
		}
		run(descriptorInvalid("SELECT loread(0,1)", 0))
		run(fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, heldOpen, b.Recorder, largeObjectValue("lo_open($1::oid,$2::integer)", "INT4", int64(0), int64(910189), descriptorRead))
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, descriptorReadStep(0, 20, "abcdef"))
		run(fixture.Exec("COMMIT", 0))
		assertRetained()
	})
}
