package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Return a real caller error only after complete source records have been read.
// No test transport or internal Finish seam is substituted for the public API.
type copyFilterTerminalReader struct {
	input    *strings.Reader
	terminal error
}

func (r *copyFilterTerminalReader) Read(dst []byte) (int, error) {
	n, err := r.input.Read(dst)
	if err == io.EOF {
		return n, r.terminal
	}
	return n, err
}

func TestReferenceCopyFilterLifecycle(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE copy_lifecycle(id int)")
		c.exec(t, "INSERT INTO copy_lifecycle VALUES(99)")
		peerDB := p.Native
		if b.PG {
			peerDB = p.CopyPostgres
		}
		peer, err := peerDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		observer := copyTextConnection{conn: peer, ctx: p.Ctx, pg: b.PG}
		t.Run("transaction_visibility", func(t *testing.T) {
			c.exec(t, "BEGIN")
			open := true
			defer func() {
				if open {
					c.exec(t, "ROLLBACK")
				}
			}()
			copyFilterImport(t, c, "COPY copy_lifecycle FROM STDIN WITH (ON_ERROR ignore) WHERE id<>2", "1\nbad\n2\n3\n", 2)
			copyFilterIDs(t, c, "copy_lifecycle", 1, 3, 99)
			copyFilterIDs(t, observer, "copy_lifecycle", 99)
			copyFilterNotices(t, *notices, copyFilterSummary(1))
			*notices = (*notices)[:0]
			c.exec(t, "COMMIT")
			open = false
			copyFilterIDs(t, observer, "copy_lifecycle", 1, 3, 99)
		})
		c.exec(t, "DELETE FROM copy_lifecycle WHERE id<>99")
		t.Run("rollback", func(t *testing.T) {
			c.exec(t, "BEGIN")
			copyFilterImport(t, c, "COPY copy_lifecycle FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY silent)", "1\nbad\n", 1)
			c.exec(t, "ROLLBACK")
			copyFilterIDs(t, c, "copy_lifecycle", 99)
		})
		t.Run("same_owner_held_select", func(t *testing.T) {
			// Unlike wrapped peer consumers this is the physical COPY owner.
			stmt, err := c.conn.PrepareContext(c.ctx, "SELECT id FROM copy_lifecycle ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			consume := func(want ...int64) {
				rows, err := stmt.QueryContext(c.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				names, err := rows.Columns()
				if err != nil {
					t.Fatal(err)
				}
				copyFilterEqual(t, names, []string{"id"})
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				copyFilterEqual(t, types[0].DatabaseTypeName(), "INT4")
				got := make([]int64, 0)
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					got = append(got, id)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				copyFilterEqual(t, got, want)
			}
			consume(99)
			copyFilterImport(t, c, "COPY copy_lifecycle FROM STDIN WHERE id>0", "1\n-1\n", 1)
			consume(1, 99)
			c.exec(t, "DELETE FROM copy_lifecycle WHERE id<>99")
			consume(99)
		})
		t.Run("retained_reader_snapshot", func(t *testing.T) {
			rows, err := peer.QueryContext(p.Ctx, "SELECT id FROM copy_lifecycle ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			names, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			copyFilterEqual(t, names, []string{"id"})
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			copyFilterEqual(t, types[0].DatabaseTypeName(), "INT4")
			copyFilterImport(t, c, "COPY copy_lifecycle FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY silent) WHERE id>0", "1\nbad\n-1\n", 1)
			got := make([]int64, 0)
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				got = append(got, id)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			copyFilterEqual(t, got, []int64{99})
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			copyFilterIDs(t, observer, "copy_lifecycle", 1, 99)
			c.exec(t, "DELETE FROM copy_lifecycle WHERE id<>99")
		})
		for _, tc := range []struct{ name, input, predicate string }{
			{"accepted_prefix", "1\nbad\n", "TRUE"},
			{"all_filtered", "1\n2\n", "FALSE"},
			{"all_rejected", "bad\nworse\n", "TRUE"},
		} {
			t.Run("terminal/"+tc.name, func(t *testing.T) {
				*notices = (*notices)[:0]
				ordinary := errors.New("COPY filter caller terminal error")
				r, err := c.from("COPY copy_lifecycle FROM STDIN WITH (ON_ERROR ignore) WHERE "+tc.predicate, &copyFilterTerminalReader{input: strings.NewReader(tc.input), terminal: ordinary})
				if err == nil || r != (copyTextResult{}) {
					t.Fatalf("terminal result=%+v err=%v", r, err)
				}
				if !b.PG && !errors.Is(err, ordinary) {
					t.Fatalf("caller identity lost: %v", err)
				}
				copyFilterNotices(t, *notices)
				copyFilterIDs(t, observer, "copy_lifecycle", 99)
			})
		}
		// Precancel never reads input; existing active cancellation/reservation
		// definitions retain their real entered/release/join lifecycle witnesses.
		ctx, cancel := context.WithCancel(c.ctx)
		cancel()
		canceled := c
		canceled.ctx = ctx
		never := &copyTextNoRead{}
		r, err := canceled.from("COPY copy_lifecycle FROM STDIN WITH (ON_ERROR ignore) WHERE FALSE", never)
		if !errors.Is(err, context.Canceled) || r != (copyTextResult{}) || never.calls != 0 {
			t.Fatalf("precancel result=%+v err=%v reads=%d", r, err, never.calls)
		}
		copyFilterIDs(t, observer, "copy_lifecycle", 99)
		t.Run("late_cancellation_all_rejected", func(t *testing.T) {
			*notices = (*notices)[:0]
			ctx, cancel := context.WithCancel(c.ctx)
			defer cancel()
			active := c
			active.ctx = ctx
			r, err := active.from("COPY copy_lifecycle FROM STDIN WITH (ON_ERROR ignore)", &fixture.EnumCancelReader{Reader: bytes.NewReader([]byte("bad\nworse\n")), Cancel: cancel})
			if !errors.Is(err, context.Canceled) || r != (copyTextResult{}) {
				t.Fatalf("late cancellation result=%+v err=%v", r, err)
			}
			copyFilterNotices(t, *notices)
			copyFilterIDs(t, observer, "copy_lifecycle", 99)
			if !b.PG {
				copyFilterIDs(t, c, "copy_lifecycle", 99)
			}
		})
	})
}

