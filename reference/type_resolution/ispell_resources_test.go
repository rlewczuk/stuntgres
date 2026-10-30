package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ispellDestination(t *testing.T, p *fixture.Pair, configure func(*stuntgres.Driver) error) (*sql.DB, *sql.DB, fixture.Backend) {
	t.Helper()
	d := stuntgres.NewDriver()
	if configure != nil {
		if err := configure(d); err != nil {
			t.Fatal(err)
		}
	}
	connector, err := d.OpenConnector("ispell_destination/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	t.Cleanup(func() { raw.Close() })
	wrapped, recorder := fixture.RecordedNativePool(t, connector)
	return wrapped, raw, fixture.Backend{Name: "destination", DB: wrapped, Recorder: recorder}
}

func TestReferenceIspellResources(t *testing.T) {
	databases := []string{"ispell_resource_db"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_resources", "legacy", "")
		ispellToken(t, p, b, c, "ispell_resources", "rebookings", "{booking,book}")
		ispellCreate(t, p, b, c, "ispell_empty", "empty", "")
		ispellToken(t, p, b, c, "ispell_empty", "book", nil)
		ispellCreate(t, p, b, c, "ispell_empty_stop", "legacy", ",StopWords=tr_ispell_empty")
		ispellToken(t, p, b, c, "ispell_empty_stop", "book", "{book}")
		regtsExec(t, p, b, c, "CREATE DATABASE ispell_resource_db TEMPLATE template1", 0)
		pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "ispell_resource_db", "")
		selected := fixture.Backend{Name: b.Name, DB: pool, Recorder: recorder, PG: b.PG}
		ispellCreate(t, p, selected, pool, "ispell_logical", "legacy", "")
		ispellToken(t, p, selected, pool, "ispell_logical", "skies", "{sky}")
		if b.PG {
			return
		}
		// Public SQL witnesses distinguish copied bytes, independent Driver data,
		// exact-DSN reuse, and absent resources; no private store inspection.
		d := stuntgres.NewDriver()
		word := []byte("original\n")
		if err := d.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: "tr_ispell_local.dict", Data: word}); err != nil {
			t.Fatal(err)
		}
		if err := d.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: "tr_ispell_local.affix"}); err != nil {
			t.Fatal(err)
		}
		copy(word, "mutated!")
		connector, err := d.OpenConnector("ispell_copy")
		if err != nil {
			t.Fatal(err)
		}
		first, record := fixture.RecordedNativePool(t, connector)
		fb := fixture.Backend{DB: first, Recorder: record}
		ispellCreate(t, p, fb, first, "ispell_copy", "local", "")
		ispellToken(t, p, fb, first, "ispell_copy", "original", "{original}")
		ispellToken(t, p, fb, first, "ispell_copy", "mutated!", nil)
		secondConnector, err := d.OpenConnector("ispell_copy")
		if err != nil {
			t.Fatal(err)
		}
		second, secondRecord := fixture.RecordedNativePool(t, secondConnector)
		ispellToken(t, p, fixture.Backend{DB: second, Recorder: secondRecord}, second, "ispell_copy", "original", "{original}")
		for _, value := range []string{"different", "original"} {
			t.Run(value, func(t *testing.T) {
				pool, _, db := ispellDestination(t, p, func(d *stuntgres.Driver) error {
					for _, file := range []stuntgres.TextSearchFile{{Name: "tr_ispell_local.dict", Data: []byte(value + "\n")}, {Name: "tr_ispell_local.affix"}} {
						if err := d.RegisterTextSearchFile(file); err != nil {
							return err
						}
					}
					return nil
				})
				ispellCreate(t, p, db, pool, "ispell_copy", "local", "")
				ispellToken(t, p, db, pool, "ispell_copy", value, "{"+value+"}")
			})
		}
		missing, _, mb := ispellDestination(t, p, nil)
		diagnostic := dictionaryDiagnostic("F0000", `could not open dictionary file "tr_ispell_legacy.dict": No such file or directory`)
		fixture.RunStep(t, p.Ctx, missing, mb.Recorder, fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY ispell_missing (TEMPLATE=ispell,DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy)", diagnostic))
	})
}

func TestReferenceIspellMetadata(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,tmplnamespace::bigint AS namespace,tmplinit::oid::bigint AS init,tmpllexize::oid::bigint AS lexize FROM pg_ts_template WHERE tmplname='ispell' AND tmplnamespace='pg_catalog'::regnamespace", []string{"id", "namespace", "init", "lexize"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3733), int64(11), int64(3731), int64(3732)}})
		regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,proname,pronamespace::bigint AS namespace,prorettype::bigint AS result,proargtypes::text AS args,prosrc,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid IN (3731,3732) ORDER BY oid", []string{"id", "proname", "namespace", "result", "args", "prosrc", "proisstrict", "provolatile", "proparallel"}, []string{"INT8", "NAME", "INT8", "INT8", "TEXT", "TEXT", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(3731), "dispell_init", int64(11), int64(2281), "2281", "dispell_init", true, "i", "s"}, {int64(3732), "dispell_lexize", int64(11), int64(2281), "2281 2281 2281 2281", "dispell_lexize", true, "i", "s"}})
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='ispell' AND dictnamespace='pg_catalog'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		ispellCreate(t, p, b, c, "ispell_metadata", "legacy", ",StopWords=tr_ispell_book")
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		regtsSelect(t, p, b, c, "SELECT d.dictname,d.dicttemplate::bigint AS template,d.dictinitoption,r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='ispell_metadata'", []string{"dictname", "template", "dictinitoption", "rolname"}, []string{"NAME", "INT8", "TEXT", "NAME"}, [][]any{{"ispell_metadata", int64(3733), "dictfile = 'tr_ispell_legacy', afffile = 'tr_ispell_legacy', stopwords = 'tr_ispell_book'", owner}})
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid='ispell_metadata'::regdictionary::oid AND refclassid=2615 AND refobjid='public'::regnamespace::oid AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		// PostgreSQL omits pg_depend edges to pinned builtin objects. The real
		// dictionary->template relationship is dicttemplate, not a fabricated
		// physical dependency row; retain both its exact identity and helpers.
		regtsSelect(t, p, b, c, "SELECT t.oid::bigint AS template,t.tmplnamespace::bigint AS namespace,t.tmplinit::oid::bigint AS init,t.tmpllexize::oid::bigint AS lexize FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate WHERE d.dictname='ispell_metadata' AND d.dictnamespace='public'::regnamespace", []string{"template", "namespace", "init", "lexize"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3733), int64(11), int64(3731), int64(3732)}})
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid='ispell_metadata'::regdictionary::oid AND refclassid=3764 AND refobjid=3733", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid='ispell_metadata'::regdictionary::oid AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		ispellToken(t, p, b, c, "simple", "BOOK", "{book}")
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		ispellToken(t, p, b, c, "unaccent", "Hôtel", "{Hotel}")
	})
}
