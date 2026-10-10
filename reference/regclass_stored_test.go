package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func regclassDropDiagnostic(target, detail string) referenceDiagnostic {
	want := controlsReferenceDiagnostic("2BP01", "cannot drop table "+target+" because other objects depend on it")
	want.Detail = detail
	want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	return want
}

func regclassNotice(t *testing.T, notices []referenceDiagnostic, message, detail string) {
	t.Helper()
	want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail}
	if !reflect.DeepEqual(notices, []referenceDiagnostic{want}) {
		t.Fatalf("notices=%#v want %#v", notices, want)
	}
}

func TestReferenceRegclassStoredDependencies(t *testing.T) {
	schemaReferenceCleanup(t, "rr_store_a", "rr_store_b", "rr_store_renamed")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, recorder := noticeReferencePool(t, p, backend.pg, &notices)
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, recorder)
			run := func(s referenceStep) { referenceRunStep(t, ctx, c, recorder, s) }
			for _, q := range []string{"CREATE SCHEMA rr_store_a", "CREATE SCHEMA rr_store_b", "CREATE TABLE rr_store_a.rr_target(v INT)", "CREATE TABLE rr_store_default(id INT,r regclass DEFAULT 'rr_store_a.rr_target'::regclass)", "CREATE TABLE rr_store_check(r regclass, CONSTRAINT rr_bound CHECK(r='rr_store_a.rr_target'::regclass))", "CREATE VIEW rr_store_view AS SELECT 'rr_store_a.rr_target'::regclass AS r"} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO rr_store_default(id) VALUES(1)", 1))
			var original int64
			if err := c.QueryRowContext(ctx, "SELECT 'rr_store_a.rr_target'::regclass::oid").Scan(&original); err != nil {
				t.Fatal(err)
			}
			run(referenceExec(fmt.Sprintf("CREATE TABLE rr_store_numeric(r regclass DEFAULT %d::oid::regclass)", original), 0))
			for _, q := range []string{"ALTER TABLE rr_store_a.rr_target RENAME TO \"Moved.Target\"", "ALTER TABLE rr_store_a.\"Moved.Target\" SET SCHEMA rr_store_b", "ALTER SCHEMA rr_store_b RENAME TO rr_store_renamed", "CREATE TABLE rr_store_a.rr_target(v INT)"} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO rr_store_default(id) VALUES(2)", 1))
			run(referenceExec("INSERT INTO rr_store_numeric DEFAULT VALUES", 1))
			run(referenceSelect("SELECT r::bigint AS id FROM rr_store_default ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{original}, {original}}))
			spelling := `rr_store_renamed."Moved.Target"`
			run(regclassRows("SELECT r FROM rr_store_default ORDER BY id", "r", spelling, spelling))
			run(regclassRows("SELECT r FROM rr_store_view", "r", spelling))
			run(regclassRows("SELECT r FROM rr_store_numeric", "r", spelling))
			run(regclassRows("SELECT 'rr_store_a.rr_target'::text::regclass", "regclass", "rr_store_a.rr_target"))
			detail := "default value for column r of table rr_store_default depends on table " + spelling + "\nconstraint rr_bound on table rr_store_check depends on table " + spelling + "\nview rr_store_view depends on table " + spelling
			run(referenceErrorDiagnostic("DROP TABLE "+spelling, regclassDropDiagnostic(spelling, detail)))
			run(referenceExec("DROP TABLE "+spelling+" CASCADE", 0))
			regclassNotice(t, notices, "drop cascades to 3 other objects", "drop cascades to default value for column r of table rr_store_default\ndrop cascades to constraint rr_bound on table rr_store_check\ndrop cascades to view rr_store_view")
			run(regclassRows("SELECT r FROM rr_store_default ORDER BY id", "r", fmt.Sprint(original), fmt.Sprint(original)))
			run(regclassRows("SELECT r FROM rr_store_numeric", "r", fmt.Sprint(original)))
			run(regclassError("SELECT r FROM rr_store_view", "42P01", `relation "rr_store_view" does not exist`, "15", ""))
			run(referenceExec("INSERT INTO rr_store_default(id) VALUES(3)", 1))
			run(referenceExec("INSERT INTO rr_store_check VALUES(0::regclass)", 1))
			run(regclassRows("SELECT r FROM rr_store_default ORDER BY id", "r", fmt.Sprint(original), fmt.Sprint(original), nil))
		})
	}
	for _, name := range []string{"self", "mutual"} {
		t.Run(name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
					if name == "self" {
						run(referenceExec("CREATE TABLE rr_self(r regclass)", 0))
						run(referenceExec("ALTER TABLE rr_self ALTER COLUMN r SET DEFAULT 'rr_self'::regclass", 0))
						run(referenceExec("INSERT INTO rr_self DEFAULT VALUES", 1))
						run(regclassRows("SELECT r FROM rr_self", "r", "rr_self"))
						run(referenceExec("DROP TABLE rr_self", 0))
					} else {
						for _, q := range []string{"CREATE TABLE rr_cycle_a(r regclass)", "CREATE TABLE rr_cycle_b(r regclass DEFAULT 'rr_cycle_a'::regclass)", "ALTER TABLE rr_cycle_a ALTER COLUMN r SET DEFAULT 'rr_cycle_b'::regclass"} {
							run(referenceExec(q, 0))
						}
						run(referenceExec("INSERT INTO rr_cycle_a DEFAULT VALUES", 1))
						run(referenceExec("INSERT INTO rr_cycle_b DEFAULT VALUES", 1))
						run(regclassRows("SELECT r FROM rr_cycle_a", "r", "rr_cycle_b"))
						run(regclassRows("SELECT r FROM rr_cycle_b", "r", "rr_cycle_a"))
						run(referenceExec("DROP TABLE rr_cycle_a,rr_cycle_b", 0))
					}
				})
			}
		})
	}
}

