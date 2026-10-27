package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDroppedObjectsProtocol(t *testing.T) {
	droppedScenario(t, func(d droppedCase) {
		d.setup()
		for _, q := range []string{"SELECT pg_event_trigger_dropped_objects()", "SELECT * FROM pg_event_trigger_dropped_objects()", "SELECT public.et_do_read(1)"} {
			context := ""
			if q == "SELECT public.et_do_read(1)" {
				context = "SQL function \"et_do_read\" statement 1"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, droppedProtocolDiagnostic(context))
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				d.run(s)
				d.bag(1, nil)
			}
		}
	})
	for _, phase := range []string{"ddl_command_start", "ddl_command_end"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", phase, prepared), func(t *testing.T) {
				droppedScenario(t, func(d droppedCase) {
					d.setup()
					d.exec("CREATE TABLE public.et_do_forbidden(i integer)", "CREATE FUNCTION public.et_do_forbidden_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN PERFORM pg_event_trigger_dropped_objects(); END$$")
					eventTriggerCreate(t, d.p, d.b, d.c, "et_do_start", phase, "'DROP TABLE'", "public.et_do_forbidden_fn")
					want := droppedProtocolDiagnostic("SQL statement \"SELECT pg_event_trigger_dropped_objects()\"\nPL/pgSQL function et_do_forbidden_fn() line 1 at PERFORM")
					s := fixture.ErrorDiagnostic("DROP TABLE public.et_do_forbidden", want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					d.run(s)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_do_forbidden'", 1))
					d.bag(1, nil)
				})
			})
		}
	}
	// The ordinary SQL helper executes successfully only in the lexical drop
	// phase; two invocations persist the same full bag without consuming it.
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("ordinary_helper/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TABLE public.et_do_allowed(i integer)")
				rs := d.capture(droppedTable("public", "et_do_allowed", false))
				d.install("'DROP TABLE'")
				d.drop("DROP TABLE public.et_do_allowed", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
			})
		})
	}
}

func TestReferenceEventTriggerDroppedObjectsNested(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("lexical/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec(
					"CREATE TABLE public.et_do_outer(i integer)",
					"CREATE TABLE public.et_do_inner(i integer)",
					"CREATE TABLE public.et_do_failed(i integer)",
					"CREATE TABLE public.et_do_guard(phase text,state text,message text)",
					"CREATE FUNCTION public.et_do_nested_fail() RETURNS integer LANGUAGE SQL AS $$DROP TABLE public.et_do_failed; SELECT 1$$",
					"CREATE FUNCTION public.et_do_nested_good() RETURNS integer LANGUAGE SQL AS $$DROP TABLE public.et_do_inner; SELECT 1$$",
					"CREATE FUNCTION public.et_do_guard_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM pg_event_trigger_dropped_objects(); RAISE EXCEPTION 'nested phase inherited sql_drop'; EXCEPTION WHEN SQLSTATE '39P03' THEN INSERT INTO public.et_do_guard VALUES(TG_EVENT,SQLSTATE,SQLERRM); END; END$$",
					"CREATE OR REPLACE FUNCTION public.et_do_drop_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN IF EXISTS(SELECT 1 FROM pg_event_trigger_dropped_objects() WHERE object_name='et_do_outer' AND object_type='table') THEN SELECT public.et_do_read(1) INTO n; BEGIN SELECT public.et_do_nested_fail() INTO n; EXCEPTION WHEN raise_exception THEN n:=0; END; SELECT public.et_do_nested_good() INTO n; SELECT public.et_do_read(2) INTO n; ELSIF EXISTS(SELECT 1 FROM pg_event_trigger_dropped_objects() WHERE object_name='et_do_failed' AND object_type='table') THEN SELECT public.et_do_read(9) INTO n; RAISE EXCEPTION 'caught nested drop'; ELSE SELECT public.et_do_read(3) INTO n; SELECT public.et_do_read(4) INTO n; END IF; END$$",
				)
				outer := d.capture(droppedTable("public", "et_do_outer", false))
				inner := d.capture(droppedTable("public", "et_do_inner", false))
				eventTriggerCreate(t, d.p, d.b, d.c, "et_do_start", "ddl_command_start", "'DROP TABLE'", "public.et_do_guard_fn")
				eventTriggerCreate(t, d.p, d.b, d.c, "et_do_drop", "sql_drop", "'DROP TABLE'", "public.et_do_drop_fn")
				eventTriggerCreate(t, d.p, d.b, d.c, "et_do_end", "ddl_command_end", "'DROP TABLE'", "public.et_do_guard_fn")
				d.drop("DROP TABLE public.et_do_outer", prepared)
				d.bag(1, outer)
				d.bag(2, outer)
				d.bag(3, inner)
				d.bag(4, inner)
				d.bag(9, nil)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 12))
				d.run(fixture.Select("SELECT phase,state,message FROM public.et_do_guard ORDER BY phase", []string{"phase", "state", "message"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{
					{"ddl_command_end", "39P03", droppedContextMessage}, {"ddl_command_end", "39P03", droppedContextMessage},
					{"ddl_command_start", "39P03", droppedContextMessage}, {"ddl_command_start", "39P03", droppedContextMessage},
				}))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_do_failed'", 1))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname IN ('et_do_outer','et_do_inner')", 0))
			})
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("same_name_generation/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TABLE public.et_do_swap(a integer,b integer)")
				rs := d.capture([]droppedRecord{droppedColumn("public", "et_do_swap", "b", 2)})
				d.install("'ALTER TABLE'")
				d.drop("ALTER TABLE public.et_do_swap DROP COLUMN b, ADD COLUMN b integer", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
				d.run(fixture.Select("SELECT attnum FROM pg_attribute WHERE attrelid='public.et_do_swap'::regclass AND attname='b' AND NOT attisdropped", []string{"attnum"}, []string{"INT2"}, [][]any{{int64(3)}}))
			})
		})
	}
}

