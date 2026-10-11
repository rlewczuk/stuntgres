package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableCoreNativeControlRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec(`CREATE TABLE "source.dot"("i.dot" INT NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
	p.Step(t, fixture.Exec(`INSERT INTO "source.dot" VALUES(1,2,'source',FALSE)`, 1))
	p.Step(t, fixture.Prepared(fixture.Exec(`CREATE TABLE "clone.dot"(LIKE "source.dot")`, 0)))
	p.Step(t, fixture.Prepared(fixture.Exec(`INSERT INTO "clone.dot" VALUES($1,$2,$3,$4)`, 1, 7, int64(9223372036854775807), "O'Brien\n\\tail", true)))
	p.Step(t, fixture.Exec(`INSERT INTO "clone.dot" VALUES(8,NULL,NULL,NULL)`, 1))
	p.Step(t, fixture.Exec(`CREATE TABLE z()`, 0))
	p.Step(t, fixture.Prepared(fixture.Exec(`CREATE TABLE zl(LIKE z)`, 0)))
	p.Step(t, fixture.Prepared(fixture.Exec("CREATE TABLE typed(i INT4 NOT NULL,b INT8,s TEXT,f BOOL)", 0)))
	p.Step(t, fixture.Exec(`CREATE TABLE quoted_types(i "int4" NOT NULL,b "int8",s "text",f "bool")`, 0))
	for _, name := range []string{"typed", "quoted_types"} {
		p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO "+name+" VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien", true)))
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT * FROM "+name, []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien", true}})
			step.Prepared = prepared
			p.Step(t, step)
			for _, backend := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, step.Query, step.Columns, step.Types, []bool{false, true, true, true}, 1)
			}
		}
	}
	p.Step(t, fixture.Exec(`DROP TABLE "source.dot" RESTRICT`, 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	absentClone := fixture.ImplicitError(`SELECT * FROM "clone.dot"`, fixture.Diagnostic{Code: "42P01", Message: `relation "clone.dot" does not exist`, Position: "15"})
	absentClone.SelectRows = true
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, absentClone)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP TABLE "clone.dot",z,zl,typed,quoted_types`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL rejected native expanded dump", err)
		}
	}
	expected := fixture.Select(`SELECT * FROM "clone.dot" ORDER BY "i.dot"`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien\n\\tail", true}, {int64(8), nil, nil, nil}})
	p.Step(t, expected)
	p.Step(t, fixture.Prepared(expected))
	for _, b := range p.Backends() {
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, expected.Query, expected.Columns, expected.Types, []bool{false, true, true, true}, 2)
	}
	for _, name := range []string{"typed", "quoted_types"} {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT * FROM "+name, []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien", true}})
			step.Prepared = prepared
			p.Step(t, step)
			for _, backend := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, step.Query, step.Columns, step.Types, []bool{false, true, true, true}, 1)
			}
		}
	}
	for _, z := range []string{"z", "zl"} {
		p.Step(t, fixture.Select("SELECT * FROM "+z, []string{}, []string{}, nil))
		p.Step(t, fixture.Prepared(fixture.Select("SELECT * FROM "+z, []string{}, []string{}, nil)))
		for _, b := range p.Backends() {
			fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, "SELECT * FROM "+z, []string{}, []string{}, []bool{}, 0)
		}
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	p.Step(t, fixture.ImplicitError(`INSERT INTO "clone.dot" VALUES(NULL,NULL,NULL,NULL)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "i.dot" of relation "clone.dot" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null).", Schema: "public", Table: "clone.dot", Column: "i.dot"}))
	// Restore resolves LIKE against its private candidate, not incumbent z.
	for _, script := range []string{"CREATE TABLE replacement(LIKE missing)", "CREATE TABLE replacement(id INT,id TEXT)", "CREATE TABLE replacement(id no_such_type)", "CREATE TABLE replacement(LIKE z INCLUDING ALL)", "DROP TABLE z", "CREATE TABLE replacement(id INT); DROP TABLE replacement"} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted", script)
		}
		p.Step(t, expected)
		redump.Reset()
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("failed restore changed catalog", err)
		}
	}
	p.Step(t, fixture.Prepared(fixture.Exec(`ALTER TABLE "clone.dot" DROP CONSTRAINT "source.dot_i.dot_not_null"`, 0)))
	p.Step(t, expected)
	for _, b := range p.Backends() {
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, expected.Query, expected.Columns, expected.Types, []bool{true, true, true, true}, 2)
	}
	p.Step(t, fixture.Prepared(fixture.Exec(`INSERT INTO "clone.dot" VALUES(NULL,99,'released',FALSE)`, 1)))
	p.Step(t, fixture.Select(`SELECT * FROM "clone.dot" WHERE b=99`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{nil, int64(99), "released", false}}))
	absent := fixture.ImplicitError(`SELECT * FROM "source.dot"`, fixture.Diagnostic{Code: "42P01", Message: `relation "source.dot" does not exist`, Position: "15"})
	absent.SelectRows = true
	p.Step(t, absent)
}

func TestReferenceTableCoreAuthorizationAndOwnershipCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("table_owner_%v", prepared), fmt.Sprintf("table_reader_%v", prepared)
			fixture.RoleReferenceCleanup(t, []string{owner, reader})
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE USER "+owner, 0))
			p.Step(t, fixture.Exec("CREATE USER "+reader, 0))
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(11)", 1))
			if p.Postgres != nil {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			}
			for _, backend := range p.Backends() {
				db := backend.DB
				conn, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer conn.Close()
					record := backend.Recorder
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, record, s) }
					defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
					run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					run(fixture.Exec("CREATE TABLE source(i INT NOT NULL)", 0))
					run(fixture.Exec("INSERT INTO source VALUES(7)", 1))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
					denied := fixture.ImplicitError("CREATE TABLE denied(LIKE source)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
					denied.ErrorStage = "execute"
					run(denied)
					denied = fixture.ImplicitError("DROP TABLE source", fixture.Diagnostic{Code: "42501", Message: "must be owner of table source"})
					denied.ErrorStage = "execute"
					run(denied)
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					blocked := fixture.ImplicitError("DROP ROLE "+owner, fixture.Diagnostic{Code: "2BP01", Message: fmt.Sprintf("role %q cannot be dropped because some objects depend on it", owner), Detail: "owner of table source"})
					blocked.ErrorStage = "execute"
					run(blocked)
					run(fixture.Select("SELECT * FROM source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(fixture.Exec("DROP TABLE source CASCADE", 0))
					run(fixture.Exec("DROP ROLE "+owner, 0))
					run(fixture.Exec("DROP ROLE "+reader, 0))
					run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				}()
			}
		})
	}
}

func TestReferenceTableCoreHeldCursorAndPreparedRebinding(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			record := backend.Recorder
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, record)
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, backend.DB, record, s) }
			run(fixture.Exec("CREATE TABLE held(id INT)", 0))
			run(fixture.Exec("INSERT INTO held VALUES(11),(22)", 2))
			stmt, err := backend.DB.PrepareContext(ctx, "SELECT id FROM held ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held, err := backend.DB.QueryContext(ctx, "SELECT id FROM held ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var raw driver.Rows
			var rawConnClose func() error
			if !backend.PG {
				conn, err := p.Native.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				rawConnClose = conn.Close
				defer rawConnClose()
				if err := conn.Raw(func(c any) error {
					var e error
					raw, e = c.(driver.QueryerContext).QueryContext(ctx, "SELECT id FROM held ORDER BY id", nil)
					return e
				}); err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
			}
			consume := func() {
				labels, err := held.Columns()
				if err != nil || !reflect.DeepEqual(labels, []string{"id"}) {
					t.Fatal(labels, err)
				}
				types, err := held.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal(types, err)
				}
				var values []int64
				for held.Next() {
					var value int64
					if err := held.Scan(&value); err != nil {
						t.Fatal(err)
					}
					values = append(values, value)
				}
				if err := held.Err(); err != nil || !reflect.DeepEqual(values, []int64{11, 22}) {
					t.Fatal(values, err)
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
				record.Mu.Lock()
				var observation *fixture.Operation
				for _, op := range record.Operations {
					if op.SQL == "SELECT id FROM held ORDER BY id" && op.Err == nil && op.Columns != nil {
						observation = op
					}
				}
				if observation == nil {
					record.Mu.Unlock()
					t.Fatal("held cursor has no recorded result")
				}
				captured, closed, closeErr := observation.Rows, observation.Closed, observation.CloseErr
				record.Mu.Unlock()
				if !reflect.DeepEqual(captured, [][]driver.Value{{int64(11)}, {int64(22)}}) || !closed || closeErr != nil {
					t.Fatal("held recorded values/closure", captured, closed, closeErr)
				}
			}
			// PostgreSQL portals must close before DROP; native eager cursors stay held.
			if backend.PG {
				consume()
			}
			run(fixture.Exec("DROP TABLE held RESTRICT", 0))
			failed, err := stmt.QueryContext(ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("absent prepared query exposed rows")
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "held" does not exist`, Position: "16"}
			fixture.AssertDiagnostic(t, err, &want)
			run(fixture.Exec("CREATE TABLE held(other TEXT)", 0))
			run(fixture.Exec("INSERT INTO held VALUES('fresh')", 1))
			failed, err = stmt.QueryContext(ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("incompatible prepared query exposed rows")
			}
			want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "id" does not exist`, Position: "8"}
			fixture.AssertDiagnostic(t, err, &want)
			if !backend.PG {
				consume()
				if !reflect.DeepEqual(raw.Columns(), []string{"id"}) || raw.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
					t.Fatal("retained raw metadata changed")
				}
				values := make([]driver.Value, 1)
				for _, n := range []int64{11, 22} {
					if err := raw.Next(values); err != nil || values[0] != n {
						t.Fatal(values, err)
					}
				}
				if err := raw.Next(values); err != io.EOF {
					t.Fatal("raw cursor EOF", err)
				}
				if err := raw.Close(); err != nil {
					t.Fatal(err)
				}
			}
			fresh := fixture.Select("SELECT * FROM held", []string{"other"}, []string{"TEXT"}, [][]any{{"fresh"}})
			run(fresh)
			tx, err := backend.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if backend.PG {
				fixture.RunStep(t, ctx, tx, record, fixture.Exec("DROP TABLE held", 0))
			} else {
				_, err = tx.ExecContext(ctx, "DROP TABLE held")
				fixture.State(t, err, "0A000")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fresh)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = backend.DB.ExecContext(canceled, "DROP TABLE held")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(fresh)
		})
	}
}
