package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDroppedObjectsAlter(t *testing.T) {
	cases := []struct {
		name      string
		setup     []string
		query     string
		records   func() []droppedRecord
		surviving string
	}{
		{"column", []string{"CREATE TABLE public.et_do_alter(a integer,b integer)"}, "ALTER TABLE public.et_do_alter DROP COLUMN b", func() []droppedRecord { return []droppedRecord{droppedColumn("public", "et_do_alter", "b", 2)} }, "SELECT count(*) AS n FROM pg_attribute WHERE attrelid='public.et_do_alter'::regclass AND attname='a' AND NOT attisdropped"},
		{"default", []string{"CREATE TABLE public.et_do_alter(a integer DEFAULT 42)"}, "ALTER TABLE public.et_do_alter ALTER COLUMN a DROP DEFAULT", func() []droppedRecord {
			return []droppedRecord{droppedPayload("public", "et_do_alter", "", "default value", "a", true, false)}
		}, "SELECT count(*) AS n FROM pg_class WHERE relname='et_do_alter'"},
		{"check", []string{"CREATE TABLE public.et_do_alter(a integer,b integer CONSTRAINT et_do_check CHECK(b>0))"}, "ALTER TABLE public.et_do_alter DROP COLUMN b", func() []droppedRecord {
			return []droppedRecord{droppedColumn("public", "et_do_alter", "b", 2), droppedPayload("public", "et_do_alter", "et_do_check", "table constraint", "", false, true)}
		}, "SELECT count(*) AS n FROM pg_class WHERE relname='et_do_alter'"},
		{"constraint", []string{"CREATE TABLE public.et_do_alter(a integer CONSTRAINT et_do_pk PRIMARY KEY)"}, "ALTER TABLE public.et_do_alter DROP CONSTRAINT et_do_pk", func() []droppedRecord {
			r := droppedRecord{class: 1259, kind: "index", schema: "public", name: "et_do_pk", identity: "public.et_do_pk", names: []string{"public", "et_do_pk"}, lookup: droppedRelationLookup("public", "et_do_pk")}
			return []droppedRecord{droppedPayload("public", "et_do_alter", "et_do_pk", "table constraint", "", true, false), r}
		}, "SELECT count(*) AS n FROM pg_constraint WHERE conrelid='public.et_do_alter'::regclass AND conname='et_do_alter_a_not_null'"},
		{"index", []string{"CREATE TABLE public.et_do_alter(a integer)", "CREATE INDEX et_do_idx ON public.et_do_alter(a)"}, "DROP INDEX public.et_do_idx", func() []droppedRecord {
			return []droppedRecord{{class: 1259, original: true, kind: "index", schema: "public", name: "et_do_idx", identity: "public.et_do_idx", names: []string{"public", "et_do_idx"}, lookup: droppedRelationLookup("public", "et_do_idx")}}
		}, "SELECT count(*) AS n FROM pg_class WHERE relname='et_do_alter'"},
		// The default has both AUTO(column) and NORMAL(sequence) paths. The
		// sequence's AUTO ownership must not erase that NORMAL provenance.
		{"serial", []string{"CREATE TABLE public.et_do_alter(a integer,s serial)"}, "ALTER TABLE public.et_do_alter DROP COLUMN s", func() []droppedRecord {
			return []droppedRecord{
				droppedColumn("public", "et_do_alter", "s", 2),
				{class: 1259, kind: "sequence", schema: "public", name: "et_do_alter_s_seq", identity: "public.et_do_alter_s_seq", names: []string{"public", "et_do_alter_s_seq"}, lookup: droppedRelationLookup("public", "et_do_alter_s_seq")},
				droppedPayload("public", "et_do_alter", "", "default value", "s", false, true),
				droppedPayload("public", "et_do_alter", "et_do_alter_s_not_null", "table constraint", "", false, false),
			}
		}, "SELECT count(*) AS n FROM pg_class WHERE relname='et_do_alter'"},
		{"generated", []string{"CREATE TABLE public.et_do_alter(a integer,b integer GENERATED ALWAYS AS (a+1) STORED)"}, "ALTER TABLE public.et_do_alter ALTER COLUMN b SET EXPRESSION AS (a+2)", func() []droppedRecord {
			return []droppedRecord{droppedPayload("public", "et_do_alter", "", "default value", "b", true, false)}
		}, "SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='public.et_do_alter'::regclass AND adnum=2"},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				droppedScenario(t, func(d droppedCase) {
					d.setup()
					d.exec(tc.setup...)
					rs := d.capture(tc.records())
					d.absenceCallback()
					d.install("'ALTER TABLE','DROP INDEX'")
					d.drop(tc.query, prepared)
					d.bag(1, rs)
					d.bag(2, rs)
					d.phases(len(rs))
					d.run(eventTriggerCount(tc.surviving, 1))
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("internal_and_noop/prepared=", prepared), func(t *testing.T) {
			droppedScenario(t, func(d droppedCase) {
				d.setup()
				d.exec("CREATE TABLE public.et_do_alter(a integer DEFAULT 1,id integer GENERATED ALWAYS AS IDENTITY)")
				d.install("'ALTER TABLE'")
				for _, q := range []string{
					"ALTER TABLE public.et_do_alter ALTER COLUMN a SET DEFAULT 2",
					"ALTER TABLE public.et_do_alter ALTER COLUMN id DROP IDENTITY",
					"ALTER TABLE public.et_do_alter ALTER COLUMN id DROP DEFAULT",
				} {
					d.drop(q, prepared)
					d.bag(1, nil)
					d.bag(2, nil)
					d.run(fixture.Select("SELECT phase,n,remaining FROM public.et_do_phase", []string{"phase", "n", "remaining"}, []string{"TEXT", "INT8", "INT8"}, [][]any{{"end", int64(0), int64(0)}}))
					d.clear()
				}
			})
		})
	}
}
