package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDroppedObjectsFields(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				// LIMIT 0 is the prospective effect-free public Describe witness.
				// The root's pinned oracle checkpoint must confirm no evaluation.
				for _, prep := range []bool{false, true} {
					s := fixture.Select("SELECT * FROM pg_event_trigger_dropped_objects() LIMIT 0", droppedColumns, droppedTypes, nil)
					s.Prepared = prep
					d.run(s)
				}
				var routineOID any = int64(3566)
				if d.b.PG {
					routineOID = "3566"
				}
				d.run(fixture.Select("SELECT oid,proretset,prorows,proargnames FROM pg_proc WHERE oid=3566", []string{"oid", "proretset", "prorows", "proargnames"}, []string{"OID", "BOOL", "FLOAT4", "_TEXT"}, [][]any{{routineOID, true, float64(100), droppedArray(droppedColumns)}}))
				d.exec("CREATE TABLE public.et_do_t(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)")
				rs := d.capture(droppedTable("public", "et_do_t", true))
				d.absenceCallback()
				d.install("'DROP TABLE'")
				d.drop("DROP TABLE public.et_do_t", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
				d.phases(7)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname IN ('et_do_t','et_do_t_pkey')", 0))
			})
		})
		for _, temporary := range []bool{false, true} {
			t.Run(fmt.Sprintf("owned_payloads/prepared=%v/temp=%v", prepared, temporary), func(t *testing.T) {
				droppedTriggerPolicy(t, prepared, temporary)
			})
		}
	}
}

func TestReferenceEventTriggerDroppedObjectsCascade(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("schema/prepared=", prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "et_do_s")
			droppedScenario(t, func(d droppedCase) {
				var notices []fixture.Diagnostic
				_, d.c, d.b.Recorder = fixture.NoticeReferencePool(t, d.p, d.b.PG, &notices)
				d.setup()
				d.exec("CREATE SCHEMA et_do_s", "CREATE TABLE et_do_s.t(i integer)")
				rs := d.capture(droppedSchema("et_do_s", "t"))
				d.absenceCallback()
				d.install("'DROP SCHEMA'")
				d.drop("DROP SCHEMA et_do_s CASCADE", prepared)
				fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to table et_do_s.t"}})
				d.bag(1, rs)
				d.bag(2, rs)
				d.phases(4)
			})
		})
		t.Run(fmt.Sprint("cross_owner/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				var notices []fixture.Diagnostic
				_, d.c, d.b.Recorder = fixture.NoticeReferencePool(t, d.p, d.b.PG, &notices)
				d.setup()
				d.exec("CREATE TABLE public.et_do_parent(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)", "CREATE TABLE public.et_do_child(i integer CONSTRAINT et_do_fk REFERENCES public.et_do_parent(f1))")
				d.run(fixture.Exec("INSERT INTO public.et_do_parent VALUES(1,42)", 1))
				d.run(fixture.Exec("INSERT INTO public.et_do_child VALUES(1)", 1))
				rs := append(droppedTable("public", "et_do_parent", true), droppedPayload("public", "et_do_child", "et_do_fk", "table constraint", "", false, true))
				rs = d.capture(rs)
				rs = append(rs, d.foreignKeyTriggers()...)
				d.absenceCallback()
				d.install("'DROP TABLE'")
				d.drop("DROP TABLE public.et_do_parent CASCADE", prepared)
				fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to constraint et_do_fk on table et_do_child"}})
				// Include all four logical RI triggers, even though their names
				// contain allocator-derived suffixes captured before deletion.
				d.bag(1, rs)
				d.bag(2, rs)
				d.phases(12)
				d.run(fixture.Select("SELECT i FROM public.et_do_child", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_constraint WHERE conname='et_do_fk'", 0))
			})
		})
		t.Run(fmt.Sprint("multiple_roots/prepared=", prepared), func(t *testing.T) {
			droppedMultiRoot(t, prepared)
		})
	}
}

