package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func pgTypeError(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, want fixture.Diagnostic, prepared bool, stage string, args ...any) {
	t.Helper()
	s := fixture.ImplicitError(q, want, args...)
	s.SelectRows = strings.HasPrefix(q, "SELECT") || strings.HasPrefix(q, "/*")
	s.Prepared, s.ErrorStage = prepared, stage
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
}
func TestReferencePGTypeDiagnostics(t *testing.T) {
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT missing FROM pg_catalog.pg_type", "/*é*/ SELECT missing FROM pg_catalog.pg_type", "SELECT missing FROM pg_catalog.pg_type WHERE FALSE"} {
			t.Run(q, func(t *testing.T) {
				position := utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					pgTypeError(t, p, b, b.DB, q, fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: fmt.Sprint(position)}, prepared, stage)
				}
			})
		}
		for _, input := range []struct{ value, code, message string }{
			{"bad", "22P02", "invalid input syntax for type oid: \"bad\""},
			{"4294967296", "22003", "value \"4294967296\" is out of range for type oid"},
		} {
			for _, empty := range []bool{false, true} {
				q := "SELECT oid FROM pg_catalog.pg_type WHERE oid='" + input.value + "'"
				if empty {
					q += " AND FALSE"
				}
				t.Run(q, func(t *testing.T) {
					position := strings.Index(q, "'") + 1
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						pgTypeError(t, p, b, b.DB, q, fixture.Diagnostic{Code: input.code, Message: input.message, Position: fmt.Sprint(position)}, prepared, stage)
					}
				})
			}
			t.Run("held_bind_"+input.value, func(t *testing.T) {
				q := "SELECT oid FROM pg_catalog.pg_type WHERE oid=$1::oid"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				context := "unnamed portal parameter $1 = '...'"
				pgTypeError(t, p, b, held, q, fixture.Diagnostic{Code: input.code, Message: input.message, Context: context}, false, "execute", input.value)
				fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(q, []string{"oid"}, []string{"OID"}, [][]any{{int64(23)}}, int64(23)))
			})
		}
		t.Run("public_read_and_mutation_privileges", func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(q string) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0)) }
			fixture.RoleReferenceCleanup(t, []string{"pt18_catalog_reader"})
			run("CREATE ROLE pt18_catalog_reader")
			defer func() {
				for _, q := range []string{"RESET ROLE", "DROP SCHEMA pt18_hidden CASCADE", "DROP ROLE pt18_catalog_reader"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			run("CREATE SCHEMA pt18_hidden")
			run("CREATE TYPE pt18_hidden.pt18_hidden_enum AS ENUM('x')")
			run("REVOKE ALL ON SCHEMA pt18_hidden FROM PUBLIC")
			run("REVOKE ALL ON TYPE pt18_hidden.pt18_hidden_enum FROM PUBLIC")
			run("SET ROLE pt18_catalog_reader")
			for _, s := range []fixture.Step{
				fixture.Select("SELECT oid,typname FROM pg_catalog.pg_type WHERE oid=23", []string{"oid", "typname"}, []string{"OID", "NAME"}, [][]any{{int64(23), "int4"}}),
				fixture.Select("SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='pt18_hidden' AND t.typname='pt18_hidden_enum'", []string{"typname"}, []string{"NAME"}, [][]any{{"pt18_hidden_enum"}}),
			} {
				fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
				fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Prepared(s))
			}
			for _, q := range []string{
				"UPDATE pg_catalog.pg_type SET typname=typname WHERE FALSE",
				"DELETE FROM pg_catalog.pg_type WHERE FALSE",
				"INSERT INTO pg_catalog.pg_type(typname) VALUES('pt18_forbidden')",
			} {
				t.Run(q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						pgTypeError(t, p, b, conn, q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_type"}, prepared, "execute")
					}
				})
			}
			fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Select("SELECT oid,typname FROM pg_type WHERE oid=23", []string{"oid", "typname"}, []string{"OID", "NAME"}, [][]any{{int64(23), "int4"}}))
			fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Select("SELECT typname FROM pg_type WHERE typname='pt18_forbidden'", []string{"typname"}, []string{"NAME"}, nil))
		})
		if !b.PG {
			t.Run("native_virtual_boundaries", func(t *testing.T) {
				for _, c := range []struct{ q, message string }{
					{"SELECT oid FROM pg_type WHERE oid=23 FOR UPDATE", "row locking is not supported for virtual catalog relations"},
					{"DELETE FROM pg_type WHERE FALSE", "catalog source mutation is unsupported"},
				} {
					pgTypeError(t, p, b, b.DB, c.q, fixture.Diagnostic{Code: "0A000", Message: c.message}, false, "execute")
				}
			})
		}
	})
}
