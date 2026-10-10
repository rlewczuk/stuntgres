package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"testing/iotest"
)

func sizeStored(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, controls bool) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE size_saved(id integer,s text,i bigint,n numeric,b bigint DEFAULT pg_size_bytes('1 MB'),p text DEFAULT pg_size_pretty(1024::bigint),q text DEFAULT pg_size_pretty(10.5000::numeric),z text DEFAULT pg_size_pretty(NULL::bigint),w text DEFAULT pg_size_pretty(NULL::numeric),g text GENERATED ALWAYS AS (pg_size_pretty(i)) STORED,CONSTRAINT size_saved_check CHECK(pg_size_bytes(s)=1048576))", 0))
	run(referenceExec("CREATE INDEX size_pretty_index ON size_saved ((pg_size_pretty(i)))", 0))
	run(referenceExec("CREATE INDEX size_bytes_index ON size_saved ((pg_size_bytes(s)))", 0))
	run(referenceExec("CREATE VIEW size_saved_view AS SELECT id,pg_size_pretty(1024::bigint) AS p,pg_size_pretty(10.5000::numeric) AS q,pg_size_pretty(NULL::bigint) AS z,pg_size_pretty(NULL::numeric) AS w,pg_size_bytes(s) AS b,pg_size_pretty(i) AS g FROM size_saved", 0))
	for _, pretty := range []string{"false", "true"} {
		for _, v := range []struct {
			number, typ string
			want        any
		}{
			{"5", "INT8", int64(1048576)}, {"6", "TEXT", "1024 bytes"},
			{"7", "TEXT", "10.5000 bytes"}, {"8", "TEXT", nil}, {"9", "TEXT", nil}, {"10", "TEXT", "1024 bytes"},
		} {
			q := "SELECT pg_get_expr(d.adbin,d.adrelid," + pretty + ") AS expression FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class r ON d.adrelid=r.oid WHERE r.relname='size_saved' AND d.adnum=" + v.number
			t.Run("deparse/"+pretty+"/"+v.number, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
						rows, closeQuery, err := transactionQuery(p.ctx, c, q, prepared)
						if err != nil {
							t.Fatal(err)
						}
						defer closeQuery()
						ct, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						// pg_get_expr's catalog-query descriptor is not a size
						// result descriptor: native exposes known nullable TEXT.
						sizeColumnMetadata(t, ct, []string{"expression"}, []string{"TEXT"}, !b.pg, !b.pg)
						if !rows.Next() {
							t.Fatal("missing deparse expression", rows.Err())
						}
						var expression string
						if err := rows.Scan(&expression); err != nil {
							t.Fatal(err)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("extra deparse expression/error", rows.Err())
						}
						if err := closeQuery(); err != nil {
							t.Fatal(err)
						}
						// Replay the actual compact/pretty expression. This proves
						// selected small BIGINT and typed NULL remain resolvable,
						// without treating incidental parentheses as a contract.
						replay := referenceSelect("SELECT "+expression+" AS value FROM (SELECT 1024::bigint AS i) AS source", []string{"value"}, []string{v.typ}, [][]any{{v.want}})
						sizeBoth(t, p, b, c, replay, false)
					})
				}
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		s := referenceExec("INSERT INTO size_saved(id,s,i,n) VALUES($1,$2,$3,$4)", 1, int64(1), "1 MB", int64(1024), "10.5000")
		s.prepared = prepared
		run(s)
	}
	names, types := []string{"b", "p", "q", "z", "w", "g"}, []string{"INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
	want := []any{int64(1048576), "1024 bytes", "10.5000 bytes", nil, nil, "1024 bytes"}
	assertSaved := func(x referenceExecutor, recorder *referenceRecorder) {
		t.Helper()
		referenceRunStep(t, p.ctx, x, recorder, referenceSelect("SELECT b,p,q,z,w,g FROM size_saved ORDER BY id", names, types, [][]any{want, want}))
		referenceRunStep(t, p.ctx, x, recorder, referenceSelect("SELECT b,p,q,z,w,g FROM size_saved_view ORDER BY id", names, types, [][]any{want, want}))
	}
	assertSaved(c, b.recorder)
	if !controls || b.pg {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	rows, err := p.native.QueryContext(p.ctx, "SELECT pg_size_bytes(s),pg_size_pretty(i),pg_size_pretty(n) FROM size_saved ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	sizeColumns(t, rows, []string{"pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT8", "TEXT", "TEXT"})
	retainedColumns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	run(referenceExec("INSERT INTO size_saved(id,s,i,n) VALUES(99,'1 MB',10752,10240)", 1))
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	sizeColumns(t, rows, []string{"pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT8", "TEXT", "TEXT"})
	for range 2 {
		if !rows.Next() {
			t.Fatal("missing retained row", rows.Err())
		}
		var n int64
		var big, num string
		if err := rows.Scan(&n, &big, &num); err != nil {
			t.Fatal(err)
		}
		if n != 1048576 || big != "1024 bytes" || num != "10.5000 bytes" {
			t.Fatal(n, big, num)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained extra row/error", rows.Err())
	}
	sizeColumnMetadata(t, retainedColumns, []string{"pg_size_bytes", "pg_size_pretty", "pg_size_pretty"}, []string{"INT8", "TEXT", "TEXT"}, false, false)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	assertSaved(c, b.recorder)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("non-deterministic redump\nfirst: %s\nsecond: %s", dump.String(), redump.String())
	}
	// Re-evaluate destination-bound defaults/generated/view/index expressions.
	run(referenceExec("INSERT INTO size_saved(id,s,i,n) VALUES(2,'1 MB',1024,10.5000)", 1))
	run(referenceSelect("SELECT b,p,q,z,w,g FROM size_saved WHERE id=2", names, types, [][]any{want}))
	for _, v := range []struct {
		sql        string
		diagnostic referenceDiagnostic
	}{
		{"CREATE TABLE size_late(v bigint); INSERT INTO size_late VALUES(pg_size_bytes('1 MB')); INSERT INTO size_late VALUES(pg_size_bytes('1 KiB'));", sizeInvalid("1 KiB", "KiB")},
		{"CREATE TABLE size_late(v bigint); INSERT INTO size_late VALUES(pg_size_bytes('1 MB')); INSERT INTO size_late VALUES(pg_size_bytes('8192PB'));", sizeDiagnostic("22003", "bigint out of range", "", "")},
	} {
		err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(v.sql))
		referenceAssertDiagnostic(t, err, &v.diagnostic)
		run(referenceSelect("SELECT count(*) FROM size_saved", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(referenceErrorDiagnostic("SELECT v FROM size_late", sizeDiagnostic("42P01", `relation "size_late" does not exist`, "", "15")))
	}
	parameterSQL := "CREATE TABLE size_late(v bigint); INSERT INTO size_late VALUES(pg_size_bytes($1));"
	d := sizeDiagnostic("42P02", "parameters are not allowed in restore", "", referencePosition(strings.Index(parameterSQL, "$1")+1))
	referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(parameterSQL)), &d)
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore", err)
	}
	ioFailure := errors.New("size Restore reader failure")
	if err := stuntgres.Restore(p.ctx, p.native, iotest.ErrReader(ioFailure)); !errors.Is(err, ioFailure) {
		t.Fatal("I/O failed Restore", err)
	}
	run(referenceSelect("SELECT count(*) FROM size_saved", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	if p.postgres != nil {
		// Native executes first in the completed scenario. Replay its ACTUAL SQL
		// before the PostgreSQL branch creates its independently specified objects.
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("actual dump replay: %v\n%s", err, dump.String())
		}
		assertSaved(p.postgres, p.postgresRecord)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO size_saved(id,s,i,n) VALUES(2,'1 MB',1024,10.5000)", 1))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT b,p,q,z,w,g FROM size_saved WHERE id=2", names, types, [][]any{want}))
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	// Recursive parameter-free Restore INSERT admission, including pretty's
	// selected small BIGINT and typed numeric NULL, not only materialized dumps.
	valid := "CREATE TABLE size_replay(b bigint,p text,q text); INSERT INTO size_replay VALUES(pg_size_bytes(pg_size_pretty(pg_size_bytes('1 MB'))),pg_size_pretty(1024::bigint),pg_size_pretty(NULL::numeric));"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	run(referenceSelect("SELECT b,p,q FROM size_replay", []string{"b", "p", "q"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1048576), "1024 bytes", nil}}))
}