func TestReferenceEventTriggerDroppedObjectsAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TABLE public.et_do_bad(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)", "CREATE TABLE public.et_do_good(i integer)")
				d.run(fixture.Exec("INSERT INTO public.et_do_bad(f1) VALUES(7)", 1))
				bad := d.capture(droppedTable("public", "et_do_bad", true))
				good := d.capture(droppedTable("public", "et_do_good", false))
				d.exec("CREATE OR REPLACE FUNCTION public.et_do_drop_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_do_read(1) INTO n; IF EXISTS(SELECT 1 FROM pg_event_trigger_dropped_objects() WHERE object_type='table' AND object_name='et_do_bad') THEN RAISE EXCEPTION 'dropped objects rejected'; END IF; SELECT public.et_do_read(2) INTO n; INSERT INTO public.et_do_phase VALUES('drop',3,0); END$$")
				d.install("'DROP TABLE'")
				for _, explicit := range []bool{false, true} {
					if explicit {
						d.exec("BEGIN", "SAVEPOINT et_do_recover")
					}
					s := fixture.ErrorDiagnostic("DROP TABLE public.et_do_bad", eventTriggerFailure("et_do_drop_fn", "dropped objects rejected"))
					s.Prepared = prepared
					s.ErrorStage = "execute"
					d.run(s)
					if explicit {
						d.exec("ROLLBACK TO SAVEPOINT et_do_recover")
					}
					d.bag(1, nil)
					d.bag(2, nil)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
					d.run(fixture.Select("SELECT f1,f2 FROM public.et_do_bad", []string{"f1", "f2"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(42)}}))
					// Every deleted generation, including implicit payloads, survived.
					for _, r := range bad {
						d.run(eventTriggerCount("SELECT count(*) AS n FROM "+r.lookup+" AND oid="+fmt.Sprint(r.oid), 1))
					}
					if explicit {
						d.exec("ROLLBACK")
					}
				}
				d.drop("DROP TABLE public.et_do_good", prepared)
				d.bag(1, good)
				d.bag(2, good)
				d.phases(3)
			})
		})
	}
}
