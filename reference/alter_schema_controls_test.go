//go:build reference

package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterSchemaNativeControls(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "s1", "s2")
	c, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := &referenceRecorder{}
	referenceAlterSchemaSetup(t, p, c, r)
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s1.q1')", 10)
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s1.q2')", 100)
	referenceRunStep(t, p.ctx, c, r, referencePrepared(referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0)))
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s2.q1')", 11)
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s2.q2')", 101)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"s2"."t"`, `"s2"."q1"`, `"s2"."q2"`, `OWNED BY "s2"."t"."id"`, `"nn_id"`, "fallback", "table comment", "id comment", `"s1"."free"`} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	if strings.Contains(dump.String(), "SET SCHEMA") {
		t.Fatal("dump emitted move history")
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("canonical roundtrip", err, again.String(), dump.String())
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL replay", err, dump.String())
	}
	// Borrow fresh sessions: Dump preserves called generator state, not the
	// pre-Reset physical session's currval/lastval/cache memory.
	for _, db := range []referenceExecutor{p.memory, p.postgres} {
		referenceRunStep(t, p.ctx, db, r, referencePrepared(referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id")))
		referenceRunStep(t, p.ctx, db, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
		s := referenceImplicitError("SELECT id,note FROM s1.t ORDER BY id", referenceDiagnostic{Code: "42P01", Message: `relation "s1.t" does not exist`, Position: "21"})
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, db, r, s)
		referenceRunStep(t, p.ctx, db, r, referenceSelect("SELECT nextval('s2.q1')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(12)}}))
		referenceRunStep(t, p.ctx, db, r, referenceSelect("SELECT nextval('s2.q2')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(102)}}))
		referenceRunStep(t, p.ctx, db, r, referenceImplicitError("INSERT INTO s2.t VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "s2", Table: "t", Column: "id"}))
		referenceRunStep(t, p.ctx, db, r, referencePrepared(referenceExec("INSERT INTO s2.t(id) VALUES(11)", 1)))
		referenceRunStep(t, p.ctx, db, r, referenceSelect("SELECT note FROM s2.t WHERE id=11", []string{"note"}, []string{"TEXT"}, [][]any{{"fallback"}}))
	}
	var tableComment, columnComment, constraint string
	if err := p.postgres.QueryRowContext(p.ctx, "SELECT obj_description('s2.t'::regclass),col_description('s2.t'::regclass,1),(SELECT conname FROM pg_constraint WHERE conrelid='s2.t'::regclass AND contype='n')").Scan(&tableComment, &columnComment, &constraint); err != nil || tableComment != "table comment" || columnComment != "id comment" || constraint != "nn_id" {
		t.Fatal(tableComment, columnComment, constraint, err)
	}
	var owners int
	if err := p.postgres.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid IN ('s2.q1'::regclass,'s2.q2'::regclass) AND refobjid='s2.t'::regclass AND refobjsubid=1 AND deptype='a'").Scan(&owners); err != nil || owners != 2 {
		t.Fatal("owned association", owners, err)
	}
}
