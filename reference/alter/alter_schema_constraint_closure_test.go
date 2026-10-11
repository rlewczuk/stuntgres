package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSchemaPostgresClosure(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := b.Recorder
			referenceAlterSchemaSetup(t, p, c, r)
			for _, q := range []string{"ALTER TABLE s1.t ADD CONSTRAINT pk PRIMARY KEY(id)", "ALTER TABLE s1.t ADD CONSTRAINT ck CHECK(id>0)", "CREATE INDEX ix ON s1.t(note)", "CREATE TABLE s2.other(id INT CONSTRAINT ck CHECK(id>0))"} {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
			}
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 's1.t'::regclass::oid").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal(oid, err)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0)))
			var moved int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 's2.t'::regclass::oid").Scan(&moved); err != nil || moved != oid {
				t.Fatal(oid, moved, err)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='s2.t'::regclass::oid ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"ck"}, {"nn_id"}, {"pk"}}))
			fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
			if b.PG {
				// Namespace and row-type fields remain genuine PostgreSQL-only supplements.
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='s2' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"ix"}, {"other"}, {"pk"}, {"q1"}, {"q2"}, {"t"}}))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT conname FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE c.conrelid='s2.t'::regclass AND n.nspname='s2' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"ck"}, {"nn_id"}, {"pk"}}))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='s2' AND (t.typrelid='s2.t'::regclass OR t.oid=(SELECT typarray FROM pg_type WHERE typrelid='s2.t'::regclass)) ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{"_t"}, {"t"}}))
			}
		})
	}
}
