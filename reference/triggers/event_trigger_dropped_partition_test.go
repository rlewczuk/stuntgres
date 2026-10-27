package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 dependency.c assigns ORIGINAL only to SQL roots; INTERNAL and
// PARTITION reaches do not imply NORMAL. The newer child constraint is visited
// before the root index (descending objectId); its INTERNAL backing index does
// not reverse to an owner already on the stack. Confirm these bags on 18.6.
// objectaddress.c:getRelationTypeDescription calls both i and I "index".
func TestReferenceEventTriggerDroppedObjectsPartition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tree := range []bool{false, true} {
			t.Run(fmt.Sprintf("constraint/prepared=%v/tree=%v", prepared, tree), func(t *testing.T) {
				droppedScenario(t, func(d droppedCase) {
					d.setup()
					d.exec("CREATE TABLE public.et_do_part(i integer PRIMARY KEY) PARTITION BY RANGE(i)")
					tables := []string{"et_do_part"}
					if tree {
						d.exec("CREATE TABLE public.et_do_part_child PARTITION OF public.et_do_part DEFAULT")
						tables = append(tables, "et_do_part_child")
						d.run(fixture.Exec("INSERT INTO public.et_do_part VALUES(42)", 1))
					}
					var removed, survivors []droppedRecord
					for i, table := range tables {
						removed = append(removed, droppedPartitionPrimaryKey(d, table, i == 0)...)
						// Row/array and explicit NOT NULL identities must survive too.
						survivors = append(survivors, droppedTable("public", table, false)...)
						var notNull string
						if err := d.c.QueryRowContext(d.p.Ctx, "SELECT conname FROM pg_constraint WHERE conrelid='public."+table+"'::regclass AND contype='n'").Scan(&notNull); err != nil {
							d.t.Fatal(err)
						}
						survivors = append(survivors, droppedPayload("public", table, notNull, "table constraint", "", false, false))
					}
					removed = d.capture(removed)
					survivors = d.capture(survivors)
					liveData := func() {
						droppedPartitionAddresses(d, survivors)
						var rows [][]any
						if tree {
							rows = [][]any{{int64(42)}}
						}
						d.run(fixture.Select("SELECT i FROM public.et_do_part ORDER BY i", []string{"i"}, []string{"INT4"}, rows))
						for _, table := range tables {
							d.run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='public."+table+"'::regclass AND attname='i' AND NOT attisdropped", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
						}
					}
					live := func() {
						liveData()
						droppedPartitionAddresses(d, removed)
					}
					metadata := fixture.Select("SELECT * FROM pg_event_trigger_dropped_objects() LIMIT 0", droppedColumns, droppedTypes, nil)
					metadata.Prepared = prepared
					d.run(metadata)
					d.absenceCallback()
					d.install("'ALTER TABLE'")
					q := "ALTER TABLE public.et_do_part DROP CONSTRAINT et_do_part_pkey"

					// A successful retirement remains transactionally reversible,
					// including exact original payload/index OIDs and both reads.
					d.exec("BEGIN", "SAVEPOINT et_do_part_before")
					droppedPartitionExecute(d, q, prepared, live, fixture.Exec(q, 0))
					d.bag(1, removed)
					d.bag(2, removed)
					d.phases(len(removed))
					liveData()
					d.exec("ROLLBACK TO SAVEPOINT et_do_part_before")
					live()
					d.bag(1, nil)
					d.bag(2, nil)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
					d.exec("ROLLBACK")

					// A callback failure after reading the helper must roll back the
					// projected constraints, removed indexes and audit writes alike.
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
					d.phases(len(removed))
					liveData()
					for _, r := range removed {
						d.run(eventTriggerCount("SELECT count(*) AS n FROM "+r.lookup, 0))
					}
					if tree {
						// Duplicate values now succeed, but the inherited NOT NULL
						// payload and existing routed data remain intact.
						d.run(fixture.Exec("INSERT INTO public.et_do_part VALUES(42)", 1))
						d.run(fixture.Select("SELECT i FROM public.et_do_part_child ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}, {int64(42)}}))
					}
				})
			})
		}
		for _, roots := range []int{1, 2} {
			t.Run(fmt.Sprintf("index/prepared=%v/roots=%d", prepared, roots), func(t *testing.T) {
				droppedScenario(t, func(d droppedCase) {
					d.setup()
					var removed, survivors []droppedRecord
					var targets []string
					for i := 1; i <= roots; i++ {
						parent := fmt.Sprintf("et_do_idx_parent%d", i)
						child := fmt.Sprintf("et_do_idx_child%d", i)
						index := fmt.Sprintf("et_do_idx_root%d", i)
						d.exec("CREATE TABLE public."+parent+"(i integer) PARTITION BY RANGE(i)",
							"CREATE TABLE public."+child+" PARTITION OF public."+parent+" DEFAULT",
							"CREATE UNIQUE INDEX "+index+" ON public."+parent+"(i)")
						d.run(fixture.Exec("INSERT INTO public."+parent+" VALUES(42)", 1))
						targets = append(targets, "public."+index)
						removed = append(removed, droppedPartitionIndex(d, parent, true), droppedPartitionIndex(d, child, false))
						survivors = append(survivors, droppedTable("public", parent, false)...)
						survivors = append(survivors, droppedTable("public", child, false)...)
					}
					removed = d.capture(removed)
					survivors = d.capture(survivors)
					liveData := func() {
						droppedPartitionAddresses(d, survivors)
						for i := 1; i <= roots; i++ {
							d.run(fixture.Select(fmt.Sprintf("SELECT i FROM public.et_do_idx_parent%d", i), []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}}))
							d.run(fixture.Select(fmt.Sprintf("SELECT i FROM public.et_do_idx_child%d", i), []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}}))
						}
					}
					live := func() { liveData(); droppedPartitionAddresses(d, removed) }
					d.absenceCallback()
					d.install("'DROP INDEX'")
					q := "DROP INDEX " + strings.Join(targets, ",")
					droppedPartitionExecute(d, q, prepared, live, fixture.Exec(q, 0))
					d.bag(1, removed)
					d.bag(2, removed)
					d.phases(len(removed))
					liveData()
					for _, r := range removed {
						d.run(eventTriggerCount("SELECT count(*) AS n FROM "+r.lookup, 0))
					}
				})
			})
		}
		t.Run(fmt.Sprintf("index_local_constraint/prepared=%v", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TABLE public.et_do_local_parent(i integer) PARTITION BY RANGE(i)",
					"CREATE TABLE public.et_do_local_child PARTITION OF public.et_do_local_parent(CONSTRAINT et_do_local_unique UNIQUE(i)) DEFAULT",
					"CREATE UNIQUE INDEX et_do_local_root ON ONLY public.et_do_local_parent(i)",
					"ALTER INDEX public.et_do_local_root ATTACH PARTITION public.et_do_local_unique")
				d.run(fixture.Exec("INSERT INTO public.et_do_local_parent VALUES(42)", 1))
				// The attached local constraint is reached by reverse INTERNAL
				// ownership from its index, unlike a constraint's own PK tree.
				removed := d.capture([]droppedRecord{
					droppedPartitionIndex(d, "et_do_local_parent", true),
					droppedPartitionIndex(d, "et_do_local_child", false),
					droppedPayload("public", "et_do_local_child", "et_do_local_unique", "table constraint", "", false, true),
				})
				survivors := d.capture(append(droppedTable("public", "et_do_local_parent", false), droppedTable("public", "et_do_local_child", false)...))
				liveData := func() {
					droppedPartitionAddresses(d, survivors)
					d.run(fixture.Select("SELECT i FROM public.et_do_local_parent", []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}}))
				}
				live := func() { liveData(); droppedPartitionAddresses(d, removed) }
				d.absenceCallback()
				d.install("'DROP INDEX'")
				q := "DROP INDEX public.et_do_local_root CASCADE"
				droppedPartitionExecute(d, q, prepared, live, fixture.Exec(q, 0))
				d.bag(1, removed)
				d.bag(2, removed)
				d.phases(3)
				liveData()
				for _, r := range removed {
					d.run(eventTriggerCount("SELECT count(*) AS n FROM "+r.lookup, 0))
				}
				d.run(fixture.Exec("INSERT INTO public.et_do_local_parent VALUES(42)", 1))
				d.run(fixture.Select("SELECT i FROM public.et_do_local_child ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}, {int64(42)}}))
			})
		})
	}
}

