package reference_test

import (
	"testing"
	"time"
)

func TestReferenceTimestampNewInputErrorsAbortTransaction(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	referenceTimestampStep(t, p, referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
	referenceTimestampStep(t, p, referenceExec("INSERT INTO t VALUES('epoch')", 1))
	for _, backend := range p.backends() {
		db, recorder := backend.db, backend.recorder
		for _, tc := range []struct {
			input               any
			code, message, hint string
		}{
			{"2147483648-on-08", "22008", `date/time field value out of range: "2147483648-on-08"`, ""},
			{"1999-at-08", "22007", `invalid input syntax for type timestamp: "1999-at-08"`, ""},
			{"1999-on-08", "22007", `invalid input syntax for type timestamp: "1999-on-08"`, ""},
			{"1999-01-08--", "22007", `invalid input syntax for type timestamp: "1999-01-08--"`, ""},
			{"1999/01/08//", "22007", `invalid input syntax for type timestamp: "1999/01/08//"`, ""},
			{"99 January 1999", "22007", `invalid input syntax for type timestamp: "99 January 1999"`, ""},
			{"1999-01-08 04:05:06 now1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now1"`, ""},
			{"1999-01-08 04:05:06 now+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now+2"`, ""},
			{"1999.01.08..", "22007", `invalid input syntax for type timestamp: "1999.01.08.."`, ""},
			{"1999-01-08-1", "22007", `invalid input syntax for type timestamp: "1999-01-08-1"`, ""},
			{"1999-01-08-2147483648", "22008", `date/time field value out of range: "1999-01-08-2147483648"`, ""},
			{"2147483648-xxx-08", "22007", `invalid input syntax for type timestamp: "2147483648-xxx-08"`, ""},
			{"2147483648-at-08", "22008", `date/time field value out of range: "2147483648-at-08"`, ""},
			{"1999 0 January", "22007", `invalid input syntax for type timestamp: "1999 0 January"`, ""},
			{"1999 32 January", "22007", `invalid input syntax for type timestamp: "1999 32 January"`, ""},
			{"0 January 1999", "22007", `invalid input syntax for type timestamp: "0 January 1999"`, ""},
			{"32 January 1999", "22007", `invalid input syntax for type timestamp: "32 January 1999"`, ""},
			{"1999 31 February", "22008", `date/time field value out of range: "1999 31 February"`, ""},
			{"1999-01-08 04:05:06 today1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today1"`, ""},
			{"1999-01-08 04:05:06 today+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today+2"`, ""},
			{"1999-01-08 04:05:06 tomorrow1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow1"`, ""},
			{"1999-01-08 04:05:06 tomorrow+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow+2"`, ""},
			{"1999-01-08 04:05:06 yesterday1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday1"`, ""},
			{"1999-01-08 04:05:06 yesterday+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday+2"`, ""},
			{"1999-01-08 04:05:06 epoch1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch1"`, ""},
			{"1999-01-08 04:05:06 epoch+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch+2"`, ""},
			{"1999-01-08 04:05:06 infinity1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity1"`, ""},
			{"1999-01-08 04:05:06 infinity+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity+2"`, ""},
			{"1999-01-08 04:05:06 allballs1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs1"`, ""},
			{"1999-01-08 04:05:06 allballs+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs+2"`, ""},
			{"Friday 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999-01-08 04:05:06"`, ""},
			{"BC 0001-01-01", "22007", `invalid input syntax for type timestamp: "BC 0001-01-01"`, ""},
			{"PM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999-01-08 04:05:06"`, ""},
			{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "294277-01-01 00:00:00Z"`, ""},
			{"January 1999-08", "22008", `date/time field value out of range: "January 1999-08"`, `Perhaps you need a different "DateStyle" setting.`},
			{"Jan 1999/08", "22008", `date/time field value out of range: "Jan 1999/08"`, `Perhaps you need a different "DateStyle" setting.`},
			{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`, ""},
			{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`, ""},
		} {
			tx, err := db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
			step := referenceTransactionError(false, "INSERT INTO t VALUES('2000-01-01'),($1)", want)
			step.args = []any{tc.input}
			pg := want
			pg.Context = "unnamed portal parameter $1 = '...'"
			step.diagnostic = &pg
			step.errorStage = "execute"
			referenceTemporalRunStep(t, p.ctx, tx, recorder, step)
			referenceTemporalRunStep(t, p.ctx, tx, recorder, referenceTransactionError(false, "SELECT v FROM t", referenceTransactionAborted()))
			referenceTemporalRunStep(t, p.ctx, tx, recorder, referenceTransactionError(false, "INSERT INTO t VALUES('epoch')", referenceTransactionAborted()))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceTemporalRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}))
		}
	}
}
