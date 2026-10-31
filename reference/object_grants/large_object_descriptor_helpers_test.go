package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6 be-fsstubs.c, inv_api.c and pg_proc.dat,
// not from native execution. All runtime handles are captured separately.
const descriptorRead = 262144
const descriptorWrite = 131072

func descriptorOpen(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared bool, oid uint32, mode, slot int) int {
	t.Helper()
	q := "SELECT lo_open($1::oid,$2::integer) AS v"
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := c.PrepareContext(p.Ctx, q)
		if e != nil { t.Fatal(e) }
		defer s.Close()
		rows, err = s.QueryContext(p.Ctx, int64(oid), mode)
	} else {
		rows, err = c.QueryContext(p.Ctx, q, int64(oid), mode)
	}
	if err != nil { t.Fatal(q, err) }
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil { t.Fatal(err) }
	types, err := rows.ColumnTypes()
	if err != nil { t.Fatal(err) }
	if len(cols) != 1 || cols[0] != "v" || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatalf("open metadata: columns=%v types=%v", cols, types)
	}
	if !rows.Next() { t.Fatal("open missing row", rows.Err()) }
	var fd int
	if err := rows.Scan(&fd); err != nil { t.Fatal(err) }
	if fd != slot { t.Fatalf("lo_open descriptor=%d want lowest free slot %d", fd, slot) }
	if rows.Next() { t.Fatal("open returned extra row") }
	if err := rows.Err(); err != nil { t.Fatal(err) }
	return fd
}

func descriptorReadStep(fd, length int, bytes string) fixture.Step {
	return largeObjectValue("loread($1::integer,$2::integer)", "BYTEA", bytes, fd, length)
}

func descriptorWriteStep(fd int, bytes []byte) fixture.Step {
	return largeObjectValue("lowrite($1::integer,$2::bytea)", "INT4", int64(len(bytes)), fd, bytes)
}

func descriptorClose(fd int) fixture.Step {
	return largeObjectValue("lo_close($1::integer)", "INT4", int64(0), fd)
}

func descriptorInvalid(query string, fd int, args ...any) fixture.Step {
	return largeObjectError(query, "42704", fmt.Sprintf("invalid large-object descriptor: %d", fd), args...)
}

// Expected errors abort the subtransaction, not the fixture's surrounding block.
func descriptorRecover(run func(fixture.Step), s fixture.Step) {
	run(fixture.Exec("SAVEPOINT descriptor_error", 0))
	run(s)
	run(fixture.Exec("ROLLBACK TO SAVEPOINT descriptor_error", 0))
	run(fixture.Exec("RELEASE SAVEPOINT descriptor_error", 0))
}

func descriptorPeer(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) (*sql.Conn, func(fixture.Step)) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() {
		if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil { t.Error(err) }
		if err := c.Close(); err != nil { t.Error(err) }
	})
	return c, func(s fixture.Step) {
		t.Helper()
		s.Prepared = prepared
		objectGrantStep(t, p.Ctx, c, b.Recorder, largeObjectBackendStep(s, b))
	}
}
