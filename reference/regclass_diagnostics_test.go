package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceRegclassDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := "SELECT $1::regclass"
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("Prepare imported unknown parameter", err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			for _, tc := range []struct{ input, code, message string }{
				{"", "42602", "invalid name syntax"}, {`""`, "42P01", `relation "" does not exist`}, {`"s`, "42602", "invalid name syntax"}, {`"s"tail`, "42602", "invalid name syntax"}, {"s.", "42602", "invalid name syntax"}, {"a..b", "42602", "invalid name syntax"},
				{"a.b.c.d", "42601", "improper relation name (too many dotted names): a.b.c.d"}, {"rr_missing", "42P01", `relation "rr_missing" does not exist`}, {"rr_absent.rr_missing", "42P01", `relation "rr_absent.rr_missing" does not exist`}, {"public.rr_missing", "42P01", `relation "public.rr_missing" does not exist`}, {"rr_foreign.public.rr_missing", "0A000", `cross-database references are not implemented: "rr_foreign.public.rr_missing"`}, {"é_missing", "42P01", `relation "é_missing" does not exist`}, {" MiSsInG--Suffix ", "42P01", `relation "missing--suffix" does not exist`},
			} {
				t.Run(tc.input, func(t *testing.T) {
					literal := "SELECT '" + strings.ReplaceAll(tc.input, "'", "''") + "'::regclass"
					s := regclassError(literal, tc.code, tc.message, "8", "")
					referenceRunStep(t, ctx, c, backend.recorder, s)
					s = referencePrepared(s)
					s.errorStage = "prepare"
					referenceRunStep(t, ctx, c, backend.recorder, s)
					portal := "unnamed portal parameter $1 = '...'"
					if tc.input == "" {
						portal = "unnamed portal parameter $1 = ''"
					}
					s = regclassError(q, tc.code, tc.message, "", portal, tc.input)
					s.errorStage = "execute"
					referenceRunStep(t, ctx, c, backend.recorder, s)
					referenceRunStep(t, ctx, held, backend.recorder, s)
				})
			}
			referenceRunStep(t, ctx, c, backend.recorder, regclassError("SELECT 'é' AS prefix, 'rr_missing'::regclass", "42P01", `relation "rr_missing" does not exist`, "23", ""))
		})
	}
}

func TestReferenceRegclassAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"rr_reader"})
	schemaReferenceCleanup(t, "rr_visible", "rr_hidden")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			c, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(ctx, "RESET ROLE")
			run := func(s referenceStep) { referenceRunStep(t, ctx, c, backend.recorder, s) }
			for _, q := range []string{"CREATE ROLE rr_reader", "CREATE SCHEMA rr_visible AUTHORIZATION rr_reader", "CREATE SCHEMA rr_hidden", "CREATE TABLE rr_visible.rr_target(v INT)", "CREATE TABLE rr_hidden.rr_target(v INT)"} {
				run(referenceExec(q, 0))
			}
			var oid int64
			if err := c.QueryRowContext(ctx, "SELECT 'rr_hidden.rr_target'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"SET ROLE rr_reader", "SET search_path=rr_hidden,rr_visible"} {
				run(referenceExec(q, 0))
			}
			run(regclassRows("SELECT 'rr_target'::regclass AS r", "r", "rr_target"))
			run(regclassError("SELECT v FROM rr_visible.rr_target", "42501", "permission denied for table rr_target", "", ""))
			literal := "SELECT 'rr_hidden.rr_target'::regclass"
			s := regclassError(literal, "42501", "permission denied for schema rr_hidden", "8", "")
			run(s)
			s = referencePrepared(s)
			s.errorStage = "prepare"
			run(s)
			q := "SELECT $1::regclass"
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			s = regclassError(q, "42501", "permission denied for schema rr_hidden", "", "unnamed portal parameter $1 = '...'", "rr_hidden.rr_target")
			run(s)
			referenceRunStep(t, ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, backend.recorder, s)
			s = regclassRows(q, "regclass", "rr_hidden.rr_target")
			s.args = []any{fmt.Sprint(oid)}
			run(s)
			run(referenceExec("SET search_path=rr_hidden", 0))
			run(regclassError("SELECT 'rr_target'::regclass", "42P01", `relation "rr_target" does not exist`, "8", ""))
		})
	}
}
