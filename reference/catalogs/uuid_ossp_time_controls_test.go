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

func uuidOsspTimeControls(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	saved := uuidOsspTimeConsumers(t, p, c, r, pg)
	held := map[string]fixture.HeldStatement{}
	seen := map[string]bool{}
	for _, name := range uuidOsspTimeNames {
		q := "SELECT uuid_ossp_part." + name + "() AS u"
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		held[name] = fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
		uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, held[name], r, pg, q, 1, 1, name == "uuid_generate_v1mc"))
		// PostgreSQL has no Reset/Restore API; still prove retained demand here.
		uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, held[name], r, pg, q, 1, 1, name == "uuid_generate_v1mc"))
	}
	if pg {
		return
	}
	var oldRows []*sql.Rows
	var oldTypes [][]*sql.ColumnType
	for _, name := range uuidOsspTimeNames {
		rows, err := c.QueryContext(p.Ctx, "SELECT u FROM "+name+"_saved ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		oldRows = append(oldRows, rows)
		oldTypes = append(oldTypes, cols)
	}
	dump := extensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for i, rows := range oldRows {
		for _, v := range saved[uuidOsspTimeNames[i]] {
			var got string
			if !rows.Next() {
				t.Fatal("lost retained row", rows.Err())
			}
			if err := rows.Scan(&got); err != nil || got != v {
				t.Fatal("retained UUID", got, v, err)
			}
		}
		if rows.Next() || rows.Err() != nil || len(oldTypes[i]) != 1 || oldTypes[i][0].DatabaseTypeName() != "UUID" {
			t.Fatal("retained metadata/tail", rows.Err())
		}
		rows.Close()
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post := func(conn *sql.Conn, record *fixture.Recorder) {
		for _, name := range uuidOsspTimeNames {
			if got := uuidOsspTimeSaved(t, p, conn, name); !reflect.DeepEqual(got, saved[name]) {
				t.Fatal("restore changed saved UUID", got, saved[name])
			}
			q := "SELECT uuid_ossp_part." + name + "() AS u"
			uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, conn, record, false, q, 1, 1, name == "uuid_generate_v1mc"))
			for _, suffix := range []string{"_view", "_nested"} {
				uuidOsspTimeRead(t, p, conn, record, false, "SELECT u FROM "+name+suffix+" ORDER BY id", 2, 1, name == "uuid_generate_v1mc", true)
			}
		}
	}
	post(c, r)
	for _, name := range uuidOsspTimeNames {
		uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, held[name], r, false, held[name].SQL, 1, 1, name == "uuid_generate_v1mc"))
	}
	before := extensionDump(t, p.Ctx, p.Native)
	for _, bad := range []string{
		dump + "\nINVALID SQL;",
		strings.Replace(dump, "VERSION '1.1'", "VERSION 'missing'", 1),
		strings.ReplaceAll(dump, "uuid_generate_v1mc", "missing_uuid_v1mc"),
		strings.Replace(dump, "stuntgres-extension-owners-v2", "stuntgres-extension-owners-v999", 1),
	} {
		if bad == dump {
			t.Fatal("malformed fixture did not alter dump")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if extensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore changed data/provider")
		}
		post(c, r)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || extensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("canceled restore", err)
	}
	post(c, r)
	other, target, record := pgcryptoControlDestination(t, p, "uuid_time_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post(target, record)
	for _, conn := range []*sql.Conn{c, target} {
		for _, name := range uuidOsspTimeNames {
			if _, err := conn.ExecContext(p.Ctx, "INSERT INTO "+name+"_saved(id) VALUES(3)"); err != nil {
				t.Fatal(err)
			}
			got := uuidOsspTimeSaved(t, p, conn, name)
			if !reflect.DeepEqual(got[:2], saved[name]) || len(got) != 3 {
				t.Fatal("postrestore default", got)
			}
			uuidOsspTimeUnique(t, seen, got[2:])
		}
	}
}

func TestReferenceUUIDOsspTimeControls(t *testing.T) { uuidOsspCase(t, false, uuidOsspTimeControls) }
