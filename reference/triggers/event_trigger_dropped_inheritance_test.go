package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 dependency.c merges incoming flags for both pending and completed
// deletion targets. ORIGINAL does not suppress a separate NORMAL inheritance
// edge, and neither flag propagates to the table's INTERNAL row/array types.
func TestReferenceEventTriggerDroppedObjectsInheritance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, childFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v/child_first=%v", prepared, childFirst), func(t *testing.T) {
				droppedScenario(t, func(d droppedCase) {
					d.setup()
					d.exec("CREATE TABLE public.et_do_inh_parent(i integer)",
						"CREATE TABLE public.et_do_inh_child() INHERITS (public.et_do_inh_parent)")
					// An empty child declaration must still expose its inherited
					// attribute. Do not substitute an unrelated, empty relation.
					d.run(fixture.Exec("INSERT INTO public.et_do_inh_child(i) VALUES(42)", 1))
					live := func() {
						d.run(fixture.Select("SELECT i FROM ONLY public.et_do_inh_child", []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}}))
						d.run(fixture.Select("SELECT attnum,attislocal,attinhcount FROM pg_attribute WHERE attrelid='public.et_do_inh_child'::regclass AND attname='i' AND NOT attisdropped", []string{"attnum", "attislocal", "attinhcount"}, []string{"INT2", "BOOL", "INT2"}, [][]any{{int64(1), false, int64(1)}}))
						d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_inherits WHERE inhrelid='public.et_do_inh_child'::regclass AND inhparent='public.et_do_inh_parent'::regclass", 1))
					}
					live()

					// Six complete logical records, not a subset or helper-derived
					// golden: two explicit table roots and their row/array types.
					// Only OIDs come from the public pre-deletion catalogs.
					parent := droppedTable("public", "et_do_inh_parent", false)
					child := droppedTable("public", "et_do_inh_child", false)
					child[0].normal = true // ORIGINAL=true and NORMAL=true independently.
					rs := d.capture(append(parent, child...))
					metadata := fixture.Select("SELECT * FROM pg_event_trigger_dropped_objects() LIMIT 0", droppedColumns, droppedTypes, nil)
					metadata.Prepared = prepared
					d.run(metadata)
					d.absenceCallback()
					d.install("'DROP TABLE'")

					q := "DROP TABLE public.et_do_inh_parent,public.et_do_inh_child"
					if childFirst {
						q = "DROP TABLE public.et_do_inh_child,public.et_do_inh_parent"
					}
					if prepared {
						stmt, err := d.c.PrepareContext(d.p.Ctx, q)
						if err != nil {
							d.t.Fatal("prepare", err)
						}
						defer stmt.Close()
						// Holding the utility must not retire either root, change
						// inherited attributes/data, or invoke either event phase.
						live()
						d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 0))
						d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
						fixture.RunStep(d.t, d.p.Ctx, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: q}, d.b.Recorder, fixture.Exec(q, 0))
					} else {
						d.drop(q, false)
					}
					// Both reads must contain the exact full bag. The callback
					// checks catalog absence during sql_drop; the end phase must
					// already observe the six rows captured by that earlier phase.
					d.bag(1, rs)
					d.bag(2, rs)
					d.phases(6)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname IN ('et_do_inh_parent','et_do_inh_child') AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", 0))
				})
			})
		}
	}
}
