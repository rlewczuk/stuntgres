//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterTypePreparedModifiers(t *testing.T) {
	for _, tc := range []struct {
		name, decl, seed, action, typ string
		length, precision, scale      int64
	}{
		{name: "character", decl: "VARCHAR(5)", seed: "'ab'", action: "TYPE VARCHAR(2)", typ: "VARCHAR", length: 2},
		{name: "numeric", decl: "NUMERIC(8,3)", seed: "1.235", action: "TYPE NUMERIC(5,2)", typ: "NUMERIC", precision: 5, scale: 2},
		{name: "collation", decl: "TEXT", seed: "'a'", action: `TYPE TEXT COLLATE pg_catalog."C"`, typ: "TEXT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			for _, b := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.record, s) }
					run(referenceExec("CREATE TABLE t(c "+tc.decl+")", 0))
					run(referenceExec("INSERT INTO t VALUES("+tc.seed+")", 1))
					old, err := conn.PrepareContext(p.ctx, "SELECT c FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					run(referencePrepared(referenceExec("ALTER TABLE t ALTER c "+tc.action, 0)))
					rows, err := old.QueryContext(p.ctx)
					if rows != nil {
						rows.Close()
					}
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
					if got := referenceExtractDiagnostic(t, err); got != want {
						t.Fatal(got, want)
					}
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
						rows.Close()
					}
				})
			}
		})
	}
}
