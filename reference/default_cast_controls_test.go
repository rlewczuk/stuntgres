package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceDefaultCastControls(t *testing.T) {
	// PostgreSQL's explicit cast pathways, not assignment coercions.
	want := []any{"1", int64(1), true, int64(7), int64(42), true}
	defaultConsumerReferenceControls(t, "CREATE TABLE d(id INT,s TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT CAST(TRUE AS INT),b BOOL DEFAULT CAST(-2 AS BOOL),n BIGINT DEFAULT CAST(7 AS BIGINT),f INT DEFAULT int4('42'::text),p BOOL DEFAULT ((text(TRUE) = 'true') IS TRUE))", "CAST(", []string{"id", "s", "i", "b", "n", "f", "p"}, []string{"INT4", "TEXT", "INT4", "BOOL", "INT8", "INT4", "BOOL"}, want, want)
}

func TestReferenceDefaultDateCompoundControls(t *testing.T) {
	// Civil chronology reverses lexicographic order of these BC spellings.
	// lib/pq returns finite DATE as time.Time; native DATE returns owned text.
	defaultConsumerReferenceControls(t, "CREATE TABLE d(id INT,v DATE DEFAULT DATE '0002-01-01 BC',b BOOL DEFAULT (DATE '0002-01-01 BC' < DATE '0001-01-01 BC'),e BOOL DEFAULT (DATE '0001-01-01 BC' < DATE '0001-01-01'),n BOOL DEFAULT ((DATE '0002-01-01 BC' < DATE '0001-01-01 BC') IS TRUE))", "DATE '0002-01-01 BC'", []string{"id", "v", "b", "e", "n"}, []string{"INT4", "DATE", "BOOL", "BOOL", "BOOL"}, []any{"0002-01-01 BC", true, true, true}, []any{time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC), true, true, true})
}

func TestReferenceDefaultCastDeferredInputControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			create := referenceExec("CREATE TABLE d(id INT,v INT DEFAULT CAST('oops'::text AS int4))", 0)
			selectRows := referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}})
			failure := func(q string) referenceStep {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
				s.errorStage = "execute"
				return s
			}
			if p.postgres != nil {
				t.Run("postgres-oracle", func(t *testing.T) {
					run := func(s referenceStep) {
						s.prepared = prepared
						defaultReferenceRunStep(t, p, p.postgres, p.postgresRecord, s)
					}
					run(create)
					run(referenceExec("INSERT INTO d VALUES(1,9),(2,NULL)", 2))
					for _, q := range []string{"INSERT INTO d(id) VALUES(3)", "INSERT INTO d VALUES(4,DEFAULT)"} {
						run(failure(q))
						run(selectRows)
					}
				})
			}
			t.Run("native-controls-and-replay", func(t *testing.T) {
				native := func(s referenceStep) {
					s.prepared = prepared
					defaultReferenceRunStep(t, p, p.memory, p.memoryRecord, s)
				}
				both := func(s referenceStep) { s.prepared = prepared; defaultReferenceStep(t, p, s) }
				native(create)
				defaultConsumerReferenceRoundtrip(t, p, "CAST(", "d")
				both(referenceExec("INSERT INTO d VALUES(1,9),(2,NULL)", 2))
				defaultConsumerReferenceRoundtrip(t, p, "CAST(", "d")
				for _, q := range []string{"INSERT INTO d(id) VALUES(3)", "INSERT INTO d VALUES(4,DEFAULT)"} {
					both(failure(q))
					both(selectRows)
				}
			})
		})
	}
}

func TestReferenceDefaultCastParameterProhibitions(t *testing.T) {
	for _, expression := range []string{"int4($1)", "int8($1)", "bool($1)", "text($1)", "CAST($1 AS INT)", "$1::int4", "CAST(int4($1) AS TEXT)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%s", prepared, expression), func(t *testing.T) {
				p := newReferenceScenario(t)
				q := "CREATE TABLE bad(v INT DEFAULT " + expression + ")"
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(strings.Index(q, "$1") + 1)})
					s.prepared, s.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, db, r, s)
					referenceRunStep(t, p.ctx, db, r, referenceImplicitError("SELECT * FROM bad", referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
				})
			})
		}
	}
}
