//go:build reference

package reference_test

import "testing"

// These controls deliberately run only on PostgreSQL: native has no SQL index,
// row-type, general constraint or explicit-transaction table-DDL capability.
func TestReferenceAlterSchemaPostgresClosure(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "s1", "s2")
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := &referenceRecorder{}
	referenceAlterSchemaSetup(t, p, c, r)
	for _, q := range []string{
		"ALTER TABLE s1.t ADD CONSTRAINT pk PRIMARY KEY(id)",
		"ALTER TABLE s1.t ADD CONSTRAINT ck CHECK(id>0)",
		"CREATE INDEX ix ON s1.t(note)",
		"CREATE TABLE s1.child(id INT REFERENCES s1.t(id))",
		"CREATE TABLE s2.other(id INT CONSTRAINT ck CHECK(id>0))",
	} {
		referenceRunStep(t, p.ctx, c, r, referenceExec(q, 0))
	}
	var oid int64
	if err := c.QueryRowContext(p.ctx, "SELECT 's1.t'::regclass::oid").Scan(&oid); err != nil || oid <= 0 {
		t.Fatal(oid, err)
	}
	referenceRunStep(t, p.ctx, c, r, referencePrepared(referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0)))
	var moved int64
	if err := c.QueryRowContext(p.ctx, "SELECT 's2.t'::regclass::oid").Scan(&moved); err != nil || moved != oid {
		t.Fatal(oid, moved, err)
	}
	referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='s2' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"ix"}, {"other"}, {"pk"}, {"q1"}, {"q2"}, {"t"}}))
	referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT conname FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE c.conrelid='s2.t'::regclass AND n.nspname='s2' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"ck"}, {"nn_id"}, {"pk"}}))
	referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='s2' AND (t.typrelid='s2.t'::regclass OR t.oid=(SELECT typarray FROM pg_type WHERE typrelid='s2.t'::regclass)) ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{"_t"}, {"t"}}))
	referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
	referenceRunStep(t, p.ctx, c, r, referenceExec("INSERT INTO s1.child VALUES(7)", 1))
}

func TestReferenceAlterSchemaPostgresLaterCollisions(t *testing.T) {
	for _, kind := range []string{"index", "type"} {
		t.Run(kind, func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "s1", "s2")
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := &referenceRecorder{}
			referenceAlterSchemaSetup(t, p, c, r)
			referenceRunStep(t, p.ctx, c, r, referenceExec("CREATE INDEX ix ON s1.t(id)", 0))
			collision := "CREATE TABLE s2.ix(id INT)"
			want := referenceDiagnostic{Code: "42P07", Message: `relation "ix" already exists in schema "s2"`}
			if kind == "type" {
				collision = "CREATE TYPE s2.t AS ENUM ('probe')"
				want = referenceDiagnostic{Code: "42710", Message: `type "t" already exists in schema "s2"`}
			}
			referenceRunStep(t, p.ctx, c, r, referenceExec(collision, 0))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("ALTER TABLE s1.t SET SCHEMA s2", want)
				s.prepared, s.errorStage = prepared, "execute"
				referenceRunStep(t, p.ctx, c, r, s)
				referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
				referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
				referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='s1' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"free"}, {"ix"}, {"q1"}, {"q2"}, {"t"}}))
				referenceGenericRenameState(t, p, c, "s1.q1", 10, false)
				referenceGenericRenameState(t, p, c, "s1.q2", 100, false)
			}
		})
	}
}

func TestReferenceAlterSchemaPostgresTransactionRollback(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "s1", "s2")
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := &referenceRecorder{}
	referenceAlterSchemaSetup(t, p, c, r)
	tx, err := c.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRunStep(t, p.ctx, tx, r, referencePrepared(referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0)))
	referenceRunStep(t, p.ctx, tx, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
	referenceGenericRenameState(t, p, c, "s1.q1", 10, false)
	referenceGenericRenameState(t, p, c, "s1.q2", 100, false)
}