func TestReferenceEventTriggerDroppedObjectsIdentity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("quoted/prepared=", prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "Odd S")
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec(`CREATE SCHEMA "Odd S"`, `CREATE TABLE "Odd S"."T q"("c!" integer,i integer)`)
				d.install("'ALTER TABLE','DROP TABLE'")
				rs := d.capture([]droppedRecord{droppedColumn("Odd S", "T q", "c!", 1)})
				d.drop(`ALTER TABLE "Odd S"."T q" DROP COLUMN "c!"`, prepared)
				d.bag(1, rs)
				d.bag(2, rs)
				d.clear()
				rs = d.capture(droppedTable("Odd S", "T q", false))
				d.drop(`DROP TABLE "Odd S"."T q"`, prepared)
				d.bag(1, rs)
				d.bag(2, rs)
			})
		})
		t.Run(fmt.Sprint("arguments/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE FUNCTION public.et_do_over(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", "CREATE FUNCTION public.et_do_over(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$", "CREATE DOMAIN public.et_do_domain AS integer CONSTRAINT et_do_ck CHECK(VALUE>0)")
				d.install("'DROP FUNCTION','ALTER DOMAIN'")
				rs := d.capture([]droppedRecord{{class: 1255, original: true, kind: "function", schema: "public", identity: "public.et_do_over(integer)", names: []string{"public", "et_do_over"}, args: []string{"integer"}, lookup: "pg_proc WHERE proname='et_do_over' AND proargtypes='23'::oidvector"}})
				d.drop("DROP FUNCTION public.et_do_over(integer)", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
				d.run(fixture.Select("SELECT public.et_do_over(9::bigint) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}}))
				d.clear()
				rs = d.capture([]droppedRecord{{class: 2606, original: true, kind: "domain constraint", schema: "public", identity: "et_do_ck on public.et_do_domain", names: []string{"public.et_do_domain"}, args: []string{"et_do_ck"}, lookup: "pg_constraint WHERE conname='et_do_ck' AND contypid=(SELECT oid FROM pg_type WHERE typname='et_do_domain')"}})
				d.drop("ALTER DOMAIN public.et_do_domain DROP CONSTRAINT et_do_ck", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
			})
		})
		t.Run(fmt.Sprint("temporary/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TEMP TABLE et_do_temp(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)")
				rs := d.capture(droppedTable("pg_temp", "et_do_temp", true))
				d.absenceCallback()
				d.install("'DROP TABLE'")
				d.drop("DROP TABLE pg_temp.et_do_temp", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
				d.phases(7)
			})
		})
		t.Run(fmt.Sprint("temporary_routine/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TEMP TABLE et_do_temp_anchor(i integer)",
					"CREATE FUNCTION pg_temp.et_do_temp_fn(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
				var namespace string
				if err := d.c.QueryRowContext(d.p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_proc p ON p.pronamespace=n.oid WHERE p.proname='et_do_temp_fn' AND p.proargtypes='23'::oidvector").Scan(&namespace); err != nil {
					t.Fatal(err)
				}
				rs := d.capture([]droppedRecord{{
					class: 1255, original: true, temporary: true, kind: "function",
					schema: "pg_temp", identity: namespace + ".et_do_temp_fn(integer)",
					names: []string{"pg_temp", "et_do_temp_fn"}, args: []string{"integer"},
					lookup: "pg_proc WHERE proname='et_do_temp_fn' AND proargtypes='23'::oidvector",
				}})
				d.absenceCallback()
				d.install("'DROP FUNCTION'")
				d.drop("DROP FUNCTION pg_temp.et_do_temp_fn(integer)", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
				d.phases(1)
			})
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("temporary_view/prepared=%v", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TEMP VIEW et_do_temp_view AS SELECT 1::integer AS i")
				rs := droppedTable("pg_temp", "et_do_temp_view", false)
				rs[0].kind = "view"
				// Unlike triggers and policies, pg_rewrite uses generic
				// catalog metadata: no namespace/name or temporary override.
				// Its canonical identity still names the temporary owner.
				rs = append(rs, droppedRecord{
					class:    2618,
					kind:     "rule",
					identity: `"_RETURN" on pg_temp.et_do_temp_view`,
					names:    []string{"pg_temp", "et_do_temp_view", "_RETURN"},
					lookup:   "pg_rewrite WHERE rulename='_RETURN' AND ev_class=(SELECT oid FROM " + droppedRelationLookup("pg_temp", "et_do_temp_view") + ")",
				})
				rs = d.capture(rs)
				d.absenceCallback()
				d.install("'DROP VIEW'")
				d.drop("DROP VIEW pg_temp.et_do_temp_view", prepared)
				d.bag(1, rs)
				d.bag(2, rs)
				d.phases(4)
			})
		})
	}
}

