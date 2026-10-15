package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func objectAddressReplayChecks() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT identity,description,names,args FROM addr_saved", []string{"identity", "description", "names", "args"}, []string{"TEXT", "TEXT", "_TEXT", "_TEXT"}, [][]any{{"public.addr_recipe.b", "column b of table addr_recipe", "{public,addr_recipe,b}", "{}"}}),
		fixture.Select("SELECT identity,names,args,description,subid FROM addr_recipe_view", []string{"identity", "names", "args", "description", "subid"}, []string{"TEXT", "_TEXT", "_TEXT", "TEXT", "INT4"}, [][]any{{"public.addr_recipe.b", "{public,addr_recipe,b}", "{}", "column b of table addr_recipe", int64(2)}}),
		fixture.Select("SELECT identity FROM addr_recipe_named", []string{"identity"}, []string{"TEXT"}, [][]any{{"public.addr_recipe.b"}}),
		fixture.Select("SELECT objid='addr_recipe'::regclass::oid AS same,objsubid FROM pg_get_object_address('table column','{public,addr_recipe,b}','{}')", []string{"same", "objsubid"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(2)}}),
	}
}
func TestReferenceObjectAddressLifecycle(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE addr_recipe(a INT DEFAULT 1,b TEXT)",
			"CREATE TABLE addr_saved(identity TEXT,description TEXT,names TEXT[],args TEXT[])",
			"CREATE TABLE addr_raw_oid(value BIGINT)",
			"CREATE VIEW addr_recipe_view AS SELECT (pg_identify_object(1259,'addr_recipe'::regclass,2)).identity AS identity,(pg_identify_object_as_address(1259,'addr_recipe'::regclass,2)).object_names AS names,(pg_identify_object_as_address(1259,'addr_recipe'::regclass,2)).object_args AS args,pg_describe_object(1259,'addr_recipe'::regclass,2) AS description,(pg_get_object_address('table column',ARRAY['public','addr_recipe','b'],ARRAY[]::text[])).objsubid AS subid",
			"CREATE VIEW addr_recipe_named AS SELECT (pg_identify_object(objsubid=>2,objid=>'addr_recipe'::regclass,classid=>1259)).identity AS identity",
		} {
			objectAddressRun(t, p, b, c, fixture.Exec(q, 0))
		}
		objectAddressRun(t, p, b, c, fixture.Exec("INSERT INTO addr_saved SELECT identity,description,names,args FROM addr_recipe_view", 1))
		var rawID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'addr_recipe'::regclass::oid").Scan(&rawID); err != nil {
			t.Fatal(err)
		}
		objectAddressRun(t, p, b, c, fixture.Exec("INSERT INTO addr_raw_oid VALUES($1)", 1, rawID))
		checks := objectAddressReplayChecks()
		// Untyped numeric result data is not a symbolic regclass dependency.
		checks = append(checks, fixture.Select("SELECT value FROM addr_raw_oid", []string{"value"}, []string{"INT8"}, [][]any{{rawID}}))
		for _, s := range checks {
			objectAddressRun(t, p, b, c, s)
		}
		if b.PG {
			return
		} // SQL contract above is shared; controls have no PG API.
		old, err := c.QueryContext(p.Ctx, checks[0].Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		q := "SELECT pg_describe_object(1259,'addr_recipe'::regclass,2) AS description"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.ScalarRows(t, false, old, checks[0].Columns, checks[0].Types, checks[0].Rows, false)
		fixture.ScalarAssertDescription(t, metadata, checks[0].Columns, checks[0].Types)
		for _, s := range checks {
			objectAddressRun(t, p, b, c, s)
		}
		// Prepared native provenance cannot cross a catalog replacement. Keep
		// that ordinary error boundary, independently of retained cursor values.
		staleRows, staleErr := stmt.QueryContext(p.Ctx)
		if staleRows != nil {
			staleRows.Close()
			t.Fatal("retired prepared provenance published rows")
		}
		fixture.TransactionOrdinary(t, staleErr, "stuntgres: catalog replaced before publication")
		fixture.ScalarAssertDescription(t, metadata, checks[0].Columns, checks[0].Types)
		objectAddressRun(t, p, b, c, fixture.Select(q, []string{"description"}, []string{"TEXT"}, [][]any{{"column b of table addr_recipe"}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if redump.String() != dump.String() {
			t.Fatal("stateless inquiry changed deterministic recipe dump")
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector(t.Name() + "/address-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		if _, err := destination.ExecContext(p.Ctx, "CREATE TABLE addr_destination_spacer(a INT)"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range checks {
			rows, err := destination.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarRows(t, false, rows, s.Columns, s.Types, s.Rows, s.Bag)
		}
		// Failure after an address-bearing staged prefix cannot replace authority.
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE addr_staged(a INT); CREATE VIEW addr_bad AS SELECT pg_get_object_address();"))
		want := fixture.ControlsReferenceDiagnostic("42883", "function pg_get_object_address() does not exist")
		want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		want.Position = "65"
		fixture.AssertDiagnostic(t, err, &want)
		ioFailure := errors.New("address restore reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: ioFailure}); !errors.Is(err, ioFailure) {
			t.Fatal("ordinary restore IO error", err)
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("ordinary Restore cancellation", err)
		}
		for _, s := range checks {
			objectAddressRun(t, p, b, c, s)
		}
		objectAddressRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='addr_staged' AND relnamespace='public'::regnamespace::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceObjectAddressFeatureSmoke(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		objectAddressRun(t, p, b, c, fixture.Exec(`CREATE TABLE "Address Smoke"(a INT,"b.c" TEXT)`, 0))
		a := objectAddressWitness(t, p, b, c, 1259, "table column", []string{"public", "Address Smoke", "b.c"}, nil, "public", `"Address Smoke"`, `public."Address Smoke"."b.c"`, `column b.c of table "Address Smoke"`, `SELECT '"Address Smoke"'::regclass::oid`)
		objectAddressRun(t, p, b, c, fixture.Exec(`ALTER TABLE "Address Smoke" RENAME TO "Address Renamed"`, 0))
		renamed := objectAddressWitness(t, p, b, c, 1259, "table column", []string{"public", "Address Renamed", "b.c"}, nil, "public", `"Address Renamed"`, `public."Address Renamed"."b.c"`, `column b.c of table "Address Renamed"`, `SELECT '"Address Renamed"'::regclass::oid`)
		if renamed != a {
			t.Fatal("rename changed address", a, renamed)
		}
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE TABLE addr_smoke_demand(id INT,d TEXT DEFAULT pg_describe_object(1247,23,0),CHECK (d='type integer' AND (pg_identify_object(1247,23,0)).identity='integer' AND (pg_identify_object_as_address(1247,23,0)).object_names=ARRAY['integer']::text[] AND (pg_get_object_address('type',ARRAY['integer'],ARRAY[]::text[])).objsubid=0))", 0))
		consumeStored := func() {
			insert := fixture.Exec("INSERT INTO addr_smoke_demand(id) VALUES(1)", 1)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, insert)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(insert))
			objectAddressRun(t, p, b, c, fixture.Select("SELECT d FROM addr_smoke_demand", []string{"d"}, []string{"TEXT"}, [][]any{{"type integer"}, {"type integer"}}))
			objectAddressRun(t, p, b, c, fixture.Exec("DELETE FROM addr_smoke_demand", 2))
		}
		consumeStored()
		objectAddressRun(t, p, b, c, fixture.Exec(`CREATE VIEW addr_smoke_view AS SELECT (pg_identify_object(1259,'"Address Renamed"'::regclass,2)).identity AS identity,(pg_identify_object_as_address(1259,'"Address Renamed"'::regclass,2)).object_names AS names`, 0))
		check := fixture.Select("SELECT identity,names FROM addr_smoke_view", []string{"identity", "names"}, []string{"TEXT", "_TEXT"}, [][]any{{`public."Address Renamed"."b.c"`, `{public,"Address Renamed",b.c}`}})
		objectAddressRun(t, p, b, c, check)
		if !b.PG {
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			objectAddressRun(t, p, b, c, check)
			consumeStored()
			a = objectAddressResolve(t, p, c, "table column", []string{"public", "Address Renamed", "b.c"}, nil)
		}
		objectAddressRun(t, p, b, c, fixture.Exec(`DROP VIEW addr_smoke_view`, 0))
		objectAddressRun(t, p, b, c, fixture.Exec(`DROP TABLE "Address Renamed"`, 0))
		objectAddressNumeric(t, p, b, c, objectAddressValue{1259, a.object, 0}, "relation", nil, nil, nil, nil, nil, nil)
		objectAddressDiagnostics(t, p, b, c, []objectAddressDiagnosticCase{{"missing_after_drop", `SELECT pg_get_object_address('table',ARRAY['public','Address Renamed'],ARRAY[]::text[])`, fixture.Diagnostic{Code: "42P01", Message: `relation "public.Address Renamed" does not exist`}, true}})
	})
}
