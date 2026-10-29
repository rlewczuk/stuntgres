package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func uuidOsspControls(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	saved := uuidOsspConsumers(t, p, c, r, pg)
	q := "SELECT uuid_ossp_part.uuid_generate_v4() AS u"
	held, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	first := uuidOsspRead(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, pg, q, 1, 1)[0]
	if pg {
		return
	}
	old, err := c.QueryContext(p.Ctx, "SELECT u FROM uuid_ossp_saved ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	cols, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for i, v := range saved {
		if !old.Next() {
			t.Fatal("lost retained row", i, old.Err())
		}
		var got string
		if err := old.Scan(&got); err != nil || got != v {
			t.Fatal("retained UUID", got, err)
		}
	}
	if old.Next() || old.Err() != nil || len(cols) != 1 || cols[0].DatabaseTypeName() != "UUID" {
		t.Fatal("retained metadata/tail", old.Err())
	}
	old.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post := func(conn *sql.Conn, rec *fixture.Recorder) {
		if got := uuidOsspSaved(t, p, conn, rec); !reflect.DeepEqual(got, saved) {
			t.Fatal("saved UUID changed", got, saved)
		}
		uuidOsspInventory(t, p, conn, "postgres", "postgres")
		uuidOsspRead(t, p, conn, rec, false, q, 1, 1)
		uuidOsspRead(t, p, conn, rec, false, "SELECT u FROM uuid_ossp_view ORDER BY id", 2, 1, true)
		uuidOsspRead(t, p, conn, rec, false, "SELECT u FROM uuid_ossp_nested ORDER BY id", 2, 1, true)
	}
	post(c, r)
	if v := uuidOsspRead(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, false, q, 1, 1)[0]; v == first {
		t.Fatal("held value cached across restore")
	}
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	for _, bad := range []string{dump + "\nINVALID SQL;", strings.Replace(dump, "VERSION '1.1'", "VERSION 'missing'", 1), strings.ReplaceAll(dump, "uuid_generate_v4", "missing_uuid_v4"), strings.Replace(dump, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1)} {
		if bad == dump {
			t.Fatal("malformed-restore fixture failed to alter dump")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore changed provider/data")
		}
		post(c, r)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("canceled restore", err)
	}
	other, target, record := pgcryptoControlDestination(t, p, "uuid_ossp_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post(target, record)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO uuid_ossp_saved(id) VALUES(3)", 1))
	var later string
	if err := c.QueryRowContext(p.Ctx, "SELECT u FROM uuid_ossp_saved WHERE id=3").Scan(&later); err != nil {
		t.Fatal(err)
	}
	uuidOsspDecode(t, later)
	if later == saved[0] || later == saved[1] {
		t.Fatal("postrestore default reused UUID")
	}
}

func TestReferenceUUIDOsspControls(t *testing.T) { uuidOsspCase(t, false, uuidOsspControls) }