func TestReferenceSizeStored(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) { sizeStored(t, p, b, c, false) })
}

func TestReferenceSizeControls(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) { sizeStored(t, p, b, c, true) })
}

func TestReferenceSizeFeatureSmoke(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		s := referenceSelect("SELECT pg_size_bytes('1 MB'),pg_size_pretty(1024::bigint),pg_size_pretty(-10752::bigint),pg_size_pretty(10.5000::numeric),pg_size_pretty(11528652096115048448::numeric),pg_size_pretty(NULL::numeric)", []string{"pg_size_bytes", "pg_size_pretty", "pg_size_pretty", "pg_size_pretty", "pg_size_pretty", "pg_size_pretty"}, []string{"INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1048576), "1024 bytes", "-11 kB", "10.5000 bytes", "10240 PB", nil}})
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) { sizeColumns(t, rows, s.columns, s.types) }
		sizeBoth(t, p, b, c, s, false)
		q := "SELECT pg_size_bytes($1)"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, v := range []struct{ input, want any }{{"1 MB", int64(1048576)}, {nil, nil}, {"1 MB", int64(1048576)}} {
			referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"pg_size_bytes"}, []string{"INT8"}, [][]any{{v.want}}, v.input))
			if v.input == "1 MB" {
				s := referenceErrorDiagnostic(q, sizeInvalid("1 KiB", "KiB"), "1 KiB")
				s.selectRows, s.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, x, b.recorder, s)
			}
		}
		sizeAtomicity(t, p, b, c)
		sizeStored(t, p, b, c, true)
	})
}