// Names are generated by CREATE TABLE/INDEX. Capture only the public pre-DROP
// identity relationship; no helper/native output supplies a kind or flag.
func droppedPartitionPrimaryKey(d droppedCase, table string, root bool) []droppedRecord {
	d.t.Helper()
	var name string
	q := "SELECT conname FROM pg_constraint WHERE conrelid='public." + table + "'::regclass AND contype='p'"
	if err := d.c.QueryRowContext(d.p.Ctx, q).Scan(&name); err != nil {
		d.t.Fatal(err)
	}
	constraint := droppedPayload("public", table, name, "table constraint", "", root, false)
	return []droppedRecord{constraint, droppedPartitionIndex(d, table, false)}
}

func droppedPartitionIndex(d droppedCase, table string, original bool) droppedRecord {
	d.t.Helper()
	var name string
	q := "SELECT c.relname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='public." + table + "'::regclass"
	rows, err := d.c.QueryContext(d.p.Ctx, q)
	if err != nil {
		d.t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		d.t.Fatal("missing public pre-drop index", table)
	}
	if err := rows.Scan(&name); err != nil {
		d.t.Fatal(err)
	}
	if rows.Next() {
		d.t.Fatal("ambiguous public pre-drop index", table)
	}
	if err := rows.Err(); err != nil {
		d.t.Fatal(err)
	}
	return droppedRecord{class: 1259, original: original, kind: "index", schema: "public", name: name, identity: droppedQualified("public", name), names: []string{"public", name}, lookup: droppedRelationLookup("public", name)}
}

func droppedPartitionAddresses(d droppedCase, rs []droppedRecord) {
	d.t.Helper()
	for _, r := range rs {
		d.run(eventTriggerCount("SELECT count(*) AS n FROM "+r.lookup+" AND oid="+fmt.Sprint(r.oid), 1))
	}
}

func droppedPartitionExecute(d droppedCase, q string, prepared bool, live func(), step fixture.Step) {
	d.t.Helper()
	if !prepared {
		live()
		d.run(step)
		return
	}
	stmt, err := d.c.PrepareContext(d.p.Ctx, q)
	if err != nil {
		d.t.Fatal("prepare", err)
	}
	defer stmt.Close()
	live()
	d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 0))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
	fixture.RunStep(d.t, d.p.Ctx, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: q}, d.b.Recorder, step)
}
