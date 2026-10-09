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
	"testing"
)

func TestReferenceTableCoreNativeControlRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec(`CREATE TABLE "source.dot"("i.dot" INT NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
	p.step(t, referenceExec(`INSERT INTO "source.dot" VALUES(1,2,'source',FALSE)`, 1))
	p.step(t, referencePrepared(referenceExec(`CREATE TABLE "clone.dot"(LIKE "source.dot")`, 0)))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "clone.dot" VALUES($1,$2,$3,$4)`, 1, 7, int64(9223372036854775807), "O'Brien\n\\tail", true)))
	p.step(t, referenceExec(`INSERT INTO "clone.dot" VALUES(8,NULL,NULL,NULL)`, 1))
	p.step(t, referenceExec(`CREATE TABLE z()`, 0))
	p.step(t, referencePrepared(referenceExec(`CREATE TABLE zl(LIKE z)`, 0)))
	p.step(t, referenceExec(`DROP TABLE "source.dot" RESTRICT`, 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "LIKE") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	absentClone := referenceImplicitError(`SELECT * FROM "clone.dot"`, referenceDiagnostic{Code: "42P01", Message: `relation "clone.dot" does not exist`, Position: "15"})
	absentClone.selectRows = true
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, absentClone)
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, `DROP TABLE "clone.dot",z,zl`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL rejected native expanded dump", err)
		}
	}
	expected := referenceSelect(`SELECT * FROM "clone.dot" ORDER BY "i.dot"`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien\n\\tail", true}, {int64(8), nil, nil, nil}})
	p.step(t, expected)
	p.step(t, referencePrepared(expected))
	for _, b := range p.backends() {
		referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, false, expected.query, expected.columns, expected.types, []bool{false, true, true, true}, 2)
	}
	for _, z := range []string{"z", "zl"} {
		p.step(t, referenceSelect("SELECT * FROM "+z, []string{}, []string{}, nil))
		p.step(t, referencePrepared(referenceSelect("SELECT * FROM "+z, []string{}, []string{}, nil)))
		for _, b := range p.backends() {
			referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, "SELECT * FROM "+z, []string{}, []string{}, []bool{}, 0)
		}
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	p.step(t, referenceImplicitError(`INSERT INTO "clone.dot" VALUES(NULL,NULL,NULL,NULL)`, referenceDiagnostic{Code: "23502", Message: `null value in column "i.dot" of relation "clone.dot" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null).", Schema: "public", Table: "clone.dot", Column: "i.dot"}))
	for _, script := range []string{"CREATE TABLE replacement(LIKE missing)", "CREATE TABLE replacement(id INT,id TEXT)", "CREATE TABLE replacement(id no_such_type)", "CREATE TABLE replacement(LIKE z INCLUDING ALL)", "DROP TABLE z", "CREATE TABLE replacement(id INT); DROP TABLE replacement"} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted", script)
		}
		p.step(t, expected)
		redump.Reset()
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("failed restore changed catalog", err)
		}
	}
	p.step(t, referencePrepared(referenceExec(`ALTER TABLE "clone.dot" DROP CONSTRAINT "source.dot_i.dot_not_null"`, 0)))
	p.step(t, expected)
	for _, b := range p.backends() {
		referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, expected.query, expected.columns, expected.types, []bool{true, true, true, true}, 2)
	}
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "clone.dot" VALUES(NULL,99,'released',FALSE)`, 1)))
	p.step(t, referenceSelect(`SELECT * FROM "clone.dot" WHERE b=99`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{nil, int64(99), "released", false}}))
	absent := referenceImplicitError(`SELECT * FROM "source.dot"`, referenceDiagnostic{Code: "42P01", Message: `relation "source.dot" does not exist`, Position: "15"})
	absent.selectRows = true
	p.step(t, absent)
}

