package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumControls(t *testing.T) {
	var emitted string
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		q := "SELECT m,iv FROM mis_control_view"
		want := moneyIntervalSumPair(q, [][]any{{"$3.00", "1 mon 30 days"}})
		if b.PG {
			if emitted == "" {
				t.Fatal("actual native dump missing before PostgreSQL replay")
			}
			// Only an actually produced native dump is replay evidence.
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			moneyIntervalSumBoth(t, p, conn, b, want)
			return
		}
		run(fixture.Exec("CREATE TABLE mis_control(id INTEGER,m MONEY,iv INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO mis_control VALUES (1,'1','1 mon'),(2,'2','30 days')", 2))
		run(fixture.Exec("CREATE VIEW mis_control_view AS SELECT sum(m ORDER BY id) AS m,sum(iv ORDER BY id) AS iv FROM mis_control", 0))
		moneyIntervalSumBoth(t, p, conn, b, want)
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		old, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		if emitted == "" {
			t.Fatal("empty native dump")
		}
		run(fixture.Exec("INSERT INTO mis_control VALUES (3,'4','1 day')", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, moneyIntervalSumPair(q, [][]any{{"$7.00", "1 mon 31 days"}}))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		missing := fixture.ImplicitError("SELECT id FROM mis_control", fixture.Diagnostic{Code: "42P01", Message: "relation \"mis_control\" does not exist", Position: "16"})
		missing.SelectRows, missing.ErrorStage = true, "execute"
		run(missing)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		moneyIntervalSumReadPair(t, old, []any{"$3.00", "1 mon 30 days"})
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\nCREATE TABLE mis_invalid(v absent_mis_type);")); err == nil {
			t.Fatal("invalid restore succeeded")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore %v", err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if redump.String() != emitted {
			t.Fatal("round trip or failed replacement changed dump")
		}
		connector, err := stuntgres.NewDriver().OpenConnector("money-interval-sum-restored/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		rows, err := fresh.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		moneyIntervalSumReadPair(t, rows, []any{"$3.00", "1 mon 30 days"})
	})
}
