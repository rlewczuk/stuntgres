package reference_test

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegclassDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			c, err := backend.DB.Conn(ctx)
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
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ input, code, message string }{
				{"", "42602", "invalid name syntax"}, {`""`, "42P01", `relation "" does not exist`}, {`"s`, "42602", "invalid name syntax"}, {`"s"tail`, "42602", "invalid name syntax"}, {"s.", "42602", "invalid name syntax"}, {"a..b", "42602", "invalid name syntax"},
				{"a.b.c.d", "42601", "improper relation name (too many dotted names): a.b.c.d"}, {"rr_missing", "42P01", `relation "rr_missing" does not exist`}, {"rr_absent.rr_missing", "42P01", `relation "rr_absent.rr_missing" does not exist`}, {"public.rr_missing", "42P01", `relation "public.rr_missing" does not exist`}, {"rr_foreign.public.rr_missing", "0A000", `cross-database references are not implemented: "rr_foreign.public.rr_missing"`}, {"é_missing", "42P01", `relation "é_missing" does not exist`}, {" MiSsInG--Suffix ", "42P01", `relation "missing--suffix" does not exist`},
			} {
				t.Run(tc.input, func(t *testing.T) {
					literal := "SELECT '" + strings.ReplaceAll(tc.input, "'", "''") + "'::regclass"
					s := regclassError(literal, tc.code, tc.message, "8", "")
					fixture.RunStep(t, ctx, c, backend.Recorder, s)
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, ctx, c, backend.Recorder, s)
					portal := "unnamed portal parameter $1 = '...'"
					if tc.input == "" {
						portal = "unnamed portal parameter $1 = ''"
					}
					s = regclassError(q, tc.code, tc.message, "", portal, tc.input)
					s.ErrorStage = "execute"
					fixture.RunStep(t, ctx, c, backend.Recorder, s)
					fixture.RunStep(t, ctx, held, backend.Recorder, s)
				})
			}
			fixture.RunStep(t, ctx, c, backend.Recorder, regclassError("SELECT 'é' AS prefix, 'rr_missing'::regclass", "42P01", `relation "rr_missing" does not exist`, "23", ""))
		})
	}
}

func TestReferenceRegclassAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rr_reader"})
	fixture.SchemaReferenceCleanup(t, "rr_visible", "rr_hidden")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			c, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(ctx, "RESET ROLE")
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			for _, q := range []string{"CREATE ROLE rr_reader", "CREATE SCHEMA rr_visible AUTHORIZATION rr_reader", "CREATE SCHEMA rr_hidden", "CREATE TABLE rr_visible.rr_target(v INT)", "CREATE TABLE rr_hidden.rr_target(v INT)"} {
				run(fixture.Exec(q, 0))
			}
			var oid int64
			if err := c.QueryRowContext(ctx, "SELECT 'rr_hidden.rr_target'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"SET ROLE rr_reader", "SET search_path=rr_hidden,rr_visible"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.RegclassRows("SELECT 'rr_target'::regclass AS r", "r", "rr_target"))
			run(regclassError("SELECT v FROM rr_visible.rr_target", "42501", "permission denied for table rr_target", "", ""))
			literal := "SELECT 'rr_hidden.rr_target'::regclass"
			s := regclassError(literal, "42501", "permission denied for schema rr_hidden", "8", "")
			run(s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			run(s)
			q := "SELECT $1::regclass"
			stmt, err := c.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			s = regclassError(q, "42501", "permission denied for schema rr_hidden", "", "unnamed portal parameter $1 = '...'", "rr_hidden.rr_target")
			run(s)
			fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, backend.Recorder, s)
			s = fixture.RegclassRows(q, "regclass", "rr_hidden.rr_target")
			s.Args = []any{fmt.Sprint(oid)}
			run(s)
			run(fixture.Exec("SET search_path=rr_hidden", 0))
			run(regclassError("SELECT 'rr_target'::regclass", "42P01", `relation "rr_target" does not exist`, "8", ""))
		})
	}
}
