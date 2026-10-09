package reference_test

import (
	"reflect"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypePreparedModifiersSourceConsumerRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, decl, seed, action, typ string
		length, precision, scale      int64
	}{
		{name: "integer", decl: "INT", seed: "7", action: "TYPE BIGINT", typ: "INT8"},
		{name: "character", decl: "VARCHAR(5)", seed: "'ab'", action: "TYPE VARCHAR(2)", typ: "VARCHAR", length: 2},
		{name: "numeric", decl: "NUMERIC(8,3)", seed: "1.235", action: "TYPE NUMERIC(5,2)", typ: "NUMERIC", precision: 5, scale: 2},
		{name: "collation", decl: "TEXT", seed: "'a'", action: `TYPE TEXT COLLATE pg_catalog."C"`, typ: "TEXT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { s.nonContext = !s.prepared; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT,c "+tc.decl+")", 0))
					run(referenceExec("INSERT INTO t VALUES(1,"+tc.seed+")", 1))
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					old, err := conn.PrepareContext(p.ctx, "SELECT c FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					unaffected, err := conn.PrepareContext(p.ctx, "SELECT id FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer unaffected.Close()
					referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("ALTER TABLE t ALTER c "+tc.action, 0))
					rows, err := old.QueryContext(p.ctx)
					if rows != nil {
						rows.Close()
						t.Fatal("cached plan failure exposed a cursor")
					}
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
					if got := referenceExtractDiagnostic(t, err); got != want {
						t.Fatal(got, want)
					}
					x := referenceHeldStatement{referenceExecutor: conn, statement: unaffected, query: "SELECT id FROM t"}
					referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fresh, err := conn.PrepareContext(p.ctx, "SELECT c FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer fresh.Close()
					for _, prepared := range []bool{false, true} {
						if prepared {
							rows, err = fresh.QueryContext(p.ctx)
						} else {
							rows, err = conn.QueryContext(p.ctx, "SELECT c FROM t")
						}
						if err != nil {
							t.Fatal(err)
						}
						metadata, err := rows.ColumnTypes()
						if err != nil {
							rows.Close()
							t.Fatal(err)
						}
						c := metadata[0]
						if c.DatabaseTypeName() != tc.typ {
							rows.Close()
							t.Fatal(c)
						}
						if tc.length != 0 {
							if length, ok := c.Length(); !ok || length != tc.length {
								rows.Close()
								t.Fatal(length, ok)
							}
						}
						if tc.precision != 0 {
							if precision, scale, ok := c.DecimalSize(); !ok || precision != tc.precision || scale != tc.scale {
								rows.Close()
								t.Fatal(precision, scale, ok)
							}
						}
						if tc.typ == "INT8" && c.ScanType() != reflect.TypeFor[int64]() {
							rows.Close()
							t.Fatal(c.ScanType())
						}
						if nullable, known := c.Nullable(); (!b.pg && (!known || !nullable)) || (b.pg && known) {
							rows.Close()
							t.Fatal(nullable, known)
						}
						rows.Close()
					}
				})
			}
		})
	}
}
