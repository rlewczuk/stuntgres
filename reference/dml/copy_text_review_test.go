package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyTextOptionsAdmissionPrecedence(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_precedence(v int)")
		c.exec(t, "INSERT INTO copy_precedence VALUES(99)")
		for _, tc := range []struct {
			name, target string
			want         fixture.Diagnostic
		}{
			{"missing_relation", "copy_missing_options", fixture.Diagnostic{Code: "42P01", Message: `relation "copy_missing_options" does not exist`}},
			{"missing_column", "copy_precedence(missing)", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "copy_precedence" does not exist`}},
		} {
			for _, from := range []bool{true, false} {
				direction := "to"
				if from {
					direction = "from"
				}
				t.Run(tc.name+"/"+direction, func(t *testing.T) {
					copyTextReviewRejectedTransfer(t, b, c, tc.target, from, "DELIMITER 'ab'", tc.want)
				})
			}
		}
		t.Run("query_options_before_source", func(t *testing.T) {
			copyTextReviewRejectedTransfer(t, b, c, "(SELECT v FROM copy_missing_options)", false, "DELIMITER 'ab'", fixture.Diagnostic{Code: "0A000", Message: "COPY delimiter must be a single one-byte character"})
		})
		c.exec(t, "CREATE ROLE copy_options_actor")
		if b.PG {
			// Native already grants PUBLIC public-schema USAGE; this setup
			// establishes the same namespace rights on the real oracle.
			c.exec(t, "GRANT USAGE ON SCHEMA public TO copy_options_actor")
		}
		c.exec(t, "SET ROLE copy_options_actor")
		for _, from := range []bool{true, false} {
			direction := "to"
			if from {
				direction = "from"
			}
			t.Run("denied_permission/"+direction, func(t *testing.T) {
				copyTextReviewRejectedTransfer(t, b, c, "copy_precedence", from, "DELIMITER 'ab'", fixture.Diagnostic{Code: "42501", Message: "permission denied for table copy_precedence"})
			})
		}
		c.exec(t, "RESET ROLE")
		c.selectRows(t, "SELECT * FROM copy_precedence", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
	})
}

func TestReferenceCopyTextQuotedFormat(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_review_format(v int)")
		c.exec(t, "INSERT INTO copy_review_format VALUES(99)")
		for _, format := range []string{"TEXT", "Text", "CSV", "BINARY"} {
			for _, from := range []bool{true, false} {
				direction, position := "to", "40"
				if from {
					direction, position = "from", "41"
				}
				t.Run(format+"/"+direction, func(t *testing.T) {
					copyTextReviewRejectedTransfer(t, b, c, "copy_review_format", from, "FORMAT '"+format+"'", fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf(`COPY format "%s" not recognized`, format), Position: position})
				})
			}
		}
		// Unquoted identifiers are lexer-folded; quoted lowercase text remains
		// valid. These are actual transfers, not metadata-only acceptance.
		for _, option := range []string{"FORMAT TEXT", "FORMAT TeXt", "FORMAT 'text'"} {
			result, err := c.from("COPY copy_review_format FROM STDIN WITH("+option+")", strings.NewReader("7\n"))
			copyTextSuccess(t, result, err, 1)
			c.output(t, "COPY (SELECT v FROM copy_review_format ORDER BY v) TO STDOUT WITH("+option+")", "7\n99\n", 2)
			c.exec(t, "DELETE FROM copy_review_format WHERE v=7")
		}
		c.selectRows(t, "SELECT * FROM copy_review_format", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
	})
}

func copyTextReviewRejectedTransfer(t *testing.T, b fixture.Backend, c copyTextConnection, target string, from bool, option string, want fixture.Diagnostic) {
	t.Helper()
	var result copyTextResult
	var err error
	never := &copyTextNoRead{}
	var output bytes.Buffer
	if from {
		result, err = c.from("COPY "+target+" FROM STDIN WITH("+option+")", never)
	} else {
		result, err = c.to("COPY "+target+" TO STDOUT WITH("+option+")", &output)
	}
	copyTextDiagnostic(t, result, err, want)
	if !b.PG && never.calls != 0 {
		t.Fatal("rejected FROM invoked caller Reader")
	}
	if output.Len() != 0 {
		t.Fatal("rejected TO wrote caller", output.String())
	}
}

func TestReferenceCopyTextInputStatementTimeout(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_input_timeout(id int,v text)")
		result, err := c.from("COPY copy_input_timeout FROM STDIN", strings.NewReader("99\tsentinel\n"))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT * FROM copy_input_timeout", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		if !b.PG {
			// Both backends exercise the genuine public baseline above. Only
			// PostgreSQL exposes query_start for the following expiry barrier.
			// TestCopyTextTimeout observes the actual native Operation.Context
			// at this same admitted input stage, without a fake public hook.
			return
		}
		observerConn, err := p.CopyPostgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observerConn.Close()
		observer := copyTextConnection{conn: observerConn, ctx: p.Ctx, pg: true}
		observer.selectRows(t, "SELECT * FROM copy_input_timeout", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		var pid int
		if err := c.conn.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		c.exec(t, "SET statement_timeout TO '100ms'")
		c.selectRows(t, "SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"100ms"}})
		streamContext, stopStream := context.WithTimeout(p.Ctx, 10*time.Second)
		defer stopStream()
		owner := c
		owner.ctx = streamContext
		expiry := newCopyTextBarrier()
		done := make(chan copyTextOutcome, 1)
		joined := false
		defer func() {
			expiry.unblock()
			if !joined {
				<-done
			}
		}()
		const query = "COPY copy_input_timeout FROM STDIN"
		go func() {
			result, err := owner.from(query, &copyTextBlockedReader{barrier: expiry})
			done <- copyTextOutcome{result, err}
		}()
		select {
		case <-expiry.entered:
		case outcome := <-done:
			joined = true
			t.Fatalf("statement timeout before admitted input barrier: %+v", outcome)
		case <-streamContext.Done():
			t.Fatal(streamContext.Err())
		}
		observation, stopObservation := context.WithTimeout(p.Ctx, 5*time.Second)
		defer stopObservation()
		for {
			var reading bool
			if err := observer.conn.QueryRowContext(observation, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event='ClientRead' FROM pg_stat_activity WHERE pid=$1),FALSE)", pid, query).Scan(&reading); err != nil {
				t.Fatal("server COPY input admission", err)
			}
			if reading {
				break
			}
		}
		for {
			var due bool
			// The COPY command's own server query_start, not transaction age,
			// client sleep, cancellation dispatch or pre-EOF idle, is the barrier.
			if err := observer.conn.QueryRowContext(observation, "SELECT COALESCE((SELECT query=$2 AND clock_timestamp() >= query_start + INTERVAL '100 milliseconds' FROM pg_stat_activity WHERE pid=$1),FALSE)", pid, query).Scan(&due); err != nil {
				t.Fatal("server COPY statement deadline", err)
			}
			if due {
				break
			}
		}
		expiry.unblock()
		outcome := <-done
		joined = true
		// Independently recorded pinned PostgreSQL18 input-stage diagnostic;
		// the private native expiry proof uses the same phase's Context shape.
		copyTextDiagnostic(t, outcome.result, outcome.err, fixture.Diagnostic{Code: "57014", Message: "canceling statement due to statement timeout", Context: "COPY copy_input_timeout, line 2"})
		observer.selectRows(t, "SELECT * FROM copy_input_timeout", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		c.exec(t, "SET statement_timeout TO 0")
		c.selectRows(t, "SELECT * FROM copy_input_timeout", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
	})
}
