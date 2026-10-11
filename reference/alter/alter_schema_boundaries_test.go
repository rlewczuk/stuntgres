//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// These controls deliberately run only on PostgreSQL: native has no SQL index,
// row-type, general constraint or explicit-transaction table-DDL capability.
func TestReferenceAlterSchemaPostgresClosure(t *testing.T) {
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := &fixture.Recorder{}
	referenceAlterSchemaSetup(t, p, c, r)
	for _, q := range []string{
		"ALTER TABLE s1.t ADD CONSTRAINT pk PRIMARY KEY(id)",
		"ALTER TABLE s1.t ADD CONSTRAINT ck CHECK(id>0)",
		"CREATE INDEX ix ON s1.t(note)",
		"CREATE TABLE s2.other(id INT CONSTRAINT ck CHECK(id>0))",
	} {
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
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='s2' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"ix"}, {"other"}, {"pk"}, {"q1"}, {"q2"}, {"t"}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT conname FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE c.conrelid='s2.t'::regclass AND n.nspname='s2' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"ck"}, {"nn_id"}, {"pk"}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='s2' AND (t.typrelid='s2.t'::regclass OR t.oid=(SELECT typarray FROM pg_type WHERE typrelid='s2.t'::regclass)) ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{"_t"}, {"t"}}))
	fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
}

func TestReferenceAlterSchemaPostgresLaterCollisions(t *testing.T) {
	for _, kind := range []string{"index", "type"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "s1", "s2")
			c, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := &fixture.Recorder{}
			referenceAlterSchemaSetup(t, p, c, r)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE INDEX ix ON s1.t(id)", 0))
			collision := "CREATE TABLE s2.ix(id INT)"
			want := fixture.Diagnostic{Code: "42P07", Message: `relation "ix" already exists in schema "s2"`}
			if kind == "type" {
				collision = "CREATE TYPE s2.t AS ENUM ('probe')"
				want = fixture.Diagnostic{Code: "42710", Message: `type "t" already exists in schema "s2"`}
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(collision, 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("ALTER TABLE s1.t SET SCHEMA s2", want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
				fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='s1' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"free"}, {"ix"}, {"q1"}, {"q2"}, {"t"}}))
				fixture.GenericRenameState(t, p, c, "s1.q1", 10, false, true)
				fixture.GenericRenameState(t, p, c, "s1.q2", 100, false, true)
			}
		})
	}
}

func TestReferenceAlterSchemaPostgresTransactionRollback(t *testing.T) {
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := &fixture.Recorder{}
	referenceAlterSchemaSetup(t, p, c, r)
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, r, fixture.Prepared(fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0)))
	fixture.RunStep(t, p.Ctx, tx, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
	fixture.GenericRenameState(t, p, c, "s1.q1", 10, false, true)
	fixture.GenericRenameState(t, p, c, "s1.q2", 100, false, true)
}
