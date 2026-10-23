package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func uuidNameControls(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	uuidNameConsumers(t, p, c, r)
	uuidNameInventory(t, p, c, r)
	f := uuidNameFunctions[1]
	q := "SELECT uuid_ossp_part.uuid_generate_v5($1::uuid,$2::text) AS u"
	h := uuidNameHeld(t, p, c, q)
	defer h.Statement.Close()
	for _, v := range []struct{ ns, name, want any }{{uuidNameDNS, "www.example.com", f.example}, {uuidNameDNS, "www.widgets.com", f.widgets}, {nil, "www.example.com", nil}} {
		fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, v.want, v.ns, v.name))
	}
	rows, err := h.Statement.QueryContext(p.Ctx, "bad", "www.example.com")
	if rows != nil {
		rows.Close()
		t.Error("malformed bind published rows")
	}
	d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type uuid: \"bad\"")
	d.Context = "unnamed portal parameter $1 = '...'"
	fixture.AssertDiagnostic(t, err, &d)
	fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, f.example, uuidNameDNS, "www.example.com"))
	if pg {
		return
	}
	old, err := c.QueryContext(p.Ctx, "SELECT g AS u FROM uuid_generate_v5_names ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	cols, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	dump := extensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for _, want := range []any{f.example, f.widgets, nil, f.example} {
		if !old.Next() {
			t.Fatal("retained row missing", old.Err())
		}
		var got any
		if err := old.Scan(&got); err != nil || got != want {
			t.Fatal("retained UUID", got, want, err)
		}
	}
	if old.Next() || old.Err() != nil || len(cols) != 1 || cols[0].Name() != "u" || cols[0].DatabaseTypeName() != "UUID" {
		t.Fatal("retained tail/metadata", old.Err())
	}
	old.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post := func(target *sql.Conn, rec *fixture.Recorder) {
		uuidNameInventory(t, p, target, rec)
		for _, member := range uuidNameFunctions {
			uuidNameConsumerRead(t, p, target, rec, member.name, member.example, member.widgets)
		}
	}
	post(c, r)
	fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, f.example, uuidNameDNS, "www.example.com"))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE uuid_name_sentinel(id integer)", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO uuid_name_sentinel VALUES(7)", 1))
	before := extensionDump(t, p.Ctx, p.Native)
	for _, bad := range []string{
		dump + "\nINVALID SQL;",
		strings.ReplaceAll(dump, "uuid_generate_v5", "missing_name_provider"),
		strings.Replace(dump, "VERSION '1.1'", "VERSION 'missing'", 1),
		strings.Replace(dump, "stuntgres-extension-owners-v2", "stuntgres-extension-owners-v999", 1),
		uuidNameInvalidSignature(t, dump),
	} {
		if bad == dump {
			t.Fatal("unchanged bad fixture")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("bad restore accepted")
		}
		if extensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore published state")
		}
		post(c, r)
		fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, f.example, uuidNameDNS, "www.example.com"))
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || extensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("canceled restore", err)
	}
	fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, f.example, uuidNameDNS, "www.example.com"))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id FROM uuid_name_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	other, target, rec := pgcryptoControlDestination(t, p, "uuid_name_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post(target, rec)
	for _, member := range uuidNameFunctions {
		fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("INSERT INTO "+member.name+"_names(id,name) VALUES(5,'www.example.com')", 1))
		fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT d,g FROM "+member.name+"_names WHERE id=5", []string{"d", "g"}, []string{"UUID", "UUID"}, [][]any{{member.widgets, member.example}}))
	}
}

func TestReferenceUUIDOsspNameControls(t *testing.T) { uuidNameCase(t, uuidNameControls) }

func TestReferenceUUIDOsspNameFeatureSmoke(t *testing.T) { uuidNameCase(t, uuidNameControls) }
