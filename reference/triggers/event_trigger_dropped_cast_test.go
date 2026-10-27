package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 objectaddress.c renders casts as (source AS target), with one
// qualified source in address_names and one qualified target in address_args.
// format_type.c spells the builtin int4 endpoint "integer" even when qualified.
// Casts have neither a namespace nor a unique raw name (event_trigger.c).
// These independent expectations require the root's pinned-oracle checkpoint.
func TestReferenceEventTriggerDroppedObjectsCast(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TYPE public.et_do_cast_source AS ENUM ('red')",
					"CREATE FUNCTION public.et_do_cast_convert(public.et_do_cast_source) RETURNS integer LANGUAGE SQL AS $$SELECT 51$$",
					"CREATE CAST (public.et_do_cast_source AS integer) WITH FUNCTION public.et_do_cast_convert(public.et_do_cast_source)")
				removed := d.capture([]droppedRecord{{
					class: 2605, original: true, kind: "cast",
					identity: "(public.et_do_cast_source AS integer)",
					names:    []string{"public.et_do_cast_source"}, args: []string{"integer"},
					lookup: "pg_cast WHERE castsource='public.et_do_cast_source'::regtype AND casttarget='integer'::regtype",
				}})
				survivors := d.capture([]droppedRecord{
					{lookup: droppedTypeLookup("public", "et_do_cast_source"), identity: "public.et_do_cast_source"},
					{lookup: droppedTypeLookup("pg_catalog", "int4"), identity: "integer"},
					{lookup: "pg_proc WHERE proname='et_do_cast_convert' AND pronamespace='public'::regnamespace", identity: "public.et_do_cast_convert(public.et_do_cast_source)"},
				})
				liveData := func() {
					droppedPartitionAddresses(d, survivors)
					d.run(fixture.Select("SELECT public.et_do_cast_convert('red'::public.et_do_cast_source) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(51)}}))
				}
				live := func() {
					liveData()
					droppedPartitionAddresses(d, removed)
					// The exact cast OID must still enforce the conversion after rollback,
					// rather than merely leaving endpoint/function descriptors behind.
					d.run(fixture.Select("SELECT ('red'::public.et_do_cast_source)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(51)}}))
				}
				metadata := fixture.Select("SELECT * FROM pg_event_trigger_dropped_objects() LIMIT 0", droppedColumns, droppedTypes, nil)
				metadata.Prepared = prepared
				d.run(metadata)
				d.absenceCallback()
				d.install("'DROP CAST'")
				q := "DROP CAST (public.et_do_cast_source AS integer)"

				// Successful sql_drop/end/audit effects are reversible together with
				// the original cast OID and its actual conversion behavior.
				d.exec("BEGIN", "SAVEPOINT et_do_cast_before")
				droppedPartitionExecute(d, q, prepared, live, fixture.Exec(q, 0))
				d.bag(1, removed)
				d.bag(2, removed)
				d.phases(1)
				liveData()
				d.run(eventTriggerCount("SELECT count(*) AS n FROM "+removed[0].lookup, 0))
				d.exec("ROLLBACK TO SAVEPOINT et_do_cast_before")
				live()
				d.bag(1, nil)
				d.bag(2, nil)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
				d.exec("ROLLBACK")

				// A missing sql_drop callback cannot fake a successful rollback test:
				// execution must raise after reading, restoring the cast and both audits.
				d.exec("CREATE OR REPLACE FUNCTION public.et_do_drop_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_do_read(1) INTO n; RAISE EXCEPTION 'dropped objects rejected'; END$$")
				s := fixture.ErrorDiagnostic(q, eventTriggerFailure("et_do_drop_fn", "dropped objects rejected"))
				s.ErrorStage = "execute"
				droppedPartitionExecute(d, q, prepared, live, s)
				live()
				d.bag(1, nil)
				d.bag(2, nil)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))

				d.absenceCallback()
				droppedPartitionExecute(d, q, prepared, live, fixture.Exec(q, 0))
				d.bag(1, removed)
				d.bag(2, removed)
				d.phases(1)
				liveData()
				d.run(eventTriggerCount("SELECT count(*) AS n FROM "+removed[0].lookup, 0))
			})
		})
	}
}
