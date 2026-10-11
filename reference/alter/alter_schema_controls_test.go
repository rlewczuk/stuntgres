package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSchemaNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	c, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := p.MemoryRecord
	referenceAlterSchemaSetup(t, p, c, r)
	held, err := p.Native.QueryContext(p.Ctx, "SELECT id,note FROM s1.t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, probe := range []struct {
		q     string
		value int64
	}{{"SELECT nextval('s1.q1')", 10}, {"SELECT nextval('s1.q2')", 100}} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select(probe.q, []string{"nextval"}, []string{"INT8"}, [][]any{{probe.value}}))
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0)))
	for _, probe := range []struct {
		q     string
		value int64
	}{{"SELECT nextval('s2.q1')", 11}, {"SELECT nextval('s2.q2')", 101}} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select(probe.q, []string{"nextval"}, []string{"INT8"}, [][]any{{probe.value}}))
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"s2"."t"`, `"s2"."q1"`, `"s2"."q2"`, `OWNED BY "s2"."t"."id"`, `"nn_id"`, "fallback", "table comment", "id comment", `"s1"."free"`, "CREATE VIEW"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal(fragment, dump.String())
		}
	}
	if strings.Contains(dump.String(), "SET SCHEMA") {
		t.Fatal("dump emitted move history")
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("canonical roundtrip", err, again.String(), dump.String())
	}
	referenceSchemaHeldRows(t, held)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL replay", err, dump.String())
		}
	}
	// Borrow fresh sessions: Dump preserves called generator state, not the
	// pre-Reset physical session's currval/lastval/cache memory.
	for _, backend := range p.Backends() {
		db, r := backend.DB, backend.Recorder
		fixture.RunStep(t, p.Ctx, db, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
		fixture.RunStep(t, p.Ctx, db, r, fixture.Prepared(referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id")))
		fixture.RunStep(t, p.Ctx, db, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
		s := fixture.ImplicitError("SELECT id,note FROM s1.t ORDER BY id", fixture.Diagnostic{Code: "42P01", Message: `relation "s1.t" does not exist`, Position: "21"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, db, r, s)
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("SELECT nextval('s2.q1')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(12)}}))
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("SELECT nextval('s2.q2')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(102)}}))
		fixture.RunStep(t, p.Ctx, db, r, fixture.ImplicitError("INSERT INTO s2.t VALUES(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "s2", Table: "t", Column: "id"}))
		fixture.RunStep(t, p.Ctx, db, r, fixture.Prepared(fixture.Exec("INSERT INTO s2.t(id) VALUES(11)", 1)))
		fixture.RunStep(t, p.Ctx, db, r, fixture.Select("SELECT note FROM s2.t WHERE id=11", []string{"note"}, []string{"TEXT"}, [][]any{{"fallback"}}))
	}
	if p.Postgres == nil {
		return
	}
	var tableComment, columnComment, constraint string
	if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT obj_description('s2.t'::regclass),col_description('s2.t'::regclass,1),(SELECT conname FROM pg_constraint WHERE conrelid='s2.t'::regclass AND contype='n')").Scan(&tableComment, &columnComment, &constraint); err != nil || tableComment != "table comment" || columnComment != "id comment" || constraint != "nn_id" {
		t.Fatal(tableComment, columnComment, constraint, err)
	}
	var owners int
	if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid IN ('s2.q1'::regclass,'s2.q2'::regclass) AND refobjid='s2.t'::regclass AND refobjsubid=1 AND deptype='a'").Scan(&owners); err != nil || owners != 2 {
		t.Fatal("owned association", owners, err)
	}
}
