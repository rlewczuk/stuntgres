package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINCompoundControls(t *testing.T) {
	for _, presentation := range []bool{false, true} {
		name := "original"
		if presentation {
			name = "relocated"
		}
		t.Run(name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "compound_controls_schema")
			var emitted string
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				seed := func() {
					compoundControlSeed(run)
					if presentation {
						run(fixture.Exec("CREATE SCHEMA compound_controls_schema", 0))
						run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA compound_controls_schema", 0))
						run(fixture.Exec("ALTER OPERATOR CLASS compound_controls_schema.bit_ops USING gin RENAME TO compound_saved_bit", 0))
						run(fixture.Exec("ALTER OPERATOR FAMILY compound_controls_schema.enum_ops USING gin RENAME TO compound_saved_enum_family", 0))
					}
				}
				seed()
				compoundControlCheck(b, run)
				// Compatible builtin signatures and datums rebind after replacement;
				// concrete enum parameters/results and captured enum literals retain
				// their distinct existing generation/lifetime fences.
				const query = "SELECT id FROM compound_saved WHERE m >= $1::money AND b >= B'100' ORDER BY id"
				held := classHeld(t, p, conn, query)
				wantHeld := ginIDs(query, []int64{1, 4}, "0.01")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)
				if b.PG {
					// Execute the real native SQL bytes, never a reconstructed equivalent.
					// If native fails before Dump during red discovery, still execute the
					// independent PG assertions, without claiming that a Dump was replayed.
					run(fixture.Exec("DROP TABLE compound_saved", 0))
					run(fixture.Exec("DROP TYPE compound_saved_enum", 0))
					run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
					if presentation {
						run(fixture.Exec("DROP SCHEMA compound_controls_schema", 0))
					}
					if emitted != "" {
						ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
						if _, err := conn.ExecContext(ctx, emitted); err != nil {
							t.Fatalf("PostgreSQL rejected actual native compound GIN Dump: %v\n%s", err, emitted)
						}
					} else {
						seed()
					}
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = dump.String()
					// Dump reads committed state, not a caller's private index/row graph.
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO compound_saved VALUES(77,'111','10','m','0.02','{2}','cat:1')", 1))
					run(fixture.Exec("CREATE INDEX compound_private_i ON compound_saved USING gin(m)", 0))
					var committed bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil || !bytes.Equal(dump.Bytes(), committed.Bytes()) {
						t.Fatalf("Dump included private transaction state: %v", err)
					}
					run(fixture.Exec("ROLLBACK", 0))
					compoundControlCheck(b, run)

					enumParameter := classHeld(t, p, conn, "SELECT $1::compound_saved_enum AS v")
					enumResult := classHeld(t, p, conn, "SELECT e AS v FROM compound_saved ORDER BY id")
					const enumLiteralQuery = "SELECT id FROM compound_saved WHERE m >= $1::money AND e >= 'a'::compound_saved_enum ORDER BY id"
					enumLiteral := classHeld(t, p, conn, enumLiteralQuery)
					fixture.RunStep(t, p.Ctx, enumLiteral, b.Recorder, ginIDs(enumLiteralQuery, []int64{1, 4}, "0.01"))
					// Retain eager native rows AND their actual immutable descriptors for
					// every selected type across Reset/Restore, before consuming any row.
					type retained struct {
						rows     *sql.Rows
						metadata []*sql.ColumnType
						step     fixture.Step
					}
					var old []retained
					for _, s := range []fixture.Step{
						compoundValueStep(b, "SELECT id,b AS v FROM compound_saved ORDER BY id", "BIT", 3, [][]any{{int64(1), "100"}, {int64(2), "011"}, {int64(3), nil}, {int64(4), "100"}}),
						compoundValueStep(b, "SELECT id,v FROM compound_saved ORDER BY id", "VARBIT", -1, [][]any{{int64(1), "0"}, {int64(2), ""}, {int64(3), nil}, {int64(4), "00"}}),
						compoundValueStep(b, "SELECT id,e AS v FROM compound_saved ORDER BY id", "", 0, [][]any{{int64(1), "a"}, {int64(2), "q"}, {int64(3), nil}, {int64(4), "a"}}),
						compoundValueStep(b, "SELECT id,m AS v FROM compound_saved ORDER BY id", "MONEY", 0, [][]any{{int64(1), "$0.01"}, {int64(2), "-$0.01"}, {int64(3), nil}, {int64(4), "$0.01"}}),
					} {
						ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
						rows, err := conn.QueryContext(ctx, s.Query)
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						s.CheckMetadata(t, rows)
						metadata, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						old = append(old, retained{rows, metadata, s})
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
					run(amCount("SELECT count(*) FROM pg_type WHERE typname='compound_saved_enum'", 0))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('compound_saved','compound_saved_i','compound_saved_b','compound_saved_v','compound_saved_e','compound_saved_m','compound_private_i')", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT oid::bigint,amname,amhandler::oid::bigint FROM pg_am WHERE amname='gin'", []string{"oid", "amname", "amhandler"}, []string{"INT8", "NAME", "INT8"}, [][]any{{int64(2742), "gin", int64(333)}}))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					compoundControlCheck(b, run)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(query, nil, nil))
					literalRows, literalErr := enumLiteral.Statement.QueryContext(p.Ctx, "0.01")
					if literalRows != nil {
						literalRows.Close()
						t.Fatal("retained concrete-enum literal exposed rows after replacement")
					}
					fixture.TransactionOrdinary(t, literalErr, "stuntgres: catalog replaced before publication")
					classRoutes(t, p, b, conn, ginIDs(enumLiteralQuery, []int64{1, 4}, "0.01"))
					for _, stale := range []struct {
						held    fixture.HeldStatement
						args    []any
						message string
					}{
						{enumParameter, []any{"a"}, "cached plan must not change parameter type"},
						{enumResult, nil, "cached plan must not change result type"},
					} {
						rows, err := stale.held.Statement.QueryContext(p.Ctx, stale.args...)
						if rows != nil {
							rows.Close()
							t.Fatal("stale enum plan exposed rows")
						}
						d := fixture.ControlsReferenceDiagnostic("0A000", stale.message)
						fixture.AssertDiagnostic(t, err, &d)
					}
					for _, saved := range old {
						saved.step.CheckMetadata(t, saved.rows)
						fixture.ScalarAssertDescription(t, saved.metadata, saved.step.Columns, saved.step.Types)
						var got [][]any
						for saved.rows.Next() {
							var id, value any
							if err := saved.rows.Scan(&id, &value); err != nil {
								t.Fatal(err)
							}
							fixture.AssertRawValue(t, false, "INT4", id)
							if saved.step.CheckRaw == nil {
								fixture.AssertRawValue(t, false, saved.step.Types[1], value)
							} else {
								saved.step.CheckRaw(t, 1, len(got), value)
							}
							got = append(got, []any{fixture.Normalize(id), fixture.Normalize(value)})
						}
						if err := saved.rows.Err(); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got, saved.step.Rows) {
							t.Fatalf("replacement changed retained eager values: %#v want %#v", got, saved.step.Rows)
						}
						if err := saved.rows.Close(); err != nil {
							t.Fatal(err)
						}
						fixture.ScalarAssertDescription(t, saved.metadata, saved.step.Columns, saved.step.Types)
					}

					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/compound-independent")
					if err != nil {
						t.Fatal(err)
					}
					target := sql.OpenDB(connector)
					defer target.Close()
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					targetRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
					if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal("independent Driver Restore", err)
					}
					compoundControlCheck(b, targetRun)
					compoundControlPostDML(targetRun)
					compoundControlCheck(b, targetRun)
					compoundControlCheck(b, run) // Independent writes cannot alter source.
					var targetDump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, target, &targetDump); err != nil || !bytes.Equal(dump.Bytes(), targetDump.Bytes()) {
						t.Fatalf("independent canonical replay differs: %v", err)
					}

					schema := "public"
					if presentation {
						schema = "compound_controls_schema"
					}
					bad := dump.String() + "\nCREATE INDEX compound_restore_bad ON public.compound_saved USING gin(m " + schema + ".money_ops,b " + schema + ".money_ops);"
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
					d := fixture.ControlsReferenceDiagnostic("42804", `operator class "`+schema+`.money_ops" does not accept data type bit`)
					fixture.AssertDiagnostic(t, err, &d)
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
						t.Fatalf("late rejected index changed destination: %v", err)
					}
					canceled, cancel := context.WithCancel(p.Ctx)
					err = stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
					cancel()
					if !errors.Is(err, context.Canceled) {
						t.Fatal("Restore cancellation", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatalf("Restore cancellation fabricated SQLSTATE: %v", err)
					}
					after.Reset()
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
						t.Fatalf("canceled replacement changed destination: %v", err)
					}
					compoundControlCheck(b, run)
				}
				compoundControlCheck(b, run)
				// PG enum plans are invalid after dropping/recreating its real type too;
				// fresh typed plans independently observe the replayed destination.
				classRoutes(t, p, b, conn, ginIDs(query, []int64{1, 4}, "0.01"))
				if !b.PG {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)
				}
				compoundControlPostDML(run)
				compoundControlCheck(b, run)
			})
		})
	}
}
