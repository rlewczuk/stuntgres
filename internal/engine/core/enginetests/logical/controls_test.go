package logical

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/sqlstate"
)

// Reset and Restore replace the database's contents, so its slots and their
// history go with them; Dump never carries slots. An active slot refuses the
// replacement and nothing changes.
func TestLogicalStreamNativeControls(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int)")
	f.slot(s, "persist")
	f.must(s, "INSERT INTO t VALUES (1)")
	ctx := context.Background()

	var dump bytes.Buffer
	if err := engine.Dump(ctx, f.e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "replication_slot") {
		t.Fatal("dump serialized a slot")
	}

	holder := f.session()
	f.must(holder, "SELECT pg_create_logical_replication_slot('held', 'recorder', true)")
	pid := f.value(holder, "SELECT pg_backend_pid()::text").(string)
	var d *sqlstate.Error
	if err := engine.Reset(ctx, f.e); !errors.As(err, &d) || d.Code != "55006" || d.Message != `replication slot "held" is active for PID `+pid {
		t.Fatal("reset with an active slot", err)
	}
	f.expect(f.peek(s, "persist"), tx(row("1"))...)
	if got := f.value(s, "SELECT count(*) FROM t"); got != "1" {
		t.Fatal("refused reset changed data", got)
	}
	endSession(t, holder)

	if err := engine.Reset(ctx, f.e); err != nil {
		t.Fatal(err)
	}
	s = f.session()
	if got := f.value(s, "SELECT count(*) FROM pg_replication_slots"); got != "0" {
		t.Fatal("reset kept slots", got)
	}
	f.fails(s, "SELECT * FROM pg_logical_slot_get_changes('persist', NULL, NULL)", "42704", `replication slot "persist" does not exist`)

	f.slot(s, "persist")
	if err := engine.Restore(ctx, f.e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	s = f.session()
	if got := f.value(s, "SELECT count(*) FROM pg_replication_slots"); got != "0" {
		t.Fatal("restore kept slots", got)
	}
	if got := f.value(s, "SELECT count(*) FROM t"); got != "1" {
		t.Fatal("restored data", got)
	}
	// A new slot sees only history after its creation, not the restored rows.
	f.slot(s, "after")
	f.must(s, "INSERT INTO t VALUES (2)")
	f.expect(f.changes(s, "after"), tx(row("2"))...)
}
