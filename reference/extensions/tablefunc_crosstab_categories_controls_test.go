package reference_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabCategoriesControls(t *testing.T) {
	crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE ct_owner", "CREATE ROLE ct_reader", "CREATE SCHEMA ct_addon", "CREATE EXTENSION tablefunc SCHEMA ct_addon"} {
			run(fixture.Exec(q, 0))
		}
		crosstabCategoriesExtras(t, p, b, c)
		for _, q := range []string{
			"ALTER TABLE ct_extras OWNER TO ct_owner",
			"ALTER FUNCTION ct_addon.crosstab(text,text) OWNER TO ct_owner",
			"REVOKE EXECUTE ON FUNCTION ct_addon.crosstab(text,text) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ct_addon.crosstab(text,text) TO ct_reader",
			"CREATE VIEW ct_category_control AS SELECT * FROM ct_addon.crosstab("+fixture.CrosstabQuote(crosstabCategoriesExtrasSource)+","+fixture.CrosstabQuote(crosstabCategoriesOrder("b", "a", "c"))+") AS t(k text,metadata text,b integer,a integer,c integer)",
		} {
			run(fixture.Exec(q, 0))
		}
		q := crosstabCategoriesSQL("ct_addon.crosstab", "k text,metadata text,b integer,a integer,c integer") + " ORDER BY k"
		h := fixture.CrosstabHeld(t, p, c, q)
		view := "SELECT * FROM ct_category_control ORDER BY k"
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, target, rec, s) }
			s := crosstabCategoriesExtrasWant(q, 20)
			r(s)
			r(fixture.Prepared(s))
			s.Query, s.Args = view, nil
			r(s)
			r(fixture.Prepared(s))
			r(fixture.Select("SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='ct_addon.crosstab(text,text)'::regprocedure", []string{"rolname"}, []string{"NAME"}, [][]any{{"ct_owner"}}))
			r(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname='ct_extras'", []string{"rolname"}, []string{"NAME"}, [][]any{{"ct_owner"}}))
			r(fixture.Select("SELECT has_schema_privilege('ct_owner','ct_addon','USAGE') AS usage,has_function_privilege('ct_reader','ct_addon.crosstab(text,text)','EXECUTE') AS execute", []string{"usage", "execute"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
			other := b
			other.Recorder = rec
			crosstabCategoriesDeclaration(t, p, other, target, "ct_addon")
			crosstabInventory(t, p, other, target, "ct_addon")
		}
		post(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, crosstabCategoriesExtrasWant(q, 20))
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, view)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		columns, err := old.Columns()
		if err != nil {
			t.Fatal(err)
		}
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var sourceProc int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ct_addon.crosstab(text,text)'::regprocedure::oid::bigint").Scan(&sourceProc); err != nil || sourceProc <= 0 {
			t.Fatal("source hash identity", sourceProc, err)
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		crosstabCategoriesRecipe(t, dump)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='tablefunc'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if !old.Next() {
			t.Fatal("detached category row missing", old.Err())
		}
		cells := make([]any, 5)
		if err := old.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4]); err != nil {
			t.Fatal(err)
		}
		for i := range cells {
			cells[i] = fixture.Normalize(cells[i])
		}
		if !reflect.DeepEqual(cells, []any{"g1", "first", int64(20), int64(10), nil}) || old.Next() || old.Err() != nil {
			t.Fatal("detached category result", cells, old.Err())
		}
		if !reflect.DeepEqual(columns, []string{"k", "metadata", "b", "a", "c"}) || len(meta) != 5 || meta[2].DatabaseTypeName() != "INT4" {
			t.Fatal("detached category metadata", columns, meta)
		}
		old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, crosstabCategoriesExtrasWant(q, 20))
		run(fixture.Exec("CREATE TABLE ct_category_sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO ct_category_sentinel VALUES(71)", 1))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		bad := []string{dump + "\nINVALID SQL;"}
		for _, field := range []string{"signature", "declaration", "owner"} {
			bad = append(bad, crosstabCategoriesBadRecipe(t, dump, field))
		}
		for _, broken := range bad {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(broken)); err == nil {
				t.Fatal("corrupt category recipe accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("failed category restore changed committed state")
			}
			post(c, b.Recorder)
			run(fixture.Select("SELECT id FROM ct_category_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(71)}}))
		}
		other, target, rec := pgcryptoControlDestination(t, p, "ct_categories_destination")
		for _, q := range []string{"CREATE ROLE ct_owner", "CREATE ROLE ct_reader", "CREATE TABLE ct_category_shift(a integer)", "CREATE TYPE ct_category_shift_type AS (v text)", "CREATE SCHEMA ct_category_shift_schema"} {
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec(q, 0))
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		var targetProc int64
		if err := target.QueryRowContext(p.Ctx, "SELECT 'ct_addon.crosstab(text,text)'::regprocedure::oid::bigint").Scan(&targetProc); err != nil || targetProc <= 0 || targetProc == sourceProc {
			t.Fatal("independent shifted hash identity", sourceProc, targetProc, err)
		}
		post(target, rec)
		crosstabCategoriesRecipe(t, fixture.ExtensionDump(t, p.Ctx, other))
		post(c, b.Recorder)
	})
}

// Public native Dump metadata exposes release roles, not executable C symbols.
// Replacing the appended role with an incumbent role is a source-role mismatch;
// direct C-source forgery belongs in the private descriptor identity units.
func crosstabCategoriesRecipe(t *testing.T, dump string) map[string]any {
	t.Helper()
	_, _, payload := fixture.CatalogExtensionOwnerDirective(t, dump, "tablefunc", true)
	var record map[string]any
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatal(err)
	}
	members := record["members"].([]any)
	if len(members) != 17 {
		t.Fatal("category recipe inventory", len(members))
	}
	var selected map[string]any
	for _, raw := range members {
		m := raw.(map[string]any)
		if m["kind"] == "procedure" && m["schema"] == "ct_addon" && m["name"] == "crosstab" && m["signature"] == "pg_catalog.text, pg_catalog.text" {
			if selected != nil || m["declaration"] != float64(16) || m["version"] != "1.0" || m["owner"] != "ct_owner" {
				t.Fatal("appended hash declaration", m)
			}
			selected = m
		}
	}
	if selected == nil {
		t.Fatal("missing appended hash declaration")
	}
	return selected
}

func crosstabCategoriesBadRecipe(t *testing.T, dump, field string) string {
	t.Helper()
	start, end, payload := fixture.CatalogExtensionOwnerDirective(t, dump, "tablefunc", true)
	var record map[string]any
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatal(err)
	}
	var selected map[string]any
	for _, raw := range record["members"].([]any) {
		m := raw.(map[string]any)
		if m["declaration"] == float64(16) {
			if selected != nil {
				t.Fatal("duplicate declaration16")
			}
			selected = m
		}
	}
	if selected == nil {
		t.Fatal("missing declaration16")
	}
	switch field {
	case "signature":
		selected[field] = "pg_catalog.text, integer"
	case "declaration":
		selected[field] = float64(0)
	case "owner":
		selected[field] = "ct_missing_owner"
	default:
		t.Fatal("unknown category corruption", field)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return dump[:start] + "-- stuntgres-extension-owners-v3 " + base64.StdEncoding.EncodeToString(payload) + dump[end:]
}
