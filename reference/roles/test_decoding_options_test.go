package reference_test

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTestDecodingOptions(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		var lower, upper time.Time
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT clock_timestamp()").Scan(&lower); err != nil {
			s.T.Fatal(err)
		}
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT clock_timestamp()").Scan(&upper); err != nil {
			s.T.Fatal(err)
		}
		q := "SELECT lsn,xid,data FROM pg_logical_slot_peek_changes('td_slot',NULL,NULL,'skip-empty-xacts','1')"
		r, err := s.C.QueryContext(s.P.Ctx, q)
		if err != nil {
			s.T.Fatal(err)
		}
		var got []tdRow
		for r.Next() {
			var row tdRow
			if err := r.Scan(&row.LSN, &row.XID, &row.Data); err != nil {
				s.T.Fatal(err)
			}
			got = append(got, row)
		}
		if err := r.Err(); err != nil {
			s.T.Fatal(err)
		}
		r.Close()
		if len(got) != 3 {
			s.T.Fatalf("default xid rows: %#v", got)
		}
		xid := got[0].XID
		if xid == "0" || got[1].XID != xid || got[2].XID != xid {
			s.T.Fatal("SQL XID identity", got)
		}
		tdAssertRows(s.T, got, "BEGIN "+xid, tdInsert(1), "COMMIT "+xid)
		tdPeek(s, "", tdTxn(tdInsert(1))...)
		tdPeek(s, ",'include-xids','1','include-xids','0'", tdTxn(tdInsert(1))...)
		rows := tdRead(s, "pg_logical_slot_peek_changes", nil, ",'include-timestamp','1'")
		if len(rows) != 3 || rows[0].Data != "BEGIN" || rows[1].Data != tdInsert(1) {
			s.T.Fatalf("timestamp rows: %#v", rows)
		}
		m := regexp.MustCompile(`^COMMIT \(at (\d{4}-\d\d-\d\d \d\d:\d\d:\d\d(?:\.\d{1,6})?\+00)\)$`).FindStringSubmatch(rows[2].Data)
		if m == nil {
			s.T.Fatalf("UTC commit grammar: %#v", rows)
		}
		stamp, err := time.Parse("2006-01-02 15:04:05.999999Z07", m[1])
		if err != nil || stamp.Before(lower) || stamp.After(upper) {
			s.T.Fatal("commit timestamp bounds", lower, upper, rows, err)
		}
	})
}

func tdStartup(slot, code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Context: `slot "` + slot + `", output plugin "test_decoding", in the startup callback`}
}
func tdFailure(s *fixture.RoleMembershipScenario, q string, d fixture.Diagnostic, args ...any) {
	s.T.Helper()
	d.Severity = "ERROR"
	d.SeverityUnlocalized = "ERROR"
	step := fixture.ErrorDiagnostic(q, d, args...)
	step.SelectRows = true
	step.ErrorStage = "execute"
	s.Run(step)
}

func TestReferenceTestDecodingOptionBooleans(t *testing.T) {
	flags := []string{"include-xids", "include-timestamp", "skip-empty-xacts", "only-local", "include-rewrites", "stream-changes", "force-binary"}
	for _, flag := range flags {
		t.Run(flag, func(t *testing.T) {
			tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
				tdTable(s)
				tdSlot(s, "td_slot")
				s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
				for _, value := range []string{"bad", "o", " true", "false ", "", "2"} {
					q := "SELECT * FROM pg_logical_slot_get_changes('td_slot',NULL,NULL,$1::text,$2::text)"
					tdFailure(s, q, tdStartup("td_slot", "22023", fmt.Sprintf("could not parse value %q for parameter %q", value, flag)), flag, value)
				}
				// The binary function accepts either textual or force-binary output, so
				// the same complete valid parse matrix applies to all seven flags.
				for _, value := range []string{"true", "TRUE", "t", "tr", "yes", "Y", "on", "ON", "1", "false", "F", "fa", "no", "N", "off", "of", "0"} {
					truth := strings.Contains("|true|TRUE|t|tr|yes|Y|on|ON|1|", "|"+value+"|")
					q := "SELECT data FROM pg_logical_slot_peek_binary_changes('td_slot',NULL,NULL,'include-xids','0','skip-empty-xacts','1',$1::text,$2::text)"
					want := tdTxn(tdInsert(1))
					if flag == "include-xids" && truth {
						var xid string
						if err := s.C.QueryRowContext(s.P.Ctx, "SELECT xid::text FROM pg_logical_slot_peek_changes('td_slot',NULL,NULL) LIMIT 1").Scan(&xid); err != nil {
							s.T.Fatal(err)
						}
						want = []string{"BEGIN " + xid, tdInsert(1), "COMMIT " + xid}
					}
					if flag == "include-timestamp" && truth {
						// Timestamp parsing/bounds are independently asserted in Options;
						// suppress it here after exercising its accepted value in order.
						q = "SELECT data FROM pg_logical_slot_peek_binary_changes('td_slot',NULL,NULL,'include-xids','0','skip-empty-xacts','1',$1::text,$2::text,'include-timestamp','0')"
					}
					rows := make([][]any, len(want))
					for i, v := range want {
						rows[i] = []any{v}
					}
					s.Run(fixture.Select(q, []string{"data"}, []string{"BYTEA"}, rows, flag, value))
				}
				tdGet(s, "", tdTxn(tdInsert(1))...)
			})
		})
	}
}

