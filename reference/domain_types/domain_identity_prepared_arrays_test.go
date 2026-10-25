package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainIdentityPreparedArrays(t *testing.T) {
	for _, checked := range []bool{true, false} {
		name := "unchecked"
		if checked {
			name = "checked"
		}
		t.Run(name, func(t *testing.T) {
			domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) {
					t.Helper()
					fixture.RunStep(t, p.Ctx, c, r, s)
				}
				declaration := "CREATE DOMAIN di_src.d AS integer"
				if checked {
					declaration += " CONSTRAINT positive CHECK(VALUE>0)"
				}
				domainIdentityDDL(t, p, c, r, "CREATE SCHEMA di_src", "CREATE SCHEMA di_dst", declaration)
				var originalPath string
				if err := c.QueryRowContext(p.Ctx, "SHOW search_path").Scan(&originalPath); err != nil {
					t.Fatal(err)
				}
				oldArray := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_src.d'::regtype::oid")
				prepare := func(q string) fixture.HeldStatement {
					t.Helper()
					s, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(q, err)
					}
					t.Cleanup(func() {
						if err := s.Close(); err != nil {
							t.Error(err)
						}
					})
					return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
				}
				read := func(held fixture.HeldStatement, input, want any) {
					t.Helper()
					s := fixture.Select(held.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, input)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.DriverBasicMetadata(t, rows, b.PG, s.Types, []bool{false}, 0)
					}
					fixture.RunStep(t, p.Ctx, held, r, s)
				}
				heldError := func(held fixture.HeldStatement, want fixture.Diagnostic, input any) {
					t.Helper()
					s := fixture.ErrorDiagnostic(held.SQL, want, input)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, r, s)
				}
				// Complete pinned18.6 parameter-input diagnostics, including the
				// existing public array transport context on both backends.
				inputError := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "broken"`)
				inputError.Context = "unnamed portal parameter $1 = '...'"
				checkError := func(constraint string) fixture.Diagnostic {
					want := fixture.ControlsReferenceDiagnostic("23514", `value for domain di_dst.renamed violates check constraint "`+constraint+`"`)
					want.Context = "unnamed portal parameter $1 = '...'"
					want.Schema, want.DataType, want.Constraint = "di_dst", "renamed", constraint
					return want
				}
				// Bracketed domain syntax and direct companion spelling resolve the
				// same carrier but require different reconstructed annotations.
				var held []fixture.HeldStatement
				oldAnnotations := []string{"di_src.d[]", "di_src._d"}
				for _, annotation := range oldAnnotations {
					x := prepare("SELECT ($1::" + annotation + ")::text AS v")
					held = append(held, x)
					read(x, "{7,NULL}", "{7,NULL}")
				}
				domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.d RENAME TO renamed", "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst")
				domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_dst.renamed'::regtype::oid", oldArray)
				for _, x := range held {
					read(x, "{8,NULL}", "{8,NULL}")
					read(x, "[0:1]={9,NULL}", "[0:1]={9,NULL}")
					read(x, "{NULL,NULL}", "{NULL,NULL}")
					read(x, nil, nil)
					heldError(x, inputError, "{broken,NULL}")
					if checked {
						heldError(x, checkError("positive"), "{0,NULL}")
					} else {
						read(x, "{0,NULL}", "{0,NULL}")
					}
					read(x, "{8,NULL}", "{8,NULL}")
				}
				current := prepare("SELECT ($1::di_dst.renamed[])::text AS v")
				read(current, "{11,NULL}", "{11,NULL}")
				// Failed environment reanalysis must retire the original captures;
				// restoring the old environment cannot revive their renamed target.
				for _, path := range []string{"di_dst,public", originalPath} {
					domainIdentityDDL(t, p, c, r, "SET search_path TO "+path)
					for i, x := range held {
						want := fixture.ControlsReferenceDiagnostic("42704", `type "`+oldAnnotations[i]+`" does not exist`)
						want.Position = "13"
						heldError(x, want, "{8,NULL}")
					}
				}
				read(current, "{8,NULL}", "{8,NULL}")
				// The companion identity cannot conceal changed element semantics,
				// including replacing a CHECK without changing its presence flags.
				domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_dst.renamed ADD CONSTRAINT upper_bound CHECK(VALUE<10)")
				heldError(current, checkError("upper_bound"), "{11,NULL}")
				domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_dst.renamed DROP CONSTRAINT upper_bound")
				read(current, "{11,NULL}", "{11,NULL}")
				domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_dst.renamed ADD CONSTRAINT upper_bound CHECK(VALUE<9)")
				heldError(current, checkError("upper_bound"), "{9,NULL}")
				read(current, "{8,NULL}", "{8,NULL}")
				domainIdentityDDL(t, p, c, r, "DROP DOMAIN di_dst.renamed")
				// PostgreSQL retains the retired physical parameter OID; native
				// retains its established missing-name/changed-parameter fences.
				retired := fixture.ControlsReferenceDiagnostic("42704", `type "di_dst.renamed[]" does not exist`)
				retired.Position = "13"
				if b.PG {
					retired = fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for type %d", oldArray))
					retired.Context = "unnamed portal parameter $1"
				}
				heldError(current, retired, "{8,NULL}")
				domainIdentityDDL(t, p, c, r, "CREATE DOMAIN di_dst.renamed AS integer")
				replaced := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type")
				if b.PG {
					replaced = retired
				}
				heldError(current, replaced, "{8,NULL}")
				run(fixture.Prepared(fixture.Select(current.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"{8,NULL}"}}, "{8,NULL}")))
			})
		})
	}
}
