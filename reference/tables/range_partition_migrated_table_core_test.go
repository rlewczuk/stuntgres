package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// TestReferenceRangePartitionMigratedTableCoreSmoke preserves the mixed direct /
// prepared route union from smoke/TestTableCoreSmoke. PostgreSQL portals finish
// before DROP; the native driver cursor independently retains its old snapshot.
func TestReferenceRangePartitionMigratedTableCoreSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	nativeDSN := "reference/" + t.Name()
	var emitted string
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, _, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, record, s) }
			exec := func(prepared bool, q string, count int64, args ...any) {
				s := fixture.Exec(q, count, args...)
				s.Prepared = prepared
				run(s)
			}
			query := func(prepared bool, q string, names, types []string, nullable []bool, want [][]any) {
				s := fixture.Select(q, names, types, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLColumnTypes(t, backend.PG, columns, names, types, nullable)
				}
				run(s)
			}
			failure := func(prepared, selectRows bool, q string, want fixture.Diagnostic) {
				s := fixture.ImplicitError(q, want)
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, selectRows, "execute"
				run(s)
			}
			absent := func(name string) {
				failure(false, true, "SELECT * FROM "+name, fixture.Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
			}
			exec(false, "CREATE TABLE source(i INTEGER NOT NULL,s TEXT,f BOOLEAN)", 0)
			exec(true, "INSERT INTO source VALUES($1,$2,$3)", 1, 7, "O'Brien", true)
			exec(true, "CREATE TABLE typed(i INT4 NOT NULL,b INT8,s TEXT,f BOOL)", 0)
			exec(true, "INSERT INTO typed VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien", true)
			exec(false, `CREATE TABLE quoted_types(i "int4" NOT NULL,b "int8",s "text",f "bool")`, 0)
			exec(true, "INSERT INTO quoted_types VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien", true)
			typedNames, typedTypes := []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}
			typedNullable := []bool{false, true, true, true}
			typedRows := [][]any{{int64(7), int64(9223372036854775807), "O'Brien", true}}
			for _, prepared := range []bool{false, true} {
				query(prepared, "SELECT * FROM typed", typedNames, typedTypes, typedNullable, typedRows)
			}
			query(true, "SELECT * FROM quoted_types", typedNames, typedTypes, typedNullable, typedRows)
			failure(true, false, `CREATE TABLE bad (id "integer")`, fixture.Diagnostic{Code: "42704", Message: `type "integer" does not exist`, Position: "22"})
			exec(false, "CREATE TABLE clone(lead BIGINT,LIKE source,tail TEXT)", 0)
			names, types := []string{"lead", "i", "s", "f", "tail"}, []string{"INT8", "INT4", "TEXT", "BOOL", "TEXT"}
			nullable := []bool{true, false, true, true, true}
			query(true, "SELECT * FROM clone", names, types, nullable, nil)
			exec(true, "INSERT INTO clone VALUES($1,$2,$3,$4,$5)", 1, 9, 7, "O'Brien", true, "tail")
			failure(false, false, "INSERT INTO clone(i) VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null, null).", Schema: "public", Table: "clone", Column: "i"})
			cloneRows := [][]any{{int64(9), int64(7), "O'Brien", true, "tail"}}
			query(false, "SELECT * FROM clone", names, types, nullable, cloneRows)
			exec(false, "CREATE TABLE one(a INT NOT NULL)", 0)
			exec(false, "CREATE TABLE two(b TEXT)", 0)
			exec(true, "CREATE TABLE interleaved(head BOOL,LIKE one,middle BIGINT,LIKE two,tail TEXT)", 0)
			query(false, "SELECT * FROM interleaved", []string{"head", "a", "middle", "b", "tail"}, []string{"BOOL", "INT4", "INT8", "TEXT", "TEXT"}, []bool{true, false, true, true, true}, nil)
			failure(true, false, "CREATE TABLE bad(LIKE one,a TEXT)", fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`})
			absent("bad")
			for _, q := range []string{"CREATE TABLE bad(LIKE source INCLUDING ALL)", "CREATE TABLE bad(LIKE source EXCLUDING CONSTRAINTS)"} {
				exec(true, q, 0)
				query(true, "SELECT * FROM bad", []string{"i", "s", "f"}, []string{"INT4", "TEXT", "BOOL"}, []bool{false, true, true}, nil)
				failure(false, false, "INSERT INTO bad VALUES(NULL,NULL,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "bad" violates not-null constraint`, Detail: "Failing row contains (null, null, null).", Schema: "public", Table: "bad", Column: "i"})
				exec(false, "DROP TABLE bad", 0)
				absent("bad")
			}
			exec(false, "CREATE TABLE z()", 0)
			exec(true, "CREATE TABLE zl(LIKE z)", 0)
			for _, z := range []string{"z", "zl"} {
				for _, prepared := range []bool{false, true} {
					query(prepared, "SELECT * FROM "+z, []string{}, []string{}, []bool{}, nil)
				}
			}
			for _, q := range []string{"CREATE TABLE IF NOT EXISTS source(i no_such_type)", `CREATE TABLE IF NOT EXISTS source(i "integer")`, "CREATE TABLE IF NOT EXISTS source(i SMALLINT)", "CREATE TABLE IF NOT EXISTS source(LIKE missing INCLUDING ALL)"} {
				notices = nil
				exec(true, q, 0)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "source" already exists, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal("semantic skip notices", notices, want)
				}
			}
			query(false, "SELECT * FROM source", []string{"i", "s", "f"}, []string{"INT4", "TEXT", "BOOL"}, []bool{false, true, true}, [][]any{{int64(7), "O'Brien", true}})
			exec(false, "DROP TABLE source RESTRICT", 0)
			query(true, "SELECT * FROM clone", names, types, nullable, cloneRows)
			exec(false, "CREATE TABLE a(id INT)", 0)
			exec(false, "CREATE TABLE b(id INT)", 0)
			exec(false, "INSERT INTO a VALUES(11)", 1)
			exec(false, "INSERT INTO b VALUES(22)", 1)
			failure(false, false, "DROP TABLE a,missing,b", fixture.Diagnostic{Code: "42P01", Message: `table "missing" does not exist`})
			for i, n := range []string{"a", "b"} {
				query(false, "SELECT * FROM "+n, []string{"id"}, []string{"INT4"}, []bool{true}, [][]any{{int64(11 + 11*i)}})
			}
			notices = nil
			exec(true, "DROP TABLE IF EXISTS a,missing,public.a,b CASCADE", 0)
			wantNotice := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "missing" does not exist, skipping`}}
			if !reflect.DeepEqual(notices, wantNotice) {
				t.Fatal("atomic drop notices", notices, wantNotice)
			}
			absent("a")
			absent("b")

			exec(false, "CREATE TABLE held(id INT)", 0)
			exec(false, "INSERT INTO held VALUES(11),(22)", 2)
			stmt, err := db.PrepareContext(p.Ctx, "SELECT id FROM held")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			heldExecutor := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: "SELECT id FROM held"}
			var retained driver.Rows
			if !backend.PG {
				raw, err := p.NativeConnector.Driver().Open(nativeDSN)
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				retained, err = raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT id FROM held ORDER BY id", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			} else {
				// lib/pq's live portal cannot survive same-session DROP.
				query(false, "SELECT id FROM held ORDER BY id", []string{"id"}, []string{"INT4"}, []bool{true}, [][]any{{int64(11)}, {int64(22)}})
			}
			exec(false, "DROP TABLE held", 0)
			heldFailure := func(want fixture.Diagnostic) {
				s := fixture.ImplicitError("SELECT id FROM held", want)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, heldExecutor, record, s)
			}
			heldFailure(fixture.Diagnostic{Code: "42P01", Message: `relation "held" does not exist`, Position: "16"})
			exec(false, "CREATE TABLE held(other TEXT)", 0)
			exec(false, "INSERT INTO held VALUES('fresh')", 1)
			heldFailure(fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: "8"})
			if retained != nil {
				if !reflect.DeepEqual(retained.Columns(), []string{"id"}) || retained.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
					t.Fatal("retained driver metadata changed")
				}
				values := make([]driver.Value, 1)
				for _, n := range []int64{11, 22} {
					if err := retained.Next(values); err != nil || values[0] != n {
						t.Fatal("retained driver row", values, err)
					}
				}
				if err := retained.Next(values); err != io.EOF {
					t.Fatal("retained driver EOF", err)
				}
			}

			// Native controls have no PostgreSQL control equivalent. Real mode
			// replays the actual emitted SQL, then shares all roundtrip SQL checks.
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				emitted = dump.String()
				// Emitted SQL is validated by replayed behavior, not spelling.
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				absent("clone")
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
			} else {
				if emitted == "" {
					t.Fatal("native predecessor did not produce the actual table dump for replay")
				}
				exec(false, "DROP TABLE clone,typed,quoted_types,one,two,interleaved,z,zl,held", 0)
				absent("clone")
				oracle, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer oracle.Close()
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path"} {
						if _, err := oracle.ExecContext(ctx, query); err != nil {
							t.Error(err)
						}
					}
				}()
				if _, err := oracle.ExecContext(p.Ctx, emitted); err != nil {
					t.Fatal("PostgreSQL rejected actual emitted table SQL", err)
				}
			}
			query(true, "SELECT * FROM clone", names, types, nullable, cloneRows)
			query(false, "SELECT * FROM typed", typedNames, typedTypes, typedNullable, typedRows)
			query(true, "SELECT * FROM quoted_types", typedNames, typedTypes, typedNullable, typedRows)
			for _, z := range []string{"z", "zl"} {
				query(true, "SELECT * FROM "+z, []string{}, []string{}, []bool{}, nil)
			}
			failure(false, false, "INSERT INTO clone(i) VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null, null).", Schema: "public", Table: "clone", Column: "i"})
			for _, dropped := range []string{"source", "a", "b"} {
				absent(dropped)
			}
			if !backend.PG {
				redump := func() {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil || dump.String() != emitted {
						t.Fatal("redump changed complete catalog/data", err, dump.String(), emitted)
					}
				}
				redump()
				for _, tc := range []struct {
					script string
					want   fixture.Diagnostic
				}{
					{"CREATE TABLE replacement(LIKE missing)", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "31"}},
					{"CREATE TABLE replacement(id INT,id TEXT)", fixture.Diagnostic{Code: "42701", Message: `column "id" specified more than once`}},
					{"CREATE TABLE replacement(id no_such_type)", fixture.Diagnostic{Code: "42704", Message: `type "no_such_type" does not exist`, Position: "29"}},
				} {
					err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
					tc.want.Severity, tc.want.SeverityUnlocalized = "ERROR", "ERROR"
					fixture.AssertDiagnostic(t, err, &tc.want)
					query(false, "SELECT * FROM clone", names, types, nullable, cloneRows)
					redump()
				}
			}
			if !backend.PG {
				// Fast lib/pq SQL can finish before cancellation is admitted;
				// the exact precanceled API guarantee is native-only.
				canceled, stop := context.WithCancel(p.Ctx)
				stop()
				result, err := db.ExecContext(canceled, "DROP TABLE clone")
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("precanceled DROP result/error", result, err)
				}
				query(false, "SELECT * FROM clone", names, types, nullable, cloneRows)
			}
		})
	}
}