func TestReferenceTestDecodingDiagnostics(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
		before := tdPosition(s)
		for _, tc := range []struct {
			args, code, message string
			startup             bool
		}{
			{"'td_slot',NULL,NULL,'unknown','value'", "22023", `option "unknown" = "value" is unknown`, true},
			{"'td_slot',NULL,NULL,'Include-xids','0'", "22023", `option "Include-xids" = "0" is unknown`, true},
			{"NULL,NULL,NULL", "22004", "slot name must not be null", false},
			{"'td_slot',NULL,NULL,VARIADIC NULL::text[]", "22004", "options array must not be null", false},
			{"'td_slot',NULL,NULL,VARIADIC ARRAY[['a','b'],['c','d']]::text[]", "0A000", "array must be one-dimensional", false},
			{"'td_slot',NULL,NULL,VARIADIC ARRAY['include-xids',NULL]::text[]", "0A000", "array must not contain nulls", false},
			{"'td_slot',NULL,NULL,'include-xids'", "0A000", "array must have even number of elements", false},
			{"'td_missing',NULL,NULL", "42704", `replication slot "td_missing" does not exist`, false},
		} {
			d := fixture.Diagnostic{Code: tc.code, Message: tc.message}
			if tc.startup {
				d = tdStartup("td_slot", tc.code, tc.message)
			}
			tdFailure(s, "SELECT * FROM pg_logical_slot_get_changes("+tc.args+")", d)
			if after := tdPosition(s); after != before {
				s.T.Fatal("error consumed input", before, after, tc)
			}
		}
		tdGet(s, "", tdTxn(tdInsert(1))...)
	})
}

func TestReferenceTestDecodingBinary(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
		before := tdPosition(s)
		for _, fn := range []string{"pg_logical_slot_get_changes", "pg_logical_slot_peek_changes"} {
			q := "SELECT * FROM " + fn + "('td_slot',NULL,NULL,'force-binary','1','force-binary','0')"
			// Context absence is a source hypothesis for the post-startup mode
			// mismatch; a complete diagnostic failure exposes the oracle value.
			tdFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: `logical decoding output plugin "test_decoding" produces binary output, but function "` + fn + `(name,pg_lsn,integer,text[])" expects textual data`})
			if after := tdPosition(s); after != before {
				s.T.Fatal("binary mismatch consumed", before, after)
			}
		}
		for _, fn := range []string{"pg_logical_slot_peek_binary_changes", "pg_logical_slot_peek_binary_changes", "pg_logical_slot_get_binary_changes"} {
			rows := [][]any{{"BEGIN"}, {tdInsert(1)}, {"COMMIT"}}
			s.Run(fixture.Select("SELECT data FROM "+fn+"('td_slot',NULL,NULL,'include-xids','0','skip-empty-xacts','1','force-binary','1')", []string{"data"}, []string{"BYTEA"}, rows))
		}
		s.Run(fixture.Select("SELECT * FROM pg_logical_slot_get_binary_changes('td_slot',NULL,NULL)", []string{"lsn", "xid", "data"}, []string{"PG_LSN", "XID", "BYTEA"}, nil))
	})
}
