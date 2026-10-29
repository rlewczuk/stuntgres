package reference_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func crosstabBadRecipe(t *testing.T, dump, field string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	start, end, data := fixture.CatalogExtensionOwnerDirective(t, dump, "tablefunc", true)
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	switch field {
	case "owner":
		record["owner"] = "ct_missing_owner"
	case "version":
		record["version"] = "missing"
	case "signature", "type_role":
		kind, name, signature := "procedure", "crosstab", "pg_catalog.text"
		if field == "type_role" {
			kind, name, signature = "type", "tablefunc_crosstab_3", ""
		}
		var selected map[string]any
		for _, raw := range record["members"].([]any) {
			m := raw.(map[string]any)
			if m["kind"] == kind && m["schema"] == "ct_addon" && m["name"] == name && m["signature"] == signature && m["version"] == "1.0" && m["declaration"] != nil {
				if selected != nil {
					t.Fatal("duplicate symbolic recipe member", field)
				}
				selected = m
			}
		}
		if selected == nil {
			t.Fatal("missing genuine recipe member", field)
		}
		selected["signature"] = "ct_impossible_identity"
	default:
		t.Fatal("unknown corruption", field)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return dump[:start] + marker + base64.StdEncoding.EncodeToString(data) + dump[end:]
}

func TestReferenceTablefuncCrosstabControls(t *testing.T) {
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE ct_owner", 0))
		run(fixture.Exec("CREATE SCHEMA ct_addon", 0))
		fixture.CrosstabSeedInSchema(t, p, b, c, "ct_addon")
		run(fixture.Exec("CREATE VIEW ct_generic_view AS SELECT * FROM ct_addon.crosstab('SELECT k,category,v FROM public.ct_source ORDER BY seq') AS t(k text,c1 text,c2 text,c3 text)", 0))
		run(fixture.Exec("CREATE VIEW ct_wrapper_view AS SELECT * FROM ct_addon.crosstab3('SELECT k,category,v FROM public.ct_source ORDER BY seq')", 0))
		run(fixture.Exec("CREATE TABLE ct_composites(id integer,v ct_addon.tablefunc_crosstab_3)", 0))
		run(fixture.Exec("INSERT INTO ct_composites VALUES(1,'(row,one,,three)'),(2,NULL)", 2))
		run(fixture.Exec("ALTER TABLE ct_composites OWNER TO ct_owner", 0))
		q := "SELECT * FROM ct_addon.crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"
		h := fixture.CrosstabHeld(t, p, c, q)
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, target, rec, fixture.CrosstabWant("SELECT * FROM ct_generic_view ORDER BY k"))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT * FROM ct_wrapper_view ORDER BY row_name", []string{"row_name", "category_1", "category_2", "category_3"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT id,v::text AS v FROM ct_composites ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "(row,one,,three)"}, {int64(2), nil}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname='ct_composites'", []string{"rolname"}, []string{"NAME"}, [][]any{{"ct_owner"}}))
			// Ownership remains valid without current namespace lookup rights.
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT has_schema_privilege('ct_owner','ct_addon','USAGE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{false}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.CrosstabWant(q, fixture.CrosstabSource))
			// Both generic overloads and every genuine named wrapper survive replay.
			fixture.RunStep(t, p.Ctx, target, rec, fixture.CrosstabWant("SELECT * FROM ct_addon.crosstab($1::text,-1) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k", fixture.CrosstabSource))
			for _, n := range []string{"2", "3", "4"} {
				fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT count(*) AS n FROM ct_addon.crosstab"+n+"($1::text)", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}, fixture.CrosstabSource))
			}
		}
		post(c, b.Recorder)
		crosstabInventory(t, p, b, c, "ct_addon")
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.CrosstabWant(q, fixture.CrosstabSource))
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT * FROM ct_generic_view ORDER BY k")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='tablefunc'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, want := range [][]any{{"g1", "A", "C", nil}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}} {
			if !old.Next() {
				t.Fatal("old result lost", old.Err())
			}
			var k, a, v, z any
			if err := old.Scan(&k, &a, &v, &z); err != nil {
				t.Fatal(err)
			}
			for i, got := range []any{k, a, v, z} {
				if fixture.Normalize(got) != want[i] {
					t.Fatal("old cell", i, got, want[i])
				}
			}
		}
		if old.Next() || old.Err() != nil || len(meta) != 4 || meta[0].Name() != "k" || meta[0].DatabaseTypeName() != "TEXT" {
			t.Fatal("old result metadata/tail", old.Err())
		}
		old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(c, b.Recorder)
		crosstabInventory(t, p, b, c, "ct_addon")
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.CrosstabWant(q, fixture.CrosstabSource))
		run(fixture.Exec("CREATE TABLE ct_sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO ct_sentinel VALUES(71)", 1))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		start, end, payload := fixture.CatalogExtensionOwnerDirective(t, dump, "tablefunc", true)
		badDumps := []string{dump + "\nINVALID SQL;", dump[:start] + "-- stuntgres-extension-owners-v999 " + base64.StdEncoding.EncodeToString(payload) + dump[end:]}
		for _, field := range []string{"owner", "version", "signature", "type_role"} {
			badDumps = append(badDumps, crosstabBadRecipe(t, dump, field))
		}
		for _, bad := range badDumps {
			if bad == dump {
				t.Fatal("unchanged corruption")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("corrupt restore accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("failed restore published")
			}
			post(c, b.Recorder)
			run(fixture.Select("SELECT id FROM ct_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(71)}}))
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore", err)
		}
		other, target, rec := pgcryptoControlDestination(t, p, "ct_destination")
		fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("CREATE ROLE ct_owner", 0))
		for _, sql := range []string{"CREATE TABLE ct_oid_shift(a integer)", "CREATE TYPE ct_type_shift AS (v text)", "CREATE SCHEMA ct_shift"} {
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec(sql, 0))
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(target, rec)
		otherBackend := b
		otherBackend.Recorder = rec
		crosstabInventory(t, p, otherBackend, target, "ct_addon")
		post(c, b.Recorder)
	})
}
