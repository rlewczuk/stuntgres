package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnEmptyDefaultEvaluation(t *testing.T) {
	for _, definition := range []string{"b INT DEFAULT (1/0)", "b INT DEFAULT 2147483648", "b VARCHAR(2) DEFAULT 'long'::TEXT"} {
		t.Run(definition, func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				want := fixture.Diagnostic{Code: "22012", Message: "division by zero"}
				if strings.Contains(definition, "2147483648") {
					want.Code, want.Message = "22003", "integer out of range"
				}
				if strings.Contains(definition, "VARCHAR") {
					want.Code, want.Message = "22001", "value too long for type character varying(2)"
				}
				run(fixture.ImplicitError("ALTER TABLE t ADD "+definition, want))
				run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Exec("ALTER TABLE t ADD b INT DEFAULT 7", 0))
			})
		})
	}
}

func TestReferenceAlterAddColumnSyntax(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"ALTER TABLE t ADD", "ALTER TABLE t ADD b INT CHECK(", "ALTER TABLE t ADD b INT, ADD c", "ALTER TABLE t ADD b INT; ALTER TABLE t ADD c"} {
			t.Run(fmt.Sprintf("%v/%s", prepared, query), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(id INT)", 0))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(len([]rune(query)) + 1)})
					failure.Prepared = prepared
					failure.ErrorStage = "execute"
					if prepared {
						failure.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, failure)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		}
	}
}

func TestReferenceAlterAddColumnColumnLimit(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		columns := make([]string, 1600)
		for i := range columns {
			columns[i] = fmt.Sprintf("c%d INT", i)
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t("+strings.Join(columns, ",")+")", 0))
		run(fixture.Exec("ALTER TABLE t DROP c1599", 0))
		run(fixture.ImplicitError("ALTER TABLE t ADD b INT", fixture.Diagnostic{Code: "54011", Message: "tables can have at most 1600 columns"}))
		run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='t'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(1600)}}))
	})
}

func TestReferenceAlterAddColumnUtilityLifetime(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		query := "ALTER TABLE t ADD IF NOT EXISTS b INT DEFAULT 7"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if len(notices) != 0 {
			t.Fatal("Prepare emitted notice", notices)
		}
		held := fixture.HeldStatement{conn, stmt, query}
		fixture.RunStep(t, p.Ctx, held, record, fixture.Exec(query, 0))
		if len(notices) != 0 {
			t.Fatal("first ADD skipped", notices)
		}
		fixture.RunStep(t, p.Ctx, held, record, fixture.Exec(query, 0))
		if len(notices) != 1 || notices[0].Code != "42701" {
			t.Fatal(notices)
		}
		badQuery := "ALTER TABLE t ADD c INT DEFAULT 'oops'"
		bad, err := conn.PrepareContext(p.Ctx, badQuery)
		if err != nil {
			t.Fatal("utility Prepare bound default", err)
		}
		defer bad.Close()
		failure := fixture.ImplicitError(badQuery, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, bad, badQuery}, record, failure)
		run(fixture.Select("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
		tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, record, fixture.ImplicitError("ALTER TABLE missing ADD c INT DEFAULT 'oops'", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	})
}
