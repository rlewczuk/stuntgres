package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func uuidOsspGroupedRead(t *testing.T, p *fixture.Pair, e fixture.Executor, pg bool, query string) string {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != 2 {
		t.Fatal("grouped metadata", cols, err)
	}
	for i, want := range []struct{ name, typ string }{{"u", "UUID"}, {"n", "INT8"}} {
		if cols[i].Name() != want.name || cols[i].DatabaseTypeName() != want.typ {
			t.Fatalf("grouped column %d=%s/%s", i, cols[i].Name(), cols[i].DatabaseTypeName())
		}
		// These are declared nullable view outputs, not direct computed calls.
		if nullable, known := cols[i].Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("view nullability=%v,%v", nullable, known)
		}
	}
	wantScan := reflect.TypeFor[string]()
	if pg {
		wantScan = reflect.TypeFor[any]()
	}
	if cols[0].ScanType() != wantScan {
		t.Fatal("UUID scan type", cols[0].ScanType())
	}
	if _, known := cols[0].Length(); known {
		t.Fatal("UUID length known")
	}
	if _, _, known := cols[0].DecimalSize(); known {
		t.Fatal("UUID precision known")
	}
	if !rows.Next() {
		t.Fatal("missing grouped row", rows.Err())
	}
	var raw any
	var count int64
	if err := rows.Scan(&raw, &count); err != nil {
		t.Fatal(err)
	}
	var value string
	if pg {
		bytes, ok := raw.([]byte)
		if !ok {
			t.Fatalf("pq UUID raw %T", raw)
		}
		value = string(bytes)
	} else {
		var ok bool
		value, ok = raw.(string)
		if !ok {
			t.Fatalf("native UUID raw %T", raw)
		}
	}
	uuidOsspDecode(t, value)
	if count != 2 {
		t.Fatalf("aggregate count=%d want=2", count)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("grouped cursor tail", rows.Err())
	}
	return value
}

func TestReferenceUUIDOsspGroupedControls(t *testing.T) {
	uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\"", 0))
		run(fixture.Exec("CREATE TABLE uuid_ossp_group_input(id integer)", 0))
		run(fixture.Exec("INSERT INTO uuid_ossp_group_input VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE VIEW uuid_ossp_group_view AS SELECT uuid_generate_v4() AS u,count(*) AS n FROM uuid_ossp_group_input", 0))
		q := "SELECT u,n FROM uuid_ossp_group_view"
		read := func(e fixture.Executor, postgres bool) {
			// Each demand is checked independently, never against another draw.
			uuidOsspGroupedRead(t, p, e, postgres, q)
		}
		read(c, pg)
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldExecutor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		read(heldExecutor, pg)
		read(heldExecutor, pg)
		if pg {
			return
		}
		dump := extensionDump(t, p.Ctx, p.Native)
		if !strings.Contains(dump, "uuid_generate_v4") || !strings.Contains(dump, "uuid_ossp_group_view") {
			t.Fatal("lost grouped installed recipe", dump)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal("grouped restore", err)
		}
		read(c, false)
		read(heldExecutor, false)
		other, target, _ := pgcryptoControlDestination(t, p, "uuid_ossp_group_destination")
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal("independent grouped restore", err)
		}
		read(target, false)
		otherHeld, err := target.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer otherHeld.Close()
		read(fixture.HeldStatement{Executor: target, Statement: otherHeld, SQL: q}, false)
	})
}
