package reference_test

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimestampDeclarationsOccurrenceSizingAndOwnership(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			create, e := db.PrepareContext(ctx, `CREATE TABLE t(v TIMESTAMP,p0 TIMESTAMP(0),p1 TIMESTAMP(1),p2 TIMESTAMP(2),p3 TIMESTAMP(3),p4 TIMESTAMP(4),p5 TIMESTAMP(5),p6 TIMESTAMP(6),q "timestamp"(0))`)
			if e != nil {
				t.Fatal(e)
			}
			_, e = create.ExecContext(ctx)
			create.Close()
			if e != nil {
				t.Fatal(e)
			}
			stmt, e := db.PrepareContext(ctx, "INSERT INTO t VALUES($1,$1,$1,$1,$1,$1,$1,$1,$1)")
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			raw := []byte("2000-01-01 00:00:00.123456")
			if _, e = stmt.ExecContext(ctx, raw); e != nil {
				t.Fatal(e)
			}
			raw[0] = 'X'
			names := []string{"v", "p0", "p1", "p2", "p3", "p4", "p5", "p6", "q"}
			types := []string{}
			want := []any{}
			for _, ns := range []int{123456000, 0, 100000000, 120000000, 123000000, 123500000, 123460000, 123456000, 0} {
				types = append(types, "TIMESTAMP")
				want = append(want, time.Date(2000, 1, 1, 0, 0, 0, ns, time.UTC))
			}
			referenceTimestampQuery(t, ctx, db, backend.Recorder, "SELECT * FROM t", names, types, [][]any{want})
			referenceTimestampExec(t, ctx, db, "CREATE TABLE clone(LIKE t)")
			referenceTimestampExec(t, ctx, db, "INSERT INTO clone VALUES($1,$1,$1,$1,$1,$1,$1,$1,$1)", "2000-01-01 00:00:00.123456")
			referenceTimestampQuery(t, ctx, db, backend.Recorder, "SELECT * FROM clone", names, types, [][]any{want})
			// Accepted integer lexical families belong to Iconst even in precision.
			referenceTimestampExec(t, ctx, db, "CREATE TABLE radix(v TIMESTAMP(0x3),w TIMESTAMP(0b11),x TIMESTAMP(0o3))")
			referenceTimestampExec(t, ctx, db, "INSERT INTO radix VALUES('epoch','epoch','epoch')")
		})
	}
}

func TestReferenceTimestampPublicDescriptionAndPreparedRelatives(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			// database/sql metadata does not expose typmods; native Dump and engine
			// invariant tests cover them rather than pretending ColumnType has that API.
			referenceTimestampExec(t, ctx, db, "CREATE TABLE t(v TIMESTAMP(3) NOT NULL)")
			referenceTimestampExec(t, ctx, db, "INSERT INTO t VALUES('epoch')")
			rows, e := db.QueryContext(ctx, "SELECT v,TIMESTAMP(0) 'epoch' FROM t")
			if e != nil {
				t.Fatal(e)
			}
			types, e := rows.ColumnTypes()
			rows.Close()
			if e != nil {
				t.Fatal(e)
			}
			fixture.TemporalMetadata(t, backend.PG, types)
			n, k := types[0].Nullable()
			if (!backend.PG && (!k || n)) || (backend.PG && k) {
				t.Fatal(types[0])
			}
			_, known := types[1].Nullable()
			if known {
				t.Fatal("invented scalar nullability")
			}
			if types[1].ScanType() != reflect.TypeFor[time.Time]() {
				t.Fatal(types[1])
			}
			conn, e := db.Conn(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			for _, special := range []string{"today", "tomorrow", "yesterday"} {
				stmt, e := conn.PrepareContext(ctx, "SELECT TIMESTAMP '"+special+"' FROM t")
				if e != nil {
					t.Fatal(e)
				}
				var initial, later time.Time
				if e = stmt.QueryRowContext(ctx).Scan(&initial); e != nil {
					t.Fatal(e)
				}
				tx, e := conn.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				e = stmt.QueryRowContext(ctx).Scan(&later)
				tx.Rollback()
				stmt.Close()
				if e != nil || !later.Equal(initial) || initial.Hour() != 0 || initial.Nanosecond() != 0 {
					t.Fatal(special, initial, later, e)
				}
			}
		})
	}
}

func TestReferenceTimestampSettingArgumentsUseScopeEnvironmentWithoutNewCasts(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			for _, tc := range []struct{ input, code, message string }{
				{"nonsense", "22007", `invalid input syntax for type timestamp: "nonsense"`},
				{"epoch", "42804", "incompatible source and destination types"},
				{"now", "42804", "incompatible source and destination types"},
			} {
				for _, q := range []string{"SELECT current_setting(TIMESTAMP '" + tc.input + "')", "SELECT set_config('application_name',TIMESTAMP '" + tc.input + "',FALSE)"} {
					want := stuntgres.Error{Code: tc.code, Message: tc.message}
					if tc.code == "22007" {
						want.Position = strconv.Itoa(strings.LastIndex(q, "'"+tc.input+"'") + 1)
					}
					if backend.PG && tc.code == "42804" {
						want.Code, want.Position = "42883", "8"
						want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						want.Message = "function current_setting(timestamp without time zone) does not exist"
						if strings.Contains(q, "set_config") {
							want.Message = "function set_config(unknown, timestamp without time zone, boolean) does not exist"
						}
					}
					rows, err := db.QueryContext(ctx, q)
					if rows != nil {
						rows.Close()
						t.Fatal("failed binding exposed cursor")
					}
					fixture.TemporalDiagnostic(t, err, want)
					stmt, err := db.PrepareContext(ctx, q)
					if stmt != nil {
						stmt.Close()
						t.Fatal("failed binding exposed handle")
					}
					fixture.TemporalDiagnostic(t, err, want)
				}
			}
		})
	}
}

func TestReferenceTimestampInvalidDeclarationNeverPublishesAndAbortsTransaction(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			q := `CREATE TABLE bad (v "timestamp"(-1))`
			want := stuntgres.Error{Code: "22023", Message: "TIMESTAMP(-1) precision must not be negative", Position: "21"}
			_, err := db.ExecContext(ctx, q)
			fixture.TemporalDiagnostic(t, err, want)
			utilityTx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = utilityTx.ExecContext(ctx, q)
			if backend.PG {
				fixture.TemporalDiagnostic(t, err, want)
				_, err = utilityTx.ExecContext(ctx, "SELECT v FROM bad")
				fixture.TemporalDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			} else {
				fixture.TemporalDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
			}
			if err = utilityTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceTimestampExec(t, ctx, db, "CREATE TABLE t(v TIMESTAMP)")
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			invalid := "INSERT INTO t VALUES('nonsense')"
			_, err = tx.ExecContext(ctx, invalid)
			fixture.TemporalDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: strconv.Itoa(strings.Index(invalid, "'") + 1)})
			_, err = tx.ExecContext(ctx, "INSERT INTO t VALUES('now')")
			fixture.TemporalDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			rows, err := db.QueryContext(ctx, "SELECT v FROM bad")
			if rows != nil {
				rows.Close()
				t.Fatal("rejected declaration published table")
			}
			fixture.TemporalDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
			referenceTimestampQuery(t, ctx, db, backend.Recorder, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, nil)
		})
	}
}
