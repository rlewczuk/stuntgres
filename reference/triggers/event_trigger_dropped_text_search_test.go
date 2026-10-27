package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// The configuration's mapping records a NORMAL dependency on the dictionary.
// Released dependency.c and objectaddress.c determine these flags and identities;
// public pre-drop catalogs supply only the generated object OIDs.
func TestReferenceEventTriggerDroppedObjectsTextSearch(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				var notices []fixture.Diagnostic
				_, d.c, d.b.Recorder = fixture.NoticeReferencePool(t, d.p, d.b.PG, &notices)
				d.setup()
				d.exec(
					"CREATE TEXT SEARCH DICTIONARY public.et_do_ts_dict(TEMPLATE=pg_catalog.simple)",
					"CREATE TEXT SEARCH CONFIGURATION public.et_do_ts_cfg(PARSER=pg_catalog.default)",
					"ALTER TEXT SEARCH CONFIGURATION public.et_do_ts_cfg ADD MAPPING FOR asciiword WITH public.et_do_ts_dict",
				)
				rs := d.capture([]droppedRecord{
					{class: 3600, original: true, kind: "text search dictionary", schema: "public", name: "et_do_ts_dict", identity: "public.et_do_ts_dict", names: []string{"public", "et_do_ts_dict"}, lookup: "pg_ts_dict WHERE dictname='et_do_ts_dict' AND dictnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')"},
					{class: 3602, normal: true, kind: "text search configuration", schema: "public", name: "et_do_ts_cfg", identity: "public.et_do_ts_cfg", names: []string{"public", "et_do_ts_cfg"}, lookup: "pg_ts_config WHERE cfgname='et_do_ts_cfg' AND cfgnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')"},
				})
				live := func() {
					d.run(fixture.Select("SELECT oid::bigint FROM pg_ts_dict WHERE dictname='et_do_ts_dict' AND dictnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", []string{"oid"}, []string{"INT8"}, [][]any{{rs[0].oid}}))
					d.run(fixture.Select("SELECT oid::bigint,cfgparser::bigint FROM pg_ts_config WHERE cfgname='et_do_ts_cfg' AND cfgnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", []string{"oid", "cfgparser"}, []string{"INT8", "INT8"}, [][]any{{rs[1].oid, int64(3722)}}))
					d.run(fixture.Select("SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='public.et_do_ts_cfg'::regconfig", []string{"maptokentype", "mapseqno", "mapdict"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), rs[0].oid}}))
					d.run(fixture.Select("SELECT ts_lexize('public.et_do_ts_dict','WORD') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{word}"}}))
				}
				live()
				metadata := fixture.Select("SELECT * FROM pg_event_trigger_dropped_objects() LIMIT 0", droppedColumns, droppedTypes, nil)
				metadata.Prepared = prepared
				d.run(metadata)
				d.absenceCallback()
				d.install("'DROP TEXT SEARCH DICTIONARY'")
				// Setup and metadata must not emit notices either. In particular,
				// holding the utility must not emit its eventual cascade notice.
				fixture.RegtypeAssertNotices(t, notices, nil)
				q := "DROP TEXT SEARCH DICTIONARY public.et_do_ts_dict CASCADE"
				if prepared {
					stmt, err := d.c.PrepareContext(d.p.Ctx, q)
					if err != nil {
						d.t.Fatal("prepare", err)
					}
					defer stmt.Close()
					live()
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 0))
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
					fixture.RegtypeAssertNotices(t, notices, nil)
					fixture.RunStep(d.t, d.p.Ctx, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: q}, d.b.Recorder, fixture.Exec(q, 0))
				} else {
					d.drop(q, false)
				}
				// A single dependent is reported using its search-path-visible
				// description, unlike the helper's always-qualified identity.
				fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to text search configuration et_do_ts_cfg"}})
				d.bag(1, rs)
				d.bag(2, rs)
				d.phases(2)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='et_do_ts_dict' AND dictnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", 0))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_ts_config WHERE cfgname='et_do_ts_cfg' AND cfgnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", 0))
				d.run(eventTriggerCount(fmt.Sprintf("SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg=%d::oid", rs[1].oid), 0))
			})
		})
	}
}