func TestReferenceTableCoreAuthorizationAndOwnershipCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("table_owner_%v", prepared), fmt.Sprintf("table_reader_%v", prepared)
			roleReferenceCleanup(t, []string{owner, reader})
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE USER "+owner, 0))
			p.step(t, referenceExec("CREATE USER "+reader, 0))
			p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(11)", 1))
			if p.postgres != nil {
				if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			}
			for _, backend := range p.backends() {
				db := backend.db
				conn, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer conn.Close()
					record := backend.recorder
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, conn, record, s) }
					defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
					run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
					run(referenceExec("CREATE TABLE source(i INT NOT NULL)", 0))
					run(referenceExec("INSERT INTO source VALUES(7)", 1))
					run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
					denied := referenceImplicitError("CREATE TABLE denied(LIKE source)", referenceDiagnostic{Code: "42501", Message: "permission denied for table source"})
					denied.errorStage = "execute"
					run(denied)
					denied = referenceImplicitError("DROP TABLE source", referenceDiagnostic{Code: "42501", Message: "must be owner of table source"})
					denied.errorStage = "execute"
					run(denied)
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					blocked := referenceImplicitError("DROP ROLE "+owner, referenceDiagnostic{Code: "2BP01", Message: fmt.Sprintf("role %q cannot be dropped because some objects depend on it", owner), Detail: "owner of table source"})
					blocked.errorStage = "execute"
					run(blocked)
					run(referenceSelect("SELECT * FROM source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(referenceExec("DROP TABLE source CASCADE", 0))
					run(referenceExec("DROP ROLE "+owner, 0))
					run(referenceExec("DROP ROLE "+reader, 0))
					run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				}()
			}
		})
	}
}

func TestReferenceTableCoreHeldCursorAndPreparedRebinding(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			record := backend.recorder
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
			run := func(s referenceStep) { referenceRunStep(t, ctx, backend.db, record, s) }
			run(referenceExec("CREATE TABLE held(id INT)", 0))
			run(referenceExec("INSERT INTO held VALUES(11),(22)", 2))
			stmt, err := backend.db.PrepareContext(ctx, "SELECT id FROM held ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held, err := backend.db.QueryContext(ctx, "SELECT id FROM held ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var raw driver.Rows
			var rawConnClose func() error
			if !backend.pg {
				conn, err := p.native.Conn(ctx)
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
				record.mu.Lock()
				var observation *referenceOperation
				for _, op := range record.operations {
					if op.SQL == "SELECT id FROM held ORDER BY id" && op.Err == nil && op.Columns != nil {
						observation = op
					}
				}
				if observation == nil {
					record.mu.Unlock()
					t.Fatal("held cursor has no recorded result")
				}
				captured, closed, closeErr := observation.Rows, observation.Closed, observation.CloseErr
				record.mu.Unlock()
				if !reflect.DeepEqual(captured, [][]driver.Value{{int64(11)}, {int64(22)}}) || !closed || closeErr != nil {
					t.Fatal("held recorded values/closure", captured, closed, closeErr)
				}
			}
			// PostgreSQL portals must close before DROP; native eager cursors stay held.
			if backend.pg {
				consume()
			}
			run(referenceExec("DROP TABLE held RESTRICT", 0))
			failed, err := stmt.QueryContext(ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("absent prepared query exposed rows")
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "held" does not exist`, Position: "16"}
			referenceAssertDiagnostic(t, err, &want)
			run(referenceExec("CREATE TABLE held(other TEXT)", 0))
			run(referenceExec("INSERT INTO held VALUES('fresh')", 1))
			failed, err = stmt.QueryContext(ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("incompatible prepared query exposed rows")
			}
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "id" does not exist`, Position: "8"}
			referenceAssertDiagnostic(t, err, &want)
			if !backend.pg {
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
			fresh := referenceSelect("SELECT * FROM held", []string{"other"}, []string{"TEXT"}, [][]any{{"fresh"}})
			run(fresh)
			tx, err := backend.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if backend.pg {
				referenceRunStep(t, ctx, tx, record, referenceExec("DROP TABLE held", 0))
			} else {
				_, err = tx.ExecContext(ctx, "DROP TABLE held")
				referenceState(t, err, "0A000")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fresh)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = backend.db.ExecContext(canceled, "DROP TABLE held")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(fresh)
		})
	}
}
