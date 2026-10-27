package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const diagnosticsLifeDefinition = "CREATE FUNCTION dg_life(depth integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; initial text; child text:=''; code text; message text; BEGIN GET DIAGNOSTICS n=ROW_COUNT; initial:=n::text || ':' || found::text; PERFORM 1 FROM (VALUES(1),(2),(3)) q(i) WHERE i<=depth; IF depth>1 THEN child:='/' || dg_life(depth-1); END IF; BEGIN RAISE EXCEPTION 'life error'; EXCEPTION WHEN OTHERS THEN GET STACKED DIAGNOSTICS code=RETURNED_SQLSTATE,message=MESSAGE_TEXT; END; GET DIAGNOSTICS n=ROW_COUNT; RETURN initial || '|' || n::text || ':' || found::text || '|' || code || ':' || message || child; END$$"

func diagnosticsLifeWant(depth int) string {
	prefix := "0:false|"
	if depth == 0 {
		return prefix + "0:false|P0001:life error"
	}
	if depth == 1 {
		return prefix + "1:true|P0001:life error"
	}
	if depth == 2 {
		return prefix + "2:true|P0001:life error/" + diagnosticsLifeWant(1)
	}
	// Recursive reentry of the same simple expression uses PostgreSQL's SQL
	// evaluation fallback; its one outer row replaces the nested frame's count.
	return prefix + "3:true|P0001:life error/" + strings.Replace(diagnosticsLifeWant(2), "|2:true|", "|1:true|", 1)
}

func diagnosticsLifeCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held *sql.Stmt) {
	t.Helper()
	q := "SELECT dg_life($1::integer) AS v"
	for _, depth := range []int{3, 0, 1, 2, 3} {
		sqlExecutionValue(t, p, b, c, q, "TEXT", diagnosticsLifeWant(depth), depth)
		if held != nil {
			s := sqlExecutionStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{diagnosticsLifeWant(depth)}}, depth)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
		}
	}
	sqlExecutionValue(t, p, b, c, "SELECT dg_life_context() AS v", "TEXT", "PL/pgSQL function dg_life_context() line 1 at GET DIAGNOSTICS")
}

