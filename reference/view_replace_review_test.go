//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceViewReplaceCreationNamespace(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"view", "table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				schemaReferenceCleanup(t, "first_schema")
				for _, backend := range []struct {
					name   string
					db     *sql.DB
					record *referenceRecorder
				}{{"postgres_first", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
					t.Run(backend.name, func(t *testing.T) {
						c, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, backend.record, s) }
						for _, q := range []string{"CREATE SCHEMA first_schema", "CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(1)"} {
							run(referenceExec(q, map[string]int64{"INSERT INTO public.t VALUES(1)": 1}[q]))
						}
						later := map[string]string{"view": "CREATE VIEW public.v AS SELECT id FROM public.t", "table": "CREATE TABLE public.v(id INT)", "sequence": "CREATE SEQUENCE public.v START WITH 7"}[kind]
						run(referenceExec(later, 0))
						var oldOID, oldNamespace int64
						if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace FROM pg_class WHERE relname='v'").Scan(&oldOID, &oldNamespace); err != nil {
							t.Fatal(err)
						}
						run(referenceExec("SET search_path TO first_schema,public", 0))
						run(referenceExec("CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM public.t", 0))
						run(referenceSelect("SELECT id FROM first_schema.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
						var newOID, newNamespace, retainedNamespace int64
						if err := c.QueryRowContext(p.ctx, "SELECT oid,relnamespace FROM pg_class WHERE relname='v' AND oid<>$1", oldOID).Scan(&newOID, &newNamespace); err != nil {
							t.Fatal(err)
						}
						if err := c.QueryRowContext(p.ctx, "SELECT relnamespace FROM pg_class WHERE oid=$1", oldOID).Scan(&retainedNamespace); err != nil {
							t.Fatal(err)
						}
						if newOID == oldOID || newNamespace == oldNamespace || retainedNamespace != oldNamespace {
							t.Fatal("wrong creation identity/namespace")
						}
						switch kind {
						case "view":
							run(referenceSelect("SELECT id FROM public.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						case "table":
							run(referenceExec("INSERT INTO public.v VALUES(7)", 1))
							run(referenceSelect("SELECT id FROM public.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						case "sequence":
							sequenceProbeValue(t, p.ctx, c, "SELECT nextval('public.v')", 7)
						}
					})
				}
			})
		}
	}
}

// SQL GRANT controls remain PostgreSQL-only; corresponding native authority
// witnesses use the existing internal ACL API in the engine tests.
func TestReferenceViewReplaceCreationNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, qualified := range []bool{false, true} {
			t.Run(fmt.Sprintf("qualified=%v/prepared=%v", qualified, prepared), func(t *testing.T) {
				creator := fmt.Sprintf("namespace_creator_%v_%v", qualified, prepared)
				otherOwner := fmt.Sprintf("namespace_other_%v_%v", qualified, prepared)
				roleReferenceCleanup(t, []string{creator, otherOwner})
				p := newReferencePair(t)
				schemaReferenceCleanup(t, "first_schema", "target_schema")
				c, err := p.postgres.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
				for _, q := range []string{"CREATE USER " + creator, "CREATE USER " + otherOwner, "CREATE SCHEMA first_schema AUTHORIZATION " + creator, "CREATE SCHEMA target_schema", "CREATE TABLE public.t(id INT)", "CREATE VIEW public.v AS SELECT id FROM public.t", "ALTER VIEW public.v OWNER TO " + otherOwner, "CREATE VIEW target_schema.v AS SELECT id FROM public.t", "ALTER VIEW target_schema.v OWNER TO " + creator, "CREATE VIEW public.w AS SELECT id FROM target_schema.v"} {
					run(referenceExec(q, 0))
				}
				run(referenceExec("INSERT INTO public.t VALUES(1)", 1))
				for _, q := range []string{"GRANT USAGE ON SCHEMA public TO PUBLIC", "GRANT SELECT ON public.t TO " + creator + "," + otherOwner, "REVOKE ALL ON SCHEMA target_schema FROM PUBLIC", "GRANT CREATE ON SCHEMA target_schema TO " + creator} {
					if _, err := referenceAdmin.ExecContext(p.ctx, q); err != nil {
						t.Fatal(q, err)
					}
				}
				var laterOID, targetOID, ruleOID int64
				if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='v' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')").Scan(&laterOID); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='v' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='target_schema')").Scan(&targetOID); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", targetOID).Scan(&ruleOID); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("SET SESSION AUTHORIZATION "+creator, 0))
				run(referenceExec("SET search_path TO first_schema,public", 0))
				name := "v"
				if qualified {
					name = "target_schema.v"
				}
				run(referenceExec("CREATE OR REPLACE VIEW "+name+" AS SELECT id+10 AS id FROM public.t", 0))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceSelect("SELECT id FROM public.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				if qualified {
					run(referenceSelect("SELECT id FROM public.w", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
					var retainedRule int64
					if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", targetOID).Scan(&retainedRule); err != nil || retainedRule != ruleOID {
						t.Fatal(retainedRule, err)
					}
				} else {
					run(referenceSelect("SELECT id FROM first_schema.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				}
				run(referenceSelect("SELECT oid::BIGINT AS oid FROM pg_class WHERE oid=$1", []string{"oid"}, []string{"INT8"}, [][]any{{laterOID}}, laterOID))
			})
		}
	}
}

func TestReferenceViewReplaceBPCharDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, oldBPChar := range []bool{false, true} {
			t.Run(fmt.Sprintf("old_bpchar=%v/prepared=%v", oldBPChar, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				oldExpression, newExpression, oldType, newType, logicalType := "txt", "txt::bpchar", "text", "bpchar", "TEXT"
				if oldBPChar {
					oldExpression, newExpression, oldType, newType, logicalType = newExpression, oldExpression, newType, oldType, "BPCHAR"
				}
				for _, q := range []string{"CREATE TABLE t(txt TEXT)", "CREATE VIEW v AS SELECT " + oldExpression + " AS x FROM t", "CREATE VIEW w AS SELECT x FROM v"} {
					p.step(t, referenceExec(q, 0))
				}
				p.step(t, referenceExec("INSERT INTO t VALUES('a')", 1))
				s := referenceImplicitError("CREATE OR REPLACE VIEW v AS SELECT "+newExpression+" AS x FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from " + oldType + " to " + newType})
				s.prepared, s.errorStage = prepared, "execute"
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
				p.step(t, s)
				p.step(t, referenceSelect("SELECT x FROM v", []string{"x"}, []string{logicalType}, [][]any{{"a"}}))
				p.step(t, referenceSelect("SELECT x FROM w", []string{"x"}, []string{logicalType}, [][]any{{"a"}}))
			})
		}
	}
}