func TestReferenceRegclassPreparedReanalysis(t *testing.T) {
	schemaReferenceCleanup(t, "rr_prepare_a", "rr_prepare_b")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, ctx, c, backend.recorder, s) }
			for _, q := range []string{"CREATE SCHEMA rr_prepare_a", "CREATE SCHEMA rr_prepare_b", "CREATE TABLE rr_prepare_a.rr_target(v INT)", "CREATE TABLE rr_prepare_b.rr_target(v INT)", "SET search_path=rr_prepare_a,rr_prepare_b"} {
				run(referenceExec(q, 0))
			}
			var aID, bID int64
			if err := c.QueryRowContext(ctx, "SELECT 'rr_prepare_a.rr_target'::regclass::oid::bigint,'rr_prepare_b.rr_target'::regclass::oid::bigint").Scan(&aID, &bID); err != nil || aID == bID {
				t.Fatal(aID, bID, err)
			}
			expectedID := aID
			queries := []string{"SELECT 'rr_target'::regclass AS r,'rr_target'::regclass::oid::bigint AS id", "SELECT 'rr_prepare_a.rr_target'::regclass AS r,'rr_prepare_a.rr_target'::regclass::oid::bigint AS id", "SELECT $1::regclass AS r,$1::regclass::oid::bigint AS id"}
			held := make([]referenceHeldStatement, 0, len(queries))
			for _, q := range queries {
				stmt, err := c.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held = append(held, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q})
			}
			probe := func(qualifiedMissing, unqualifiedMissing bool) {
				for i, h := range held {
					s := referenceSelect(queries[i], []string{"r", "id"}, []string{"REGCLASS", "INT8"}, [][]any{{"rr_target", expectedID}})
					if i == 2 {
						s.args = []any{"rr_target"}
					}
					if i == 1 && qualifiedMissing {
						s = regclassError(queries[i], "42P01", `relation "rr_prepare_a.rr_target" does not exist`, "8", "")
					}
					if i != 1 && unqualifiedMissing {
						position, portal := "8", ""
						var args []any
						if i == 2 {
							position = ""
							portal = "unnamed portal parameter $1 = '...'"
							args = []any{"rr_target"}
						}
						s = regclassError(queries[i], "42P01", `relation "rr_target" does not exist`, position, portal, args...)
					}
					referenceRunStep(t, ctx, h, backend.recorder, s)
				}
			}
			probe(false, false)
			probe(false, false)
			expectedID = bID
			for _, q := range []string{"ALTER TABLE rr_prepare_a.rr_target RENAME TO rr_renamed", "SET search_path=rr_prepare_b,rr_prepare_a", "ALTER TABLE rr_prepare_a.rr_renamed SET SCHEMA rr_prepare_b"} {
				run(referenceExec(q, 0))
				probe(true, false)
			}
			run(referenceExec("DROP TABLE rr_prepare_b.rr_target", 0))
			probe(true, true)
			run(referenceExec("CREATE TABLE rr_prepare_b.rr_target(v INT)", 0))
			if err := c.QueryRowContext(ctx, "SELECT 'rr_prepare_b.rr_target'::regclass::oid::bigint").Scan(&expectedID); err != nil || expectedID == aID || expectedID == bID {
				t.Fatal(expectedID, aID, bID, err)
			}
			probe(true, false)
		})
	}
}