func TestReferencePLpgSQLDiagnosticsLifecycle(t *testing.T) {
	diagnosticsDynamicLifecycle(t)
	t.Run("held_replay_and_owned_outputs", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, diagnosticsLifeDefinition, "CREATE FUNCTION dg_life_context() RETURNS text LANGUAGE plpgsql AS $$DECLARE c text; BEGIN GET DIAGNOSTICS c=PG_CONTEXT; RETURN c; END$$")
			q := "SELECT dg_life($1::integer) AS v"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			diagnosticsLifeCheck(t, p, b, c, held)
			var text string
			if err := c.QueryRowContext(p.Ctx, "SELECT dg_life_context()").Scan(&text); err != nil {
				t.Fatal(err)
			}
			if b.PG {
				return
			} // Native controls have no oracle equivalent.
			retained, err := c.QueryContext(p.Ctx, "SELECT dg_life_context() AS v")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			columns, err := retained.Columns()
			if err != nil {
				t.Fatal(err)
			}
			descriptors, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			sqlExecutionMetadata(b, []string{"v"}, []string{"TEXT"})(t, retained)
			var dump []byte
			for range 2 {
				dump = parametersNativeReplayRound(t, p, b, c, func(conn *sql.Conn, backend fixture.Backend) { diagnosticsLifeCheck(t, p, backend, conn, held) })
			}
			if !retained.Next() {
				t.Fatal("retained row lost", retained.Err())
			}
			var old string
			if err := retained.Scan(&old); err != nil || old != text {
				t.Fatal(old, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained rows cardinality", retained.Err())
			}
			retained.Close()
			if len(columns) != 1 || columns[0] != "v" || len(descriptors) != 1 || descriptors[0].Name() != "v" || descriptors[0].DatabaseTypeName() != "TEXT" {
				t.Fatal("retained descriptor changed", columns, descriptors)
			}
			if text != "PL/pgSQL function dg_life_context() line 1 at GET DIAGNOSTICS" {
				t.Fatal("retained text changed", text)
			}
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			raw := sql.OpenDB(connector)
			defer raw.Close()
			recorded, rec := fixture.RecordedNativePool(t, connector)
			fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE TYPE dg_pressure AS (v integer)", 0))
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			clone, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer clone.Close()
				diagnosticsLifeCheck(t, p, fixture.Backend{DB: recorded, Recorder: rec}, clone, nil)
			}()
			bad := string(dump) + "; CREATE TABLE dg_restore_prefix(i integer); CREATE FUNCTION dg_restore_bad() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n bigint; BEGIN GET DIAGNOSTICS n=bogus; RETURN 1; END$$;"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid diagnostics Restore accepted")
			}
			diagnosticsLifeCheck(t, p, b, c, held)
			sqlExecutionCount(t, p, b, c, "pg_class WHERE relname='dg_restore_prefix'", 0)
			sqlExecutionCount(t, p, b, c, "pg_proc WHERE proname='dg_restore_bad'", 0)
			ctx, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(ctx, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(dump), Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled Restore", err)
			}
			diagnosticsLifeCheck(t, p, b, c, held)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump, after.Bytes()) {
				t.Fatal("failed Restore changed catalog/data")
			}
		})
	})
	t.Run("routine_oid_identity", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_oid() RETURNS oid LANGUAGE plpgsql AS $$DECLARE n oid; BEGIN GET DIAGNOSTICS n=PG_ROUTINE_OID; RETURN n; END$$", "CREATE FUNCTION dg_other_oid() RETURNS oid LANGUAGE plpgsql AS $$DECLARE n oid; BEGIN GET CURRENT DIAGNOSTICS n=PG_ROUTINE_OID; RETURN n; END$$")
			for _, name := range []string{"dg_oid", "dg_other_oid"} {
				sqlExecutionValue(t, p, b, c, "SELECT "+name+"() = oid AS v FROM pg_proc WHERE proname='"+name+"'", "BOOL", true)
			}
			sqlExecutionValue(t, p, b, c, "SELECT dg_oid()<>dg_other_oid() AS v", "BOOL", true)
			// Inspect the OID return codec as well as relational identity. The
			// allocator value itself is deliberately not a cross-server golden.
			for _, prepared := range []bool{false, true} {
				q := "SELECT dg_oid() AS v"
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := c.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer rows.Close()
					cols, e := rows.Columns()
					if e != nil || !reflect.DeepEqual(cols, []string{"v"}) {
						t.Fatal(cols, e)
					}
					types, e := rows.ColumnTypes()
					if e != nil || len(types) != 1 {
						t.Fatal(types, e)
					}
					col := types[0]
					scan := reflect.TypeFor[int64]()
					if b.PG {
						scan = reflect.TypeFor[any]()
					}
					l, lok := col.Length()
					n, nok := col.Nullable()
					precision, scale, dok := col.DecimalSize()
					if col.Name() != "v" || col.DatabaseTypeName() != "OID" || col.ScanType() != scan || l != 0 || lok || n || nok || precision != 0 || scale != 0 || dok {
						t.Fatalf("OID metadata=%s/%s scan=%v length=%d/%t nullable=%t/%t decimal=%d,%d/%t", col.Name(), col.DatabaseTypeName(), col.ScanType(), l, lok, n, nok, precision, scale, dok)
					}
					if !rows.Next() {
						t.Fatal("OID row absent", rows.Err())
					}
					var oid any
					if err := rows.Scan(&oid); err != nil {
						t.Fatal(err)
					}
					if b.PG {
						switch oid.(type) {
						case []byte, string:
						default:
							t.Fatalf("pq OID=%T", oid)
						}
					} else if _, ok := oid.(int64); !ok {
						t.Fatalf("native OID=%T", oid)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("OID cardinality", rows.Err())
					}
				}()
			}
			sqlExecutionValue(t, p, b, c, "SELECT dg_oid()>0::oid AS v", "BOOL", true)
			userOperatorExec(t, p, b, c, "CREATE TABLE dg_oid_saved(original oid)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dg_oid_saved SELECT dg_oid()", 1))
			userOperatorExec(t, p, b, c, "ALTER FUNCTION dg_oid() RENAME TO dg_oid_renamed")
			sqlExecutionValue(t, p, b, c, "SELECT dg_oid_renamed()=oid AS v FROM pg_proc WHERE proname='dg_oid_renamed'", "BOOL", true)
			sqlExecutionValue(t, p, b, c, "SELECT dg_oid_renamed()=original AS v FROM dg_oid_saved", "BOOL", true)
			sqlExecutionValue(t, p, b, c, "SELECT dg_oid_renamed()<>dg_other_oid() AS v", "BOOL", true)
		})
	})
	for _, heldRoute := range []bool{false, true} {
		name := "direct_cancel"
		if heldRoute {
			name = "held_cancel"
		}
		t.Run(name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE dg_cancel_marks(i integer)", "CREATE FUNCTION dg_cancel(slow boolean) RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; stack text; BEGIN GET DIAGNOSTICS n=ROW_COUNT,stack=PG_CONTEXT; INSERT INTO dg_cancel_marks VALUES(9); IF slow THEN PERFORM pg_sleep(30); END IF; RETURN n::text || '|' || stack; END$$")
				q := "SELECT dg_cancel($1::boolean) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				var executor fixture.Executor = c
				if heldRoute {
					executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				if !b.PG {
					for _, expired := range []bool{false, true} {
						ctx, cancel := context.WithCancel(p.Ctx)
						want := context.Canceled
						if expired {
							cancel()
							ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
							want = context.DeadlineExceeded
						} else {
							cancel()
						}
						var rows *sql.Rows
						var err error
						if heldRoute {
							rows, err = stmt.QueryContext(ctx, false)
						} else {
							rows, err = c.QueryContext(ctx, q, false)
						}
						if rows != nil {
							rows.Close()
							t.Fatal("pre-canceled query exposed rows")
						}
						if !errors.Is(err, want) {
							t.Fatal("ordinary context error", err, want)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("context error has SQLSTATE", err)
						}
						<-ctx.Done()
						if !errors.Is(ctx.Err(), want) {
							t.Fatal(ctx.Err())
						}
						cancel()
						sqlExecutionCount(t, p, b, c, "dg_cancel_marks", 0)
					}
				}
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT pg_sleep(30)\"\nPL/pgSQL function dg_cancel(boolean) line 1 at PERFORM", nil, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if heldRoute {
						rows, err = stmt.QueryContext(ctx, true)
					} else {
						rows, err = c.QueryContext(ctx, q, true)
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				sqlExecutionCount(t, p, b, c, "dg_cancel_marks", 0)
				s := sqlExecutionStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{"0|PL/pgSQL function dg_cancel(boolean) line 1 at GET DIAGNOSTICS"}}, false)
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				sqlExecutionCount(t, p, b, c, "dg_cancel_marks", 1)
			})
		})
	}
}
