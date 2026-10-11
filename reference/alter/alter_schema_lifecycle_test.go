package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceAlterSchemaSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	for _, q := range []string{
		"CREATE SCHEMA s1", "CREATE SCHEMA s2",
		"CREATE TABLE s1.t(id INTEGER CONSTRAINT nn_id NOT NULL,note TEXT DEFAULT 'fallback')",
		"COMMENT ON TABLE s1.t IS 'table comment'", "COMMENT ON COLUMN s1.t.id IS 'id comment'",
		"CREATE SEQUENCE s1.q1 START 10 CACHE 1 OWNED BY s1.t.id",
		"CREATE SEQUENCE s1.q2 START 100 CACHE 1 OWNED BY s1.t.id",
		"CREATE SEQUENCE s1.free START 1000", "CREATE VIEW public.v AS SELECT id,note FROM s1.t",
	} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO s1.t VALUES(7,'keep'),(9,NULL)", 2))
}

func referenceAlterSchemaRows(q string) fixture.Step {
	return fixture.Select(q, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}, {int64(9), nil}})
}

func TestReferenceAlterSchemaValuesAndHeldPlans(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "s1", "s2")
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					c, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					r := backend.Recorder
					referenceAlterSchemaSetup(t, p, c, r)
					fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO s1,s2", 0))
					statements := []*sql.Stmt{}
					defer func() {
						for _, s := range statements {
							if err := s.Close(); err != nil {
								t.Error(err)
							}
						}
					}()
					prepare := func(q string) *sql.Stmt {
						t.Helper()
						s, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(q, err)
						}
						statements = append(statements, s)
						return s
					}
					heldSelect := prepare("SELECT id,note FROM s1.t ORDER BY id")
					heldInsert := prepare("INSERT INTO s1.t(id,note) VALUES($1,$2)")
					heldSequence := prepare("SELECT nextval('s1.q1')")
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, heldSequence, "SELECT nextval('s1.q1')"}, r, fixture.Select("SELECT nextval('s1.q1')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}}))
					fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT nextval('s1.q2')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(100)}}))
					var oid, namespace int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid,relnamespace FROM pg_catalog.pg_class WHERE relname='t'").Scan(&oid, &namespace); err != nil || oid <= 0 || namespace <= 0 {
						t.Fatal(oid, namespace, err)
					}
					if prepared {
						before := ""
						if !backend.PG {
							before = referenceDDLNativeDump(t, p)
						}
						s := prepare("ALTER TABLE s1.t SET SCHEMA s2")
						fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
						if !backend.PG && referenceDDLNativeDump(t, p) != before {
							t.Fatal("utility Prepare changed persistent closure")
						}
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, s, "ALTER TABLE s1.t SET SCHEMA s2"}, r, fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, s, "ALTER TABLE s1.t SET SCHEMA s2"}, r, fixture.RoleReferenceError("ALTER TABLE s1.t SET SCHEMA s2", "42P01", `relation "s1.t" does not exist`, "", false, "execute"))
					} else {
						fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0))
					}
					var afterOID, afterNamespace int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid,relnamespace FROM pg_catalog.pg_class WHERE relname='t'").Scan(&afterOID, &afterNamespace); err != nil || afterOID != oid || afterNamespace == namespace {
						t.Fatal("identity/namespace", oid, namespace, afterOID, afterNamespace, err)
					}
					for _, mode := range []bool{false, true} {
						s := referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id")
						s.Prepared = mode
						fixture.RunStep(t, p.Ctx, c, r, s)
						fixture.TableDDLMetadata(t, p.Ctx, c, backend.PG, mode, "SELECT * FROM s2.t", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true}, 2)
					}
					fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
					for _, tc := range []struct {
						stmt                *sql.Stmt
						q, object, position string
						selectRows          bool
					}{
						{heldSelect, "SELECT id,note FROM s1.t ORDER BY id", "s1.t", "21", true},
						{heldInsert, "INSERT INTO s1.t(id,note) VALUES($1,$2)", "s1.t", "13", false},
						{heldSequence, "SELECT nextval('s1.q1')", "s1.q1", "16", true},
					} {
						failure := referenceDDLPositionError(tc.q, "42P01", `relation "`+tc.object+`" does not exist`, tc.position, false, "execute")
						failure.SelectRows = tc.selectRows
						if !tc.selectRows {
							failure.Args = []any{11, "bad"}
						}
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, tc.stmt, tc.q}, r, failure)
					}
					for _, probe := range []struct {
						q, label string
						value    int64
					}{{"SELECT nextval('s2.q1')", "nextval", 11}, {"SELECT currval('s2.q1')", "currval", 11}, {"SELECT lastval()", "lastval", 11}, {"SELECT nextval('s2.q2')", "nextval", 101}, {"SELECT nextval('s1.free')", "nextval", 1000}} {
						fixture.RunStep(t, p.Ctx, c, r, fixture.Select(probe.q, []string{probe.label}, []string{"INT8"}, [][]any{{probe.value}}))
					}
					fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("INSERT INTO s2.t VALUES(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "s2", Table: "t", Column: "id"}))
					fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
					old := referenceDDLPositionError("SELECT id,note FROM s1.t ORDER BY id", "42P01", `relation "s1.t" does not exist`, "21", false, "execute")
					old.SelectRows = true
					fixture.RunStep(t, p.Ctx, c, r, old)
					fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec("INSERT INTO s2.t(id,note) VALUES($1,$2)", 1, 11, "new")))
					fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Select("SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}, {int64(9), nil}, {int64(11), "new"}})))
				})
			}
		})
	}
}

func TestReferenceAlterSchemaCollisionsAndSyntax(t *testing.T) {
	for _, collision := range []string{"CREATE TABLE s2.t(id INT)", "CREATE VIEW s2.t AS SELECT id FROM s1.t", "CREATE SEQUENCE s2.t", "CREATE SEQUENCE s2.q2", ""} {
		t.Run(collision, func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "s1", "s2")
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					c, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					r := backend.Recorder
					referenceAlterSchemaSetup(t, p, c, r)
					if collision != "" {
						fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(collision, 0))
					}
					before := ""
					if !backend.PG {
						before = referenceDDLNativeDump(t, p)
					}
					for _, mode := range []bool{false, true} {
						q := "ALTER TABLE s1.t SET SCHEMA s2"
						want := fixture.Diagnostic{Code: "42P07", Message: `relation "t" already exists in schema "s2"`}
						phase := "execute"
						if collision == "CREATE SEQUENCE s2.q2" {
							want.Message = `relation "q2" already exists in schema "s2"`
						}
						if collision == "" {
							q += ", DROP COLUMN missing"
							want = fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ","`, Position: "31"}
							if mode {
								phase = "prepare"
							}
						}
						s := fixture.ImplicitError(q, want)
						s.Prepared, s.ErrorStage = mode, phase
						fixture.RunStep(t, p.Ctx, c, r, s)
						if !backend.PG && referenceDDLNativeDump(t, p) != before {
							t.Fatal("failed schema move changed complete native closure")
						}
						fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
						fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
						referenceSchemaSequenceState(t, p, c, "s1.q1", 10, false)
						referenceSchemaSequenceState(t, p, c, "s1.q2", 100, false)
					}
				})
			}
		})
	}
}
