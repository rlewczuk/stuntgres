package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterNotices(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE copy_notice(id int)")
		for _, mode := range []string{"default", "verbose", "silent"} {
			for _, rejected := range []int{0, 1, 2} {
				t.Run(fmt.Sprintf("%s/%d", mode, rejected), func(t *testing.T) {
					c.exec(t, "TRUNCATE copy_notice")
					*notices = (*notices)[:0]
					input := "1\n"
					for range rejected {
						input += "bad\n"
					}
					copyFilterImport(t, c, "COPY copy_notice FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY "+mode+")", input, 1)
					want := make([]fixture.Diagnostic, 0)
					if mode == "verbose" {
						for i := range rejected {
							want = append(want, copyFilterRowNotice("copy_notice", "id", "\"bad\"", i+2))
						}
					}
					if mode != "silent" && rejected > 0 {
						want = append(want, copyFilterSummary(rejected))
					}
					copyFilterNotices(t, *notices, want...)
					copyFilterIDs(t, c, "copy_notice", 1)
				})
			}
		}
		*notices = (*notices)[:0]
		copyFilterImport(t, c, "COPY copy_notice FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose) WHERE FALSE", "2\n3\n", 0)
		copyFilterNotices(t, *notices)
		c.exec(t, "SET client_min_messages=warning")
		copyFilterImport(t, c, "COPY copy_notice FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose)", "bad\n", 0)
		copyFilterNotices(t, *notices)
		c.exec(t, "SET client_min_messages=notice")
		copyFilterImport(t, c, "COPY copy_notice FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose)", "bad\n", 0)
		copyFilterNotices(t, *notices, copyFilterRowNotice("copy_notice", "id", "\"bad\"", 1), copyFilterSummary(1))
		for _, tc := range []struct{ name, options, input, display string }{
			{"decoded_text", "", "b\\141d\n", "bad"},
			{"decoded_csv", ",FORMAT csv", "\"ba\"\"d\"\n", "ba\"d"},
			{"utf8_clip", "", strings.Repeat("é", 51) + "\n", strings.Repeat("é", 50) + "..."},
		} {
			t.Run(tc.name, func(t *testing.T) {
				*notices = (*notices)[:0]
				copyFilterImport(t, c, "COPY copy_notice FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose"+tc.options+")", tc.input, 0)
				copyFilterNotices(t, *notices, copyFilterRowNotice("copy_notice", "id", "\""+tc.display+"\"", 1), copyFilterSummary(1))
			})
		}
		*notices = (*notices)[:0]
		value := strings.Repeat("é", 51)
		want := copyFilterIntError("copy_notice", value, 1)
		want.Context = "COPY copy_notice, line 1, column id: \"" + strings.Repeat("é", 50) + "...\""
		copyFilterFailure(t, c, "COPY copy_notice FROM STDIN", value+"\n", want)
		copyFilterNotices(t, *notices)
		t.Run("retained_on_constraint_failure", func(t *testing.T) {
			c.exec(t, "ALTER TABLE copy_notice ADD CONSTRAINT positive CHECK(id>0)")
			copyFilterFailure(t, c, "COPY copy_notice FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose)", "2\nbad\n-1\n", fixture.Diagnostic{Code: "23514", Message: "new row for relation \"copy_notice\" violates check constraint \"positive\"", Detail: "Failing row contains (-1).", Schema: "public", Table: "copy_notice", Constraint: "positive", Context: "COPY copy_notice, line 3: \"-1\""})
			copyFilterNotices(t, *notices, copyFilterRowNotice("copy_notice", "id", "\"bad\"", 2))
			copyFilterIDs(t, c, "copy_notice", 1)
		})
		t.Run("native_handler_peer_sql", func(t *testing.T) {
			// PostgreSQL callbacks execute during network reads; no timing parity
			// is asserted. Native delivery must release engine/owner leases.
			if b.PG {
				return
			}
			records := make([]fixture.Diagnostic, 0)
			var callbackErr error
			calls := 0
			conn := fixture.CopyNoticeConnection(t, p, false, &records, func(fixture.Diagnostic) {
				ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
				defer cancel()
				var n int64
				callbackErr = p.Native.QueryRowContext(ctx, "SELECT count(*) FROM copy_notice").Scan(&n)
				if callbackErr == nil && n != 1 {
					callbackErr = fmt.Errorf("handler count=%d want=1", n)
				}
				calls++
			})
			local := copyTextConnection{conn: conn, ctx: p.Ctx}
			copyFilterImport(t, local, "COPY copy_notice FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY verbose)", "bad\n", 0)
			if callbackErr != nil || calls != 2 {
				t.Fatalf("handler calls=%d err=%v", calls, callbackErr)
			}
			copyFilterNotices(t, records, copyFilterRowNotice("copy_notice", "id", "\"bad\"", 1), copyFilterSummary(1))
			copyFilterIDs(t, local, "copy_notice", 1)
		})
	})
}
