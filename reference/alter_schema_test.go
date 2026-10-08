//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
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
		for _, postgres := range []bool{true, false} {
			name := map[bool]string{true: "postgres", false: "native"}[postgres] + map[bool]string{true: "/prepared", false: "/direct"}[prepared]
			t.Run(name, func(t *testing.T) {
				p := newReferencePair(t)
				schemaReferenceCleanup(t, "s1", "s2")
				db := p.memory
				if postgres {
					db = p.postgres
				}
				c, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				r := &referenceRecorder{}
				referenceAlterSchemaSetup(t, p, c, r)
				prepare := func(q string) *sql.Stmt {
					t.Helper()
					s, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(q, err)
					}
					t.Cleanup(func() { s.Close() })
					return s
				}
				heldSelect := prepare("SELECT id,note FROM s1.t ORDER BY id")
				heldInsert := prepare("INSERT INTO s1.t(id,note) VALUES($1,$2)")
				heldSequence := prepare("SELECT nextval('s1.q1')")
				var value int64
				if err := heldSequence.QueryRowContext(p.ctx).Scan(&value); err != nil || value != 10 {
					t.Fatal(value, err)
				}
				sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s1.q2')", 100)
				var oid, namespace int64
				if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace FROM pg_catalog.pg_class WHERE relname='t'").Scan(&oid, &namespace); err != nil || oid <= 0 || namespace <= 0 {
					t.Fatal(oid, namespace, err)
				}
				if prepared {
					s := prepare("ALTER TABLE s1.t SET SCHEMA s2")
					referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
					result, err := s.ExecContext(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					referenceLockAffected(t, result, 0)
					_, err = s.ExecContext(p.ctx)
					want := controlsReferenceDiagnostic("42P01", `relation "s1.t" does not exist`)
					referenceAssertDiagnostic(t, err, &want)
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
				}
				referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
				for _, tc := range []struct {
					stmt     *sql.Stmt
					position string
					sequence bool
				}{{heldSelect, "21", false}, {heldInsert, "13", false}, {heldSequence, "16", true}} {
					var err error
					if tc.stmt == heldInsert {
						_, err = tc.stmt.ExecContext(p.ctx, 11, "bad")
					} else {
						_, err = tc.stmt.QueryContext(p.ctx)
					}
					object := "s1.t"
					if tc.sequence {
						object = "s1.q1"
					}
					want := controlsReferenceDiagnostic("42P01", `relation "`+object+`" does not exist`)
					want.Position = tc.position
					referenceAssertDiagnostic(t, err, &want)
				}
				sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s2.q1')", 11)
				sequenceProbeValue(t, p.ctx, c, "SELECT currval('s2.q1')", 11)
				sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 11)
				sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s2.q2')", 101)
				sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s1.free')", 1000)
				referenceRunStep(t, p.ctx, c, r, referenceImplicitError("INSERT INTO s2.t VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "s2", Table: "t", Column: "id"}))
				referenceRunStep(t, p.ctx, c, r, referencePrepared(referenceExec("INSERT INTO s2.t(id,note) VALUES($1,$2)", 1, 11, "new")))
				referenceRunStep(t, p.ctx, c, r, referencePrepared(referenceSelect("SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}, {int64(9), nil}, {int64(11), "new"}})))
			})
		}
	}
}

func TestReferenceAlterSchemaCollisionsAndSyntax(t *testing.T) {
	for _, collision := range []string{"CREATE TABLE s2.t(id INT)", "CREATE VIEW s2.t AS SELECT id FROM s1.t", "CREATE SEQUENCE s2.t", "CREATE SEQUENCE s2.q2", ""} {
		for _, postgres := range []bool{true, false} {
			t.Run(collision+map[bool]string{true: "/postgres", false: "/native"}[postgres], func(t *testing.T) {
				p := newReferencePair(t)
				schemaReferenceCleanup(t, "s1", "s2")
				db := p.memory
				if postgres {
					db = p.postgres
				}
				c, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				r := &referenceRecorder{}
				referenceAlterSchemaSetup(t, p, c, r)
				if collision != "" {
					referenceRunStep(t, p.ctx, c, r, referenceExec(collision, 0))
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
					referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
					referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM public.v ORDER BY id"))
					referenceGenericRenameState(t, p, c, "s1.q1", 10, false)
					referenceGenericRenameState(t, p, c, "s1.q2", 100, false)
				}
			})
		}
	}
}

func TestReferenceAlterSchemaMissingAndNotices(t *testing.T) {
	for _, postgres := range []bool{true, false} {
		t.Run(map[bool]string{true: "postgres", false: "native"}[postgres], func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "s1", "s2")
			var notices []referenceDiagnostic
			_, c, r := noticeReferencePool(t, p, postgres, &notices)
			referenceAlterSchemaSetup(t, p, c, r)
			for _, prepared := range []bool{false, true} {
				notices = nil
				step := referenceImplicitError("ALTER TABLE IF EXISTS missing SET SCHEMA s2, DROP COLUMN missing", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ","`, Position: "44"})
				step.prepared = prepared
				if prepared {
					step.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, c, r, step)
				if len(notices) != 0 {
					t.Fatal("malformed conditional utility emitted notice", notices)
				}
			}
			for _, target := range []string{"missing", "s1.missing", "absent.missing"} {
				q := "ALTER TABLE IF EXISTS " + target + " SET SCHEMA absent"
				notices = nil
				s, err := c.PrepareContext(p.ctx, q)
				if err != nil || len(notices) != 0 {
					t.Fatal("Prepare", err, notices)
				}
				result, err := s.ExecContext(p.ctx)
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, result, 0)
				want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices, want)
				}
			}
			for _, tc := range []struct{ q, code, message string }{
				{"ALTER TABLE s1.missing SET SCHEMA s2", "42P01", `relation "s1.missing" does not exist`},
				{"ALTER TABLE absent.t SET SCHEMA s2", "3F000", `schema "absent" does not exist`},
				{"ALTER TABLE s1.t SET SCHEMA absent", "3F000", `schema "absent" does not exist`},
			} {
				s := referencePrepared(referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message}))
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, c, r, s)
			}
			referenceRunStep(t, p.ctx, c, r, referenceExec("CREATE TABLE s2.other(id INT CONSTRAINT nn_id NOT NULL)", 0))
			referenceRunStep(t, p.ctx, c, r, referenceExec("ALTER TABLE s1.t SET SCHEMA s1", 0))
			referenceRunStep(t, p.ctx, c, r, referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0))
			referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
		})
	}
}
