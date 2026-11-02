package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsControls(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_view AS SELECT id,payload FROM sr TABLESAMPLE system_rows(3)", 0))
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_all AS SELECT id,payload FROM sr TABLESAMPLE system_rows(11)", 0))
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_zero_write AS SELECT id,payload FROM sr TABLESAMPLE system_rows(0)", 0))
		srRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE sr_calls START 1", 0))
		srControlPost(t, p, b, c)
		// Dump/Reset/Restore are native API mechanisms, not PostgreSQL SQL.
		if b.PG {
			return
		}
		srReplay(t, p, b, c)
	})
}

func srControlPost(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	srDeclaration(t, p, b, c)
	srViewWrites(t, p, b, c, "sr_zero_write")
	srViewWrites(t, p, b, c, "sr_view")
	srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM sr_view", 3))
	all := fixture.Select("SELECT id,payload FROM sr_all ORDER BY id", srNames, srTypes, srRows())
	all.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		// Views expose nullable output even when the base column is NOT NULL.
		fixture.TableDDLColumnTypes(t, b.PG, columns, srNames, srTypes, []bool{true, true})
	}
	srBoth(t, p, b, c, all)
	srBoth(t, p, b, c, srSubset("sr", "system_rows", "$1", 3, int64(3)))
	// The stored view still contains a sample frontier, not ten ordinary rows.
	for _, prepared := range []bool{false, true} {
		srRun(t, p, b, c, fixture.Exec("ALTER SEQUENCE sr_calls RESTART WITH 1", 0))
		s := srCount("SELECT count(*) AS n FROM sr_view WHERE nextval('sr_calls')<0", 0)
		s.Prepared = prepared
		srRun(t, p, b, c, s)
		srRun(t, p, b, c, fixture.Select("SELECT last_value FROM sr_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
	}
}

func srReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	h := srHeld(t, p, c, "SELECT count(*) AS n FROM sr TABLESAMPLE system_rows($1)")
	held := func() { srRun(t, p, b, h, srCount(h.SQL, 3, int64(3))) }
	held()
	var original int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'sr'::regclass::oid").Scan(&original); err != nil || original <= 0 {
		t.Fatal("source identity", original, err)
	}
	// A private insertion is deliberately absent from the committed replay.
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	tx, err := peer.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	srRun(t, p, b, tx, fixture.Exec("INSERT INTO sr VALUES(99,'uncommitted')", 1))
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	old, err := retained.QueryContext(p.Ctx, "SELECT id,payload FROM sr TABLESAMPLE system_rows(11) ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.TableDDLColumnTypes(t, false, metadata, srNames, srTypes, []bool{false, true})
	for i := int64(1); i <= 10; i++ {
		if !old.Next() {
			t.Fatal("retained sample truncated", i, old.Err())
		}
		var id int64
		var payload string
		if err := old.Scan(&id, &payload); err != nil || id != i || payload != fmt.Sprintf("v%d", i) {
			t.Fatal("retained sample", id, payload, err)
		}
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained sample tail", old.Err())
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	srRun(t, p, b, c, srCount("SELECT count(*) AS n FROM pg_extension WHERE extname='tsm_system_rows'", 0))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	srControlPost(t, p, b, c)
	held()
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
		t.Fatal("invalid replay accepted")
	}
	if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("failed replay published partial state")
	}
	srControlPost(t, p, b, c)
	held()
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled replay", err)
	}
	if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("canceled replay published partial state")
	}
	srControlPost(t, p, b, c)
	held()
	connector, err := stuntgres.NewDriver().OpenConnector("system-rows-independent-destination")
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	defer raw.Close()
	pool, recorder := fixture.RecordedNativePool(t, connector)
	dest, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	db := fixture.Backend{Name: "destination", DB: pool, Recorder: recorder}
	// Allocate genuine destination objects until the source relation address
	// has been occupied; replay must bind the new relation and handler graph.
	for n := 0; ; n++ {
		name := fmt.Sprintf("sr_destination_pad_%d", n)
		srRun(t, p, db, dest, fixture.Exec("CREATE TABLE "+name+"(v integer)", 0))
		var oid int64
		if err := dest.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid", name).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if oid >= original {
			break
		}
	}
	if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	var remapped int64
	if err := dest.QueryRowContext(p.Ctx, "SELECT 'sr'::regclass::oid").Scan(&remapped); err != nil || remapped <= 0 || remapped == original {
		t.Fatal("independent destination did not remap", original, remapped, err)
	}
	srControlPost(t, p, db, dest)
	srRun(t, p, db, dest, fixture.Exec("DELETE FROM sr WHERE id=1", 1))
	srBoth(t, p, db, dest, srCount("SELECT count(*) AS n FROM sr_all", 9))
	srControlPost(t, p, b, c)
	held()
}
