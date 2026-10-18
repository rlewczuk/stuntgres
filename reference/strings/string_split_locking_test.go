package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceStringSplitLocking(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, from, extra, replacement                                           string
			zipped, rollback, nested, left, initialEmpty, replacementEmpty, volatile bool
		}{
			{name: "commit", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)"},
			{name: "rollback", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)", rollback: true},
			{name: "zip_commit", from: "ROWS FROM(string_to_table(p.payload,','),string_to_table('x',',')) WITH ORDINALITY AS f(v,z,n)", zipped: true},
			{name: "zip_rollback", from: "ROWS FROM(string_to_table(p.payload,','),string_to_table('x',',')) WITH ORDINALITY AS f(v,z,n)", zipped: true, rollback: true},
			{name: "refreshed_field_filter", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)", extra: " AND f.v='a'"},
			{name: "refreshed_physical_filter", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)", extra: " AND p.payload='a,b'"},
			{name: "independent_constant", from: "string_to_table('a,b',',') WITH ORDINALITY AS f(v,n)"},
			{name: "later_qualifying_field", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)", extra: " AND f.v='b'", replacement: "x,b"},
			{name: "predecessor_prefix", from: "((VALUES(7)) AS h(id) CROSS JOIN LATERAL string_to_table(p.payload||','||h.id::text,',') WITH ORDINALITY AS f(v,n))", nested: true},
			{name: "left_original_zero", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)", left: true, initialEmpty: true},
			{name: "left_replacement_zero", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)", left: true, replacementEmpty: true},
			{name: "left_both_zero", from: "string_to_table(p.payload,',') WITH ORDINALITY AS f(v,n)", left: true, initialEmpty: true, replacementEmpty: true},
			{name: "independent_volatile", from: "string_to_table(nextval('arg_s')::text||',tail',',') WITH ORDINALITY AS f(v,n)", volatile: true},
		} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, holderConn, reader *sql.Conn) {
					fixture.LockExec(t, p.Ctx, holderConn, "CREATE TABLE split_lock(id INT,payload TEXT)", 0)
					initial := "a,b"
					if tc.initialEmpty {
						initial = ""
					}
					fixture.LockExec(t, p.Ctx, holderConn, "INSERT INTO split_lock VALUES(1,'"+initial+"')", 1)
					fixture.LockExec(t, p.Ctx, holderConn, "CREATE SEQUENCE s", 0)
					if tc.volatile {
						fixture.LockExec(t, p.Ctx, holderConn, "CREATE SEQUENCE arg_s", 0)
					}
					holderPID, readerPID := 0, 0
					if b.PG {
						holderPID, readerPID = fixture.RowPIDs(t, p.Ctx, holderConn, reader)
					}
					holder := fixture.QueryLockBegin(t, p.Ctx, holderConn)
					replacement := "new"
					if tc.replacement != "" {
						replacement = tc.replacement
					}
					if tc.replacementEmpty {
						replacement = ""
					}
					fixture.RowAffected(t, p.Ctx, holder, "UPDATE split_lock SET payload='"+replacement+"' WHERE id=1", false, 1)
					// PostgreSQL 18.6 rechecks a lock by refreshing the locked row and
					// requalifying it with the function-scan tuple the row was produced
					// with; it does not rerun the function (18.0 did).
					selectList := "p.payload,f.v,f.n"
					names, types := []string{"payload", "v", "n"}, []string{"TEXT", "TEXT", "INT8"}
					want := [][]any{{"new", "a", int64(1)}, {"new", "b", int64(2)}}
					if tc.rollback {
						want = [][]any{{"a,b", "a", int64(1)}, {"a,b", "b", int64(2)}}
					}
					if tc.zipped {
						selectList = "p.payload,f.v,f.z,f.n"
						names = []string{"payload", "v", "z", "n"}
						types = []string{"TEXT", "TEXT", "TEXT", "INT8"}
						want = [][]any{{"new", "a", "x", int64(1)}, {"new", "b", nil, int64(2)}}
						if tc.rollback {
							want = [][]any{{"a,b", "a", "x", int64(1)}, {"a,b", "b", nil, int64(2)}}
						}
					}
					switch tc.name {
					case "refreshed_field_filter":
						want = [][]any{{"new", "a", int64(1)}}
					case "refreshed_physical_filter":
						want = nil
					case "later_qualifying_field":
						want = [][]any{{"x,b", "b", int64(2)}}
					case "predecessor_prefix":
						want = [][]any{{"new", "a", int64(1)}, {"new", "b", int64(2)}, {"new", "7", int64(3)}}
					case "left_original_zero":
						want = [][]any{{"new", nil, nil}}
					case "left_replacement_zero":
						want = [][]any{{"", "a", int64(1)}, {"", "b", int64(2)}}
					case "left_both_zero":
						want = [][]any{{"", nil, nil}}
					}
					if tc.volatile {
						want = [][]any{{"new", "1", int64(1)}, {"new", "tail", int64(2)}}
					}
					join, tail := " CROSS JOIN LATERAL ", ""
					if tc.nested {
						join = " CROSS JOIN "
					}
					if tc.left {
						join = " LEFT JOIN LATERAL "
						tail = " ON TRUE"
					}
					q := "SELECT " + selectList + " FROM split_lock p" + join + tc.from + tail + " WHERE p.id=1" + tc.extra + " ORDER BY f.n,nextval('s') FOR UPDATE OF p"
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					done := fixture.RowAsync(t, ctx, reader, q, prepared, true, b.Recorder.RawBackend)
					// Both are public pre-release observations, not assumed queue timing.
					// Do not assert volatile call counts during refreshed-row rechecks.
					if b.PG {
						fixture.RowWait(t, ctx, readerPID, holderPID)
					} else {
						fixture.LimitSequenceCapture(t, ctx, holder)
					}
					var err error
					if tc.rollback {
						err = holder.Rollback()
					} else {
						err = holder.Commit()
					}
					if err != nil {
						t.Fatal(err)
					}
					out := fixture.LockResult(t, done)
					if out.Stmt != nil {
						defer out.Stmt.Close()
					}
					fixture.StringCoreRetainedRows(t, out.Rows, out.Err, names, types, want, false)
				})
			})
		}
	}
}
