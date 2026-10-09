package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func referenceAlterSchemaSetup(t *testing.T, p *referencePair, c *sql.Conn, r *referenceRecorder) {
	t.Helper()
	for _, q := range []string{
		"CREATE SCHEMA s1", "CREATE SCHEMA s2",
		"CREATE TABLE s1.t(id INTEGER CONSTRAINT nn_id NOT NULL,note TEXT DEFAULT 'fallback')",
		"COMMENT ON TABLE s1.t IS 'table comment'", "COMMENT ON COLUMN s1.t.id IS 'id comment'",
		"CREATE SEQUENCE s1.q1 START 10 CACHE 1 OWNED BY s1.t.id",
		"CREATE SEQUENCE s1.q2 START 100 CACHE 1 OWNED BY s1.t.id",
		"CREATE SEQUENCE s1.free START 1000", "CREATE VIEW public.v AS SELECT id,note FROM s1.t",
	} {
		referenceRunStep(t, p.ctx, c, r, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, c, r, referenceExec("INSERT INTO s1.t VALUES(7,'keep'),(9,NULL)", 2))
}

func referenceAlterSchemaRows(q string) referenceStep {
	return referenceSelect(q, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}, {int64(9), nil}})
}

func TestReferenceAlterSchemaValuesAndHeldPlans(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			schemaReferenceCleanup(t, "s1", "s2")
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					c, err := db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					r := backend.recorder
					referenceAlterSchemaSetup(t, p, c, r)
					referenceRunStep(t, p.ctx, c, r, referenceExec("SET search_path TO s1,s2", 0))
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
						s, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(q, err)
						}
						statements = append(statements, s)
						return s
					}
					heldSelect := prepare("SELECT id,note FROM s1.t ORDER BY id")
					heldInsert := prepare("INSERT INTO s1.t(id,note) VALUES($1,$2)")
					heldSequence := prepare("SELECT nextval('s1.q1')")
					referenceRunStep(t, p.ctx, referenceHeldStatement{c, heldSequence, "SELECT nextval('s1.q1')"}, r, referenceSelect("SELECT nextval('s1.q1')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}}))
					referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT nextval('s1.q2')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(100)}}))
					var oid, namespace int64
					if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace FROM pg_catalog.pg_class WHERE relname='t'").Scan(&oid, &namespace); err != nil || oid <= 0 || namespace <= 0 {
						t.Fatal(oid, namespace, err)
					}
					if prepared {
						before := ""
						if !backend.pg {
							before = referenceDDLNativeDump(t, p)
						}
						s := prepare("ALTER TABLE s1.t SET SCHEMA s2")
						referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
						if !backend.pg && referenceDDLNativeDump(t, p) != before {
							t.Fatal("utility Prepare changed persistent closure")
						}
						referenceRunStep(t, p.ctx, referenceHeldStatement{c, s, "ALTER TABLE s1.t SET SCHEMA s2"}, r, referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0))
						referenceRunStep(t, p.ctx, referenceHeldStatement{c, s, "ALTER TABLE s1.t SET SCHEMA s2"}, r, roleReferenceError("ALTER TABLE s1.t SET SCHEMA s2", "42P01", `relation "s1.t" does not exist`, "", false, "execute"))
					} else {
						referenceRunStep(t, p.ctx, c, r, referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0))
					}
					var afterOID, afterNamespace int64
					if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace FROM pg_catalog.pg_class WHERE relname='t'").Scan(&afterOID, &afterNamespace); err != nil || afterOID != oid || afterNamespace == namespace {
						t.Fatal("identity/namespace", oid, namespace, afterOID, afterNamespace, err)
					}
					for _, mode := range []bool{false, true} {
						s := referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id")
						s.prepared = mode
						referenceRunStep(t, p.ctx, c, r, s)
						referenceTableDDLMetadata(t, p.ctx, c, backend.pg, mode, "SELECT * FROM s2.t", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true}, 2)
					}
					referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
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
						failure.selectRows = tc.selectRows
						if !tc.selectRows {
							failure.args = []any{11, "bad"}
						}
						referenceRunStep(t, p.ctx, referenceHeldStatement{c, tc.stmt, tc.q}, r, failure)
					}
					for _, probe := range []struct {
						q, label string
						value    int64
					}{{"SELECT nextval('s2.q1')", "nextval", 11}, {"SELECT currval('s2.q1')", "currval", 11}, {"SELECT lastval()", "lastval", 11}, {"SELECT nextval('s2.q2')", "nextval", 101}, {"SELECT nextval('s1.free')", "nextval", 1000}} {
						referenceRunStep(t, p.ctx, c, r, referenceSelect(probe.q, []string{probe.label}, []string{"INT8"}, [][]any{{probe.value}}))
					}
					referenceRunStep(t, p.ctx, c, r, referenceImplicitError("INSERT INTO s2.t VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "s2", Table: "t", Column: "id"}))
					referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
					old := referenceDDLPositionError("SELECT id,note FROM s1.t ORDER BY id", "42P01", `relation "s1.t" does not exist`, "21", false, "execute")
					old.selectRows = true
					referenceRunStep(t, p.ctx, c, r, old)
					referenceRunStep(t, p.ctx, c, r, referencePrepared(referenceExec("INSERT INTO s2.t(id,note) VALUES($1,$2)", 1, 11, "new")))
					referenceRunStep(t, p.ctx, c, r, referencePrepared(referenceSelect("SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}, {int64(9), nil}, {int64(11), "new"}})))
				})
			}
		})
	}
}

func TestReferenceAlterSchemaCollisionsAndSyntax(t *testing.T) {
	for _, collision := range []string{"CREATE TABLE s2.t(id INT)", "CREATE VIEW s2.t AS SELECT id FROM s1.t", "CREATE SEQUENCE s2.t", "CREATE SEQUENCE s2.q2", ""} {
		t.Run(collision, func(t *testing.T) {
			p := newReferenceScenario(t)
			schemaReferenceCleanup(t, "s1", "s2")
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					c, err := db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					r := backend.recorder
					referenceAlterSchemaSetup(t, p, c, r)
					if collision != "" {
						referenceRunStep(t, p.ctx, c, r, referenceExec(collision, 0))
					}
					before := ""
					if !backend.pg {
						before = referenceDDLNativeDump(t, p)
					}
					for _, mode := range []bool{false, true} {
						q := "ALTER TABLE s1.t SET SCHEMA s2"
						want := referenceDiagnostic{Code: "42P07", Message: `relation "t" already exists in schema "s2"`}
						phase := "execute"
						if collision == "CREATE SEQUENCE s2.q2" {
							want.Message = `relation "q2" already exists in schema "s2"`
						}
						if collision == "" {
							q += ", DROP COLUMN missing"
							want = referenceDiagnostic{Code: "42601", Message: `syntax error at or near ","`, Position: "31"}
							if mode {
								phase = "prepare"
							}
						}
						s := referenceImplicitError(q, want)
						s.prepared, s.errorStage = mode, phase
						referenceRunStep(t, p.ctx, c, r, s)
						if !backend.pg && referenceDDLNativeDump(t, p) != before {
							t.Fatal("failed schema move changed complete native closure")
						}
						referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
						referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
						referenceSchemaSequenceState(t, p, c, "s1.q1", 10, false)
						referenceSchemaSequenceState(t, p, c, "s1.q2", 100, false)
					}
				})
			}
		})
	}
}
