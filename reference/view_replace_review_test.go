package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceViewReplaceCreationNamespace(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"view", "table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				schemaReferenceCleanup(t, "first_schema")
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						c, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, backend.recorder, s) }
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

func TestReferenceViewReplaceBPCharDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, oldBPChar := range []bool{false, true} {
			t.Run(fmt.Sprintf("old_bpchar=%v/prepared=%v", oldBPChar, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				oldExpression, newExpression, oldType, newType, logicalType := "txt", "txt::bpchar", "text", "bpchar", "TEXT"
				if oldBPChar {
					oldExpression, newExpression, oldType, newType, logicalType = newExpression, oldExpression, newType, oldType, "BPCHAR"
				}
				for _, q := range []string{"CREATE TABLE t(txt TEXT)", "CREATE VIEW v AS SELECT " + oldExpression + " AS x FROM t", "CREATE VIEW w AS SELECT x FROM v"} {
					p.step(t, referenceExec(q, 0))
				}
				p.step(t, referenceExec("INSERT INTO t VALUES('a')", 1))
				var before bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
					t.Fatal(err)
				}
				s := referenceImplicitError("CREATE OR REPLACE VIEW v AS SELECT "+newExpression+" AS x FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from " + oldType + " to " + newType})
				s.prepared, s.errorStage = prepared, "execute"
				p.step(t, s)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
					t.Fatal("failed replacement changed native catalog", err)
				}
				p.step(t, referenceSelect("SELECT x FROM v", []string{"x"}, []string{logicalType}, [][]any{{"a"}}))
				p.step(t, referenceSelect("SELECT x FROM w", []string{"x"}, []string{logicalType}, [][]any{{"a"}}))
			})
		}
	}
}