func TestReferenceRegclassDependencyShapes(t *testing.T) {
	for _, consumer := range []string{"default", "view", "check"} {
		for _, shape := range []string{"symbolic", "numeric", "numeric_string", "numeric_integer", "numeric_bigint", "numeric_oid_string", "missing", "null", "opaque_array", "array_constructor", "domain_input", "domain_scalar", "domain_array"} {
			t.Run(consumer+"/"+shape, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						var notices []referenceDiagnostic
						_, c, recorder := noticeReferencePool(t, p, backend.pg, &notices)
						ctx := context.WithValue(p.ctx, referenceRecorderKey{}, recorder)
						run := func(s referenceStep) { referenceRunStep(t, ctx, c, recorder, s) }
						for _, q := range []string{"CREATE TABLE rr_shape_target(v INT)", "CREATE DOMAIN rr_shape_domain AS regclass", "CREATE DOMAIN rr_shape_array_domain AS regclass[]"} {
							run(referenceExec(q, 0))
						}
						var oid int64
						if err := c.QueryRowContext(ctx, "SELECT 'rr_shape_target'::regclass::oid").Scan(&oid); err != nil {
							t.Fatal(err)
						}
						expression, typ := "'rr_shape_target'::regclass", "regclass"
						dependent := shape == "symbolic" || shape == "numeric_string" || shape == "array_constructor" || shape == "domain_input" || shape == "domain_scalar"
						array := false
						switch shape {
						case "numeric":
							expression = fmt.Sprintf("%d::oid::regclass", oid)
						case "numeric_string":
							expression = fmt.Sprintf("'%d'::regclass", oid)
						case "numeric_integer":
							expression = fmt.Sprintf("%d::regclass", oid)
						case "numeric_bigint":
							expression = fmt.Sprintf("%d::bigint::regclass", oid)
						case "numeric_oid_string":
							expression = fmt.Sprintf("'%d'::oid::regclass", oid)
						case "missing":
							expression = "4294967295::regclass"
						case "null":
							expression = "NULL::regclass"
						case "opaque_array":
							expression = "'{rr_shape_target}'::regclass[]"
							typ = "regclass[]"
							array = true
						case "array_constructor":
							expression = "ARRAY['rr_shape_target'::regclass]"
							typ = "regclass[]"
							array = true
						case "domain_input":
							expression = "CAST('rr_shape_target' AS rr_shape_domain)"
							typ = "rr_shape_domain"
						case "domain_scalar":
							expression = "CAST('rr_shape_target'::regclass AS rr_shape_domain)"
							typ = "rr_shape_domain"
						case "domain_array":
							expression = "CAST('{rr_shape_target}' AS rr_shape_array_domain)"
							typ = "rr_shape_array_domain"
							array = true
						}
						create, label := "", ""
						switch consumer {
						case "default":
							create = "CREATE TABLE rr_shape_consumer(r " + typ + " DEFAULT " + expression + ")"
							label = "default value for column r of table rr_shape_consumer"
						case "view":
							create = "CREATE VIEW rr_shape_consumer AS SELECT " + expression + " AS r"
							label = "view rr_shape_consumer"
						case "check":
							create = "CREATE TABLE rr_shape_consumer(r " + typ + ",CONSTRAINT rr_shape_bound CHECK(r=" + expression + "))"
							label = "constraint rr_shape_bound on table rr_shape_consumer"
							if array {
								create = "CREATE TABLE rr_shape_consumer(r regclass,CONSTRAINT rr_shape_bound CHECK((" + expression + ") IS NULL))"
							}
						}
						run(referenceExec(create, 0))
						if consumer == "default" {
							run(referenceExec("INSERT INTO rr_shape_consumer DEFAULT VALUES", 1))
						}
						if consumer == "check" && !array {
							run(referenceExec("INSERT INTO rr_shape_consumer VALUES(NULL)", 1))
						}
						query := "SELECT r FROM rr_shape_consumer"
						value := func(name string) any {
							if consumer == "check" || shape == "null" {
								return nil
							}
							if shape == "missing" {
								return "4294967295"
							}
							if array {
								return "{" + name + "}"
							}
							return name
						}
						selectValues := func(values ...any) {
							s := regclassRows(query, "r", values...)
							if array && consumer != "check" {
								s.types = []string{"_REGCLASS"}
							}
							if len(values) > 1 {
								s.bag = true
							}
							run(s)
						}
						rejectArrayCheck := func() {
							run(referenceImplicitError("INSERT INTO rr_shape_consumer VALUES(0::regclass)", referenceDiagnostic{
								Code: "23514", Message: `new row for relation "rr_shape_consumer" violates check constraint "rr_shape_bound"`,
								Detail: "Failing row contains (-).", Schema: "public", Table: "rr_shape_consumer", Constraint: "rr_shape_bound",
							}))
						}
						if consumer == "check" && array {
							selectValues()
							rejectArrayCheck()
						} else {
							selectValues(value("rr_shape_target"))
						}
						run(referenceExec("ALTER TABLE rr_shape_target RENAME TO rr_shape_renamed", 0))
						if consumer == "check" && array {
							selectValues()
						} else {
							selectValues(value("rr_shape_renamed"))
						}
						if dependent {
							run(referenceErrorDiagnostic("DROP TABLE rr_shape_renamed", regclassDropDiagnostic("rr_shape_renamed", label+" depends on table rr_shape_renamed")))
							run(referenceExec("DROP TABLE rr_shape_renamed CASCADE", 0))
							regclassNotice(t, notices, "drop cascades to "+label, "")
						} else {
							run(referenceExec("DROP TABLE rr_shape_renamed", 0))
							if len(notices) != 0 {
								t.Fatalf("unexpected notices %#v", notices)
							}
						}
						if dependent && consumer == "view" {
							run(regclassError(query, "42P01", `relation "rr_shape_consumer" does not exist`, "15", ""))
							return
						}
						if consumer == "check" && array {
							selectValues()
							if !dependent {
								rejectArrayCheck()
							}
						} else {
							selectValues(value(fmt.Sprint(oid)))
						}
						if consumer == "default" {
							run(referenceExec("INSERT INTO rr_shape_consumer DEFAULT VALUES", 1))
							second := value(fmt.Sprint(oid))
							if dependent {
								second = nil
							}
							selectValues(value(fmt.Sprint(oid)), second)
						}
						if consumer == "check" && dependent {
							run(referenceExec("INSERT INTO rr_shape_consumer VALUES(0::regclass)", 1))
							if array {
								selectValues("-")
							}
						}
					})
				}
			})
		}
	}
}
