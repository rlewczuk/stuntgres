package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceAlterAddColumnEmptyDefaultEvaluation(t *testing.T) {
	for _, definition := range []string{"b INT DEFAULT (1/0)", "b INT DEFAULT 2147483648", "b VARCHAR(2) DEFAULT 'long'::TEXT"} {
		t.Run(definition, func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				want := referenceDiagnostic{Code: "22012", Message: "division by zero"}
				if strings.Contains(definition, "2147483648") {
					want.Code, want.Message = "22003", "integer out of range"
				}
				if strings.Contains(definition, "VARCHAR") {
					want.Code, want.Message = "22001", "value too long for type character varying(2)"
				}
				run(referenceImplicitError("ALTER TABLE t ADD "+definition, want))
				run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, nil))
				run(referenceExec("ALTER TABLE t ADD b INT DEFAULT 7", 0))
			})
		})
	}
}

func TestReferenceAlterAddColumnSyntax(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"ALTER TABLE t ADD", "ALTER TABLE t ADD b INT CHECK(", "ALTER TABLE t ADD b INT, ADD c", "ALTER TABLE t ADD b INT; ALTER TABLE t ADD c"} {
			t.Run(fmt.Sprintf("%v/%s", prepared, query), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(id INT)", 0))
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1)", 1))
					failure := referenceImplicitError(query, referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: referencePosition(len([]rune(query)) + 1)})
					failure.prepared = prepared
					failure.errorStage = "execute"
					if prepared {
						failure.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, failure)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		}
	}
}

func TestReferenceAlterAddColumnColumnLimit(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		columns := make([]string, 1600)
		for i := range columns {
			columns[i] = fmt.Sprintf("c%d INT", i)
		}
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t("+strings.Join(columns, ",")+")", 0))
		run(referenceExec("ALTER TABLE t DROP c1599", 0))
		run(referenceImplicitError("ALTER TABLE t ADD b INT", referenceDiagnostic{Code: "54011", Message: "tables can have at most 1600 columns"}))
		run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='t'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(1600)}}))
	})
}

func TestReferenceAlterAddColumnUtilityLifetime(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		var notices []referenceDiagnostic
		_, conn, record := noticeReferencePool(t, p, backend.pg, &notices)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		query := "ALTER TABLE t ADD IF NOT EXISTS b INT DEFAULT 7"
		stmt, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if len(notices) != 0 {
			t.Fatal("Prepare emitted notice", notices)
		}
		held := referenceHeldStatement{conn, stmt, query}
		referenceRunStep(t, p.ctx, held, record, referenceExec(query, 0))
		if len(notices) != 0 {
			t.Fatal("first ADD skipped", notices)
		}
		referenceRunStep(t, p.ctx, held, record, referenceExec(query, 0))
		if len(notices) != 1 || notices[0].Code != "42701" {
			t.Fatal(notices)
		}
		badQuery := "ALTER TABLE t ADD c INT DEFAULT 'oops'"
		bad, err := conn.PrepareContext(p.ctx, badQuery)
		if err != nil {
			t.Fatal("utility Prepare bound default", err)
		}
		defer bad.Close()
		failure := referenceImplicitError(badQuery, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		failure.errorStage = "execute"
		referenceRunStep(t, p.ctx, referenceHeldStatement{conn, bad, badQuery}, record, failure)
		run(referenceSelect("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
		tx, err := conn.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, record, referenceImplicitError("ALTER TABLE missing ADD c INT DEFAULT 'oops'", referenceDiagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	})
}