func TestReferenceEventTriggerDroppedObjectsSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "et_do_smoke")
	droppedScenario(t, func(d droppedCase) {
		d.setup()
		d.exec("CREATE SCHEMA et_do_smoke", "CREATE TABLE et_do_smoke.t(i integer)")
		rs := d.capture(droppedSchema("et_do_smoke", "t"))
		d.absenceCallback()
		d.install("'DROP SCHEMA'")
		d.drop("DROP SCHEMA et_do_smoke CASCADE", true)
		d.bag(1, rs)
		d.bag(2, rs)
		d.phases(4)
		s := fixture.ErrorDiagnostic("SELECT * FROM pg_event_trigger_dropped_objects()", droppedProtocolDiagnostic(""))
		s.Prepared = true
		s.SelectRows = true
		s.ErrorStage = "execute"
		d.run(s)
	})
}

// Both explicit roots must retain ORIGINAL even when the second view is also
// reached through a NORMAL dependency and reverse INTERNAL rule ownership.
func droppedMultiRoot(t *testing.T, prepared bool) {
	droppedScenario(t, func(d droppedCase) {
		d.setup()
		d.exec("CREATE VIEW public.et_do_v1 AS SELECT 1::integer AS i",
			"CREATE VIEW public.et_do_v2 AS SELECT i FROM public.et_do_v1")
		var rs []droppedRecord
		for _, name := range []string{"et_do_v1", "et_do_v2"} {
			view := droppedTable("public", name, false)
			view[0].kind = "view"
			view[0].normal = name == "et_do_v2"
			view = append(view, droppedRecord{class: 2618, normal: name == "et_do_v2", kind: "rule", identity: `"_RETURN" on public.` + name, names: []string{"public", name, "_RETURN"}, lookup: "pg_rewrite WHERE rulename='_RETURN' AND ev_class=(SELECT oid FROM " + droppedRelationLookup("public", name) + ")"})
			rs = append(rs, view...)
		}
		rs = d.capture(rs)
		d.absenceCallback()
		d.install("'DROP VIEW'")
		d.drop("DROP VIEW public.et_do_v1,public.et_do_v2", prepared)
		d.bag(1, rs)
		d.bag(2, rs)
		d.phases(8)
		d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname IN ('et_do_v1','et_do_v2')", 0))
	})
}

func droppedTriggerPolicy(t *testing.T, prepared, temporary bool) {
	droppedScenario(t, func(d droppedCase) {
		d.setup()
		schema, prefix := "public", ""
		if temporary {
			schema, prefix = "pg_temp", "TEMP "
		}
		d.exec("CREATE "+prefix+"TABLE et_do_payload(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)",
			"CREATE FUNCTION public.et_do_row_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER et_do_row BEFORE INSERT ON "+schema+".et_do_payload FOR EACH ROW EXECUTE FUNCTION public.et_do_row_fn()",
			"CREATE POLICY et_do_policy ON "+schema+".et_do_payload USING(f1>0)")
		rs := droppedTable(schema, "et_do_payload", true)
		rel := "(SELECT oid FROM " + droppedRelationLookup(schema, "et_do_payload") + ")"
		rs = append(rs,
			droppedRecord{class: 2620, temporary: temporary, kind: "trigger", schema: schema, identity: "et_do_row on " + schema + ".et_do_payload", names: []string{schema, "et_do_payload", "et_do_row"}, lookup: "pg_trigger WHERE tgrelid=" + rel + " AND tgname='et_do_row'"},
			droppedRecord{class: 3256, normal: true, temporary: temporary, kind: "policy", schema: schema, identity: "et_do_policy on " + schema + ".et_do_payload", names: []string{schema, "et_do_payload", "et_do_policy"}, lookup: "pg_policy WHERE polrelid=" + rel + " AND polname='et_do_policy'"})
		rs = d.capture(rs)
		d.absenceCallback()
		d.install("'DROP TABLE'")
		d.drop("DROP TABLE "+schema+".et_do_payload", prepared)
		d.bag(1, rs)
		d.bag(2, rs)
		d.phases(9)
	})
}
