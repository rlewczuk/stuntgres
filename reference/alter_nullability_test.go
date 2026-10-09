package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceAlterNullabilityActionLists(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, seed, actions string
			want                referenceDiagnostic
			columns             []string
			rows                [][]any
		}{
			{name: "semantic-before-scan", seed: "NULL,NULL", actions: "ALTER a SET NOT NULL, ALTER missing SET NOT NULL", want: referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
			{name: "drop-before-set", seed: "7,9", actions: "ALTER a SET NOT NULL, ALTER a DROP NOT NULL"},
			{name: "reverse-drop-before-set", seed: "7,9", actions: "ALTER a DROP NOT NULL, ALTER a SET NOT NULL"},
			{name: "drop-column-before-set", seed: "7,9", actions: "ALTER a SET NOT NULL, DROP COLUMN a", want: referenceDiagnostic{Code: "42703", Message: `column "a" of relation "t" does not exist`}},
			{name: "physical-scan-order", seed: "NULL,NULL", actions: "ADD CONSTRAINT b_nn NOT NULL b, ADD CONSTRAINT a_nn NOT NULL a", want: referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}},
			{name: "drop-before-add", seed: "7,9", actions: "ADD CONSTRAINT n NOT NULL a, DROP CONSTRAINT n", want: referenceDiagnostic{Code: "42704", Message: `constraint "n" of relation "t" does not exist`}},
			{name: "projection", seed: "7,9", actions: "DROP COLUMN b, ALTER a SET NOT NULL", columns: []string{"a"}, rows: [][]any{{int64(7)}}},
			{name: "projection-rollback", seed: "NULL,9", actions: "DROP COLUMN b, ALTER a SET NOT NULL", want: referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}},
			{name: "set-before-transformed-add", seed: "7,9", actions: "ADD CONSTRAINT requested NOT NULL a, ALTER a SET NOT NULL"},
		} {
			t.Run(tc.name+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				// Separate backend subtests allow the pinned oracle to run even while native fails.
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
						run(referenceExec("INSERT INTO t VALUES("+tc.seed+")", 1))
						q := "ALTER TABLE t " + tc.actions
						s := referenceExec(q, 0)
						if tc.want.Code != "" {
							s = referenceImplicitError(q, tc.want)
							s.errorStage = "execute"
						}
						s.prepared = prepared
						run(s)
						columns := tc.columns
						types := []string{"INT4"}
						if columns == nil {
							columns = []string{"a", "b"}
							types = append(types, "INT4")
						}
						rows := tc.rows
						if rows == nil {
							if tc.seed == "7,9" {
								rows = [][]any{{int64(7), int64(9)}}
							} else if tc.seed == "NULL,9" {
								rows = [][]any{{nil, int64(9)}}
							} else {
								rows = [][]any{{nil, nil}}
							}
						}
						run(referenceSelect("SELECT * FROM t", columns, types, rows))
						nullable := []bool{tc.want.Code != ""}
						if len(columns) == 2 {
							nullable = append(nullable, true)
						}
						referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, prepared, "SELECT * FROM t", columns, types, nullable, 1)
						if tc.want.Code == "" && tc.name != "projection" {
							if tc.name == "set-before-transformed-add" {
								run(referenceImplicitError("ALTER TABLE t DROP CONSTRAINT requested", referenceDiagnostic{Code: "42704", Message: `constraint "requested" of relation "t" does not exist`}))
							}
							run(referenceExec("ALTER TABLE t DROP CONSTRAINT t_a_not_null", 0))
						}
					})
				}
			})
		}
	}
}

func TestReferenceAlterNullabilityFailingActionNotice(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, backend.pg, &notices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(NULL)", 1))
			run(referenceImplicitError("ALTER TABLE t DROP CONSTRAINT IF EXISTS missing, ALTER a SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "missing" of relation "t" does not exist, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices %#v want %#v", notices, want)
			}
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		})
	}
}

func TestReferenceAlterNullabilityTransactions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(NULL,9)", 1))
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
			tr(referenceExec("UPDATE t SET a=7", 1))
			tr(referenceExec("SAVEPOINT s", 0))
			tr(referenceExec("ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL", 0))
			tr(referenceExec("INSERT INTO t VALUES(8)", 1))
			tr(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			tr(referenceExec("ROLLBACK TO s", 0))
			tr(referenceExec("ROLLBACK TO s", 0))
			tr(referenceExec("INSERT INTO t VALUES(NULL,10)", 1))
			tr(referenceExec("DELETE FROM t WHERE b=10", 1))
			tr(referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			tr(referenceExec("INSERT INTO t VALUES(8,11)", 1))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}, {int64(8), int64(11)}}))
			run(referenceImplicitError("INSERT INTO t VALUES(NULL,12)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 12).", Schema: "public", Table: "t", Column: "a"}))
		})
	}
}

func TestReferenceAlterNullabilityFreshValidation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			run(referenceExec("CREATE TABLE u(a INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7)", 1))
			run(referenceExec("INSERT INTO u VALUES(1)", 1))
			tx, err := b.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect("SELECT a FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceExec("INSERT INTO t VALUES(NULL)", 1))
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError("ALTER TABLE t ALTER a SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
		})
	}
}