func TestReferenceCopyFilterFeatureSmoke(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE smoke_copy(id int PRIMARY KEY,keep bool,note text DEFAULT 'fallback')")
		c.exec(t, "INSERT INTO smoke_copy VALUES(99,true,'sentinel')")
		q := "SELECT id,keep,note FROM smoke_copy ORDER BY id"
		conn, stmt := copyFilterHeld(t, p, b, q)
		step := fixture.Select(q, []string{"id", "keep", "note"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(99), true, "sentinel"}})
		copyFilterConsumers(t, p, b, conn, stmt, step)
		copyFilterImport(t, c, "COPY smoke_copy(id,keep) FROM STDIN WITH (FORMAT csv,HEADER MATCH,ON_ERROR ignore,REJECT_LIMIT 2,LOG_VERBOSITY verbose) WHERE keep", "id,keep\n1,true\nbad,true\n2,false\n3,\n4,true\n", 2)
		step.Rows = [][]any{{int64(1), true, "fallback"}, {int64(4), true, "fallback"}, {int64(99), true, "sentinel"}}
		copyFilterConsumers(t, p, b, conn, stmt, step)
		copyFilterNotices(t, *notices, copyFilterRowNotice("smoke_copy", "id", "\"bad\"", 3), copyFilterSummary(1))
		c.output(t, "COPY (SELECT id,keep,note FROM smoke_copy ORDER BY id) TO STDOUT", "1\tt\tfallback\n4\tt\tfallback\n99\tt\tsentinel\n", 3)
		*notices = (*notices)[:0]
		copyFilterFailure(t, c, "COPY smoke_copy(id,keep) FROM STDIN WITH (FORMAT csv,ON_ERROR ignore,REJECT_LIMIT 1,LOG_VERBOSITY verbose) WHERE keep", "5,true\nbad,true\n6,true\nworse,true\n", copyFilterLimitError("smoke_copy", "worse", 4, 1))
		copyFilterNotices(t, *notices, copyFilterRowNotice("smoke_copy", "id", "\"bad\"", 2), copyFilterRowNotice("smoke_copy", "id", "\"worse\"", 4))
		copyFilterConsumers(t, p, b, conn, stmt, step)
		*notices = (*notices)[:0]
		copyFilterFailure(t, c, "COPY smoke_copy(id,keep) FROM STDIN WHERE FALSE", "bad\ttrue\n", copyFilterIntError("smoke_copy", "bad", 1))
		copyFilterImport(t, c, "COPY smoke_copy(id,keep) FROM STDIN WITH (ON_ERROR ignore,LOG_VERBOSITY silent) WHERE FALSE", "bad\ttrue\n", 0)
		copyFilterNotices(t, *notices)
		c.exec(t, "CREATE TABLE smoke_binary(id int,keep bool)")
		r, err := c.from("COPY smoke_binary FROM STDIN WITH (FORMAT binary,ON_ERROR stop) WHERE keep", bytes.NewReader(copyFilterBinaryInput()))
		copyTextSuccess(t, r, err, 1)
		copyFilterIDs(t, c, "smoke_binary", 1)
		r, err = c.from("COPY smoke_binary FROM STDIN WITH (FORMAT binary,ON_ERROR ignore)", bytes.NewReader(copyFilterBinaryInput()))
		copyTextDiagnostic(t, r, err, fixture.Diagnostic{Code: "42601", Message: "only ON_ERROR STOP is allowed in BINARY mode"})
		copyFilterIDs(t, c, "smoke_binary", 1)
		c.exec(t, "CREATE DOMAIN smoke_positive AS int CHECK(VALUE>0)")
		c.exec(t, "CREATE TABLE smoke_domain(id int,v smoke_positive)")
		copyFilterImport(t, c, "COPY smoke_domain FROM STDIN WITH (ON_ERROR ignore)", "1\t1\n2\t-1\n3\t2\n", 2)
		copyFilterIDs(t, c, "smoke_domain", 1, 3)
		copyFilterNotices(t, *notices, copyFilterSummary(1))
		copyFilterConsumers(t, p, b, conn, stmt, step)
	})
}
