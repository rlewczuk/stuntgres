package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

const binaryConversionProjection = `convert(b,'LATIN1','UTF8'),convert_from(b,'LATIN1'),convert_to(s,'LATIN1'),encode(b,'hex'),decode(h,'hex')`

var binaryConversionNames = []string{"convert", "convert_from", "convert_to", "encode", "decode"}
var binaryConversionTypes = []string{"BYTEA", "TEXT", "BYTEA", "TEXT", "BYTEA"}

func TestReferenceBinaryConversionMetadata(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_metadata(id integer,b bytea,s text,h text)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO bc_metadata VALUES(1,'\xe9','é','e9'),(2,'','',''),(3,NULL,NULL,NULL)`, 3))
		for _, id := range []int{99, 1, 2, 3} {
			for _, prepared := range []bool{false, true} {
				t.Run(strings.Repeat("p", 1)+string(rune('0'+id%10)), func(t *testing.T) {
					q := "SELECT " + binaryConversionProjection + " FROM bc_metadata WHERE id=$1"
					var r *sql.Rows
					var err error
					if prepared {
						stmt, e := c.PrepareContext(p.ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						r, err = stmt.QueryContext(p.ctx, id)
					} else {
						r, err = c.QueryContext(p.ctx, q, id)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer r.Close()
					check := binaryConversionMetadata(b.pg, binaryConversionNames, binaryConversionTypes)
					check(t, r)
					ct, err := r.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if id == 99 {
						if r.Next() {
							t.Fatal("unexpected row")
						}
					} else {
						if !r.Next() {
							t.Fatal(r.Err())
						}
						got := make([]any, 5)
						dst := make([]any, 5)
						for i := range dst {
							dst[i] = &got[i]
						}
						if err := r.Scan(dst...); err != nil {
							t.Fatal(err)
						}
						var want []any
						if id == 1 {
							want = []any{"é", "é", "\xe9", "e9", "\xe9"}
						} else if id == 2 {
							want = []any{"", "", "", "", ""}
						} else {
							want = []any{nil, nil, nil, nil, nil}
						}
						for i, v := range got {
							referenceAssertRawValue(t, b.pg, binaryConversionTypes[i], v)
							if !reflect.DeepEqual(referenceNormalize(v), want[i]) {
								t.Fatalf("column %d value=%#v want=%#v", i, v, want[i])
							}
							if raw, ok := v.([]byte); ok && len(raw) > 0 {
								raw[0] = 0
							}
						}
						if r.Next() {
							t.Fatal("extra row")
						}
					}
					if err := r.Err(); err != nil {
						t.Fatal(err)
					}
					if err := r.Close(); err != nil {
						t.Fatal(err)
					}
					binaryConversionColumns(t, ct, b.pg, binaryConversionNames, binaryConversionTypes)
				})
			}
		}
		binaryConversionBoth(t, p, b, c, referenceSelect("SELECT "+binaryConversionProjection+" FROM bc_metadata WHERE id=1", binaryConversionNames, binaryConversionTypes, [][]any{{"é", "é", "\xe9", "e9", "\xe9"}}))
	})
}

func TestReferenceBinaryConversionLifecycle(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE bc_control(id integer,b bytea,s text,CONSTRAINT bc_control_check CHECK(decode(encode(b,'hex'),'hex')=b))", 0),
			referenceExec(`INSERT INTO bc_control VALUES(1,'\xe9','é'),(2,'',''),(3,NULL,NULL)`, 3),
			referenceExec("CREATE VIEW bc_control_view AS SELECT id,convert_from(b,'LATIN1') AS s,convert_to(s,'LATIN1') AS b,encode(b,'escape') AS e FROM bc_control", 0),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		want := referenceSelect("SELECT s,b,e FROM bc_control_view ORDER BY id", []string{"s", "b", "e"}, []string{"TEXT", "BYTEA", "TEXT"}, [][]any{{"é", "\xe9", `\351`}, {"", "", ""}, {nil, nil, nil}})
		want.checkMetadata = binaryConversionViewMetadata(b.pg, want.columns, want.types)
		binaryConversionBoth(t, p, b, c, want)
		if b.pg {
			return
		} // Native controls have no PostgreSQL API equivalent.
		held, err := c.QueryContext(p.ctx, want.query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ct, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, clause := range []string{"convert_from(", "convert_to(", "encode(", "decode("} {
			if !strings.Contains(dump.String(), clause) {
				t.Fatalf("dump omits %s: %s", clause, dump.String())
			}
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		binaryConversionBoth(t, p, b, c, want)
		readFailure := errors.New("binary conversion restore reader failed")
		if err := stuntgres.Restore(p.ctx, p.native, catalogControlFailingReader{readFailure}); !errors.Is(err, readFailure) {
			t.Fatal(err)
		}
		binaryConversionBoth(t, p, b, c, want)
		duringRead, cancelRead := context.WithCancel(p.ctx)
		if err := stuntgres.Restore(duringRead, p.native, &catalogControlCancelReader{cancel: cancelRead}); !errors.Is(err, context.Canceled) {
			cancelRead()
			t.Fatal(err)
		}
		cancelRead()
		binaryConversionBoth(t, p, b, c, want)
		if !held.Next() {
			t.Fatal(held.Err())
		}
		var s, e string
		var raw []byte
		if err := held.Scan(&s, &raw, &e); err != nil || s != "é" || string(raw) != "\xe9" || e != `\351` {
			t.Fatal(s, raw, e, err)
		}
		if ct[0].DatabaseTypeName() != "TEXT" || ct[1].DatabaseTypeName() != "BYTEA" {
			t.Fatal("retained metadata changed")
		}
		held.Close()
		invalid := dump.String() + "\nCREATE TABLE bc_invalid(v bytea); INSERT INTO bc_invalid VALUES(decode('0q','hex'));"
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(invalid)); err == nil {
			t.Fatal("bad Restore accepted")
		}
		binaryConversionBoth(t, p, b, c, want)
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		binaryConversionBoth(t, p, b, c, want)
		if p.postgres != nil {
			// Replay actual emitted SQL, not a hand-written approximation of Dump.
			// This native backend phase owns its replay objects. A defer (not
			// test-level Cleanup) removes them before the ordinary PostgreSQL
			// backend body starts, including partial replay/assertion failures.
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := p.postgres.ExecContext(cleanup, "DROP VIEW IF EXISTS bc_control_view; DROP TABLE IF EXISTS bc_control"); err != nil {
					t.Errorf("binary conversion replay cleanup: %v", err)
				}
			}()
			_, err := p.postgres.ExecContext(p.ctx, "DROP VIEW IF EXISTS bc_control_view; DROP TABLE IF EXISTS bc_control")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			replayed := want
			replayed.checkMetadata = binaryConversionViewMetadata(true, want.columns, want.types)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, replayed)
		}
	})
}

func TestReferenceBinaryConversionCancellation(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_cancel(b bytea,s text)", 0))
		for _, tc := range []struct {
			q, typ    string
			arg, want any
		}{
			{"SELECT encode($1,'hex') AS v", "TEXT", []byte("abc"), "616263"},
			{"SELECT decode($1,'hex') AS v", "BYTEA", "616263", "abc"},
			{"SELECT convert($1,'LATIN1','UTF8') AS v", "BYTEA", []byte{0xe9}, "é"},
			{"SELECT convert_from($1,'LATIN1') AS v", "TEXT", []byte{0xe9}, "é"},
			{"SELECT convert_to($1,'LATIN1') AS v", "BYTEA", "é", "\xe9"},
		} {
			stmt, err := b.db.PrepareContext(p.ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			column := "b"
			if tc.typ == "TEXT" {
				column = "s"
			}
			writeSQL := "INSERT INTO bc_cancel(" + column + ") " + strings.TrimSuffix(tc.q, " AS v")
			write, err := b.db.PrepareContext(p.ctx, writeSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer write.Close()
			for _, deadline := range []bool{false, true} {
				var ctx context.Context
				var cancel context.CancelFunc
				want := context.Canceled
				if deadline {
					ctx, cancel = context.WithDeadline(p.ctx, time.Unix(0, 0))
					want = context.DeadlineExceeded
				} else {
					ctx, cancel = context.WithCancel(p.ctx)
					cancel()
				}
				defer cancel()
				rows, err := stmt.QueryContext(ctx, tc.arg)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled call returned cursor")
				}
				if !errors.Is(err, want) {
					t.Fatal(err)
				}
				direct, err := b.db.QueryContext(ctx, tc.q, tc.arg)
				if direct != nil {
					direct.Close()
					t.Fatal("canceled direct call returned cursor")
				}
				if !errors.Is(err, want) {
					t.Fatal(err)
				}
				result, err := b.db.ExecContext(ctx, writeSQL, tc.arg)
				if result != nil || !errors.Is(err, want) {
					t.Fatal(result, err)
				}
				result, err = write.ExecContext(ctx, tc.arg)
				if result != nil || !errors.Is(err, want) {
					t.Fatal(result, err)
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT b,s FROM bc_cancel", []string{"b", "s"}, []string{"BYTEA", "TEXT"}, nil))
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: tc.q}, b.recorder, referenceSelect(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.arg))
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: write, query: writeSQL}, b.recorder, referenceExec(writeSQL, 1, tc.arg))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT "+column+" AS v FROM bc_cancel", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DELETE FROM bc_cancel", 1))
		}
	})
}
