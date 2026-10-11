package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type addConstraintFailedReader struct{ err error }

func (r addConstraintFailedReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceAlterAddConstraintControls(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "after_type"}[rewrite], func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1,2)", 1))
				run(fixture.Exec("CREATE TABLE t(a INT,b INT,n INT,note TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(9,9,NULL,'old')", 1))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a<5) NO INHERIT NOT VALID, ADD CONSTRAINT nn NOT NULL n NOT VALID, ADD CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH FULL NOT VALID", 0))
				run(fixture.Exec("CREATE TABLE valid(id INT,note TEXT)", 0))
				run(fixture.Exec("INSERT INTO valid VALUES(1,'keep')", 1))
				run(fixture.Exec("ALTER TABLE valid ADD CONSTRAINT valid_pk PRIMARY KEY(id), ADD CONSTRAINT valid_ck CHECK(id>0), ADD CONSTRAINT valid_ex EXCLUDE(note WITH =)", 0))
				if rewrite {
					run(fixture.Exec("ALTER TABLE t ALTER note TYPE VARCHAR(20), ALTER a TYPE BIGINT, ALTER n TYPE BIGINT", 0))
				}
				intType := "INT4"
				noteType := "TEXT"
				if rewrite {
					intType, noteType = "INT8", "VARCHAR"
				}
				want := fixture.Select("SELECT a,b,n,note FROM t", []string{"a", "b", "n", "note"}, []string{intType, "INT4", intType, noteType}, [][]any{{int64(9), int64(9), nil, "old"}})
				definitions := func() {
					addConstraintInvalid(t, p, b, c, "t", "ck", "CHECK ((a < 5)) NO INHERIT NOT VALID")
					addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL n NOT VALID")
					addConstraintInvalid(t, p, b, c, "t", "fk", "FOREIGN KEY (a, b) REFERENCES p(a, b) MATCH FULL NOT VALID")
				}
				definitions()
				run(want)
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, want.Query, want.Columns, want.Types, []bool{true, true, false, true}, 1)
				var retained *sql.Rows
				if !b.PG {
					var err error
					retained, err = c.QueryContext(p.Ctx, want.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					labels, err := retained.Columns()
					if err != nil || !reflect.DeepEqual(labels, want.Columns) {
						t.Fatal("retained labels", labels, err)
					}
					types, err := retained.ColumnTypes()
					if err != nil || len(types) != 4 {
						t.Fatal("retained metadata", types, err)
					}
					if nullable, known := types[2].Nullable(); !known || nullable {
						t.Fatal("invalid NN metadata is not enforced nonnullable", nullable, known)
					}
					oldIDs := addConstraintCatalog(t, p, c)
					dump := addConstraintDump(t, p)
					if strings.Count(string(dump), "NOT VALID") != 3 {
						t.Fatal("dump lost invalid definitions", string(dump))
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(fixture.Error("SELECT * FROM t", "42P01"))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump, addConstraintDump(t, p)) {
						t.Fatal("invalid definition canonical redump changed")
					}
					if reflect.DeepEqual(oldIDs, addConstraintCatalog(t, p, c)) {
						t.Fatal("Restore did not remint persistent identities")
					}
					definitions()
					run(want)
					// Restore into a distinct engine must not mutate the source stream/catalog.
					driver := stuntgres.NewDriver()
					connector, err := driver.OpenConnector("add-constraint-import")
					if err != nil {
						t.Fatal(err)
					}
					freshNative := sql.OpenDB(connector)
					defer freshNative.Close()
					fresh := sql.OpenDB(fixture.MigrationRecordedConnector(t, connector))
					defer fresh.Close()
					if err := stuntgres.Restore(p.Ctx, freshNative, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, fresh, &fixture.Recorder{RawBackend: fixture.RawNative}, want)
					if !bytes.Equal(dump, addConstraintDump(t, p)) {
						t.Fatal("source changed during distinct import")
					}
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
							t.Fatal("real emitted SQL PG replay", err, string(dump))
						}
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE t,valid,p CASCADE"); err != nil {
							t.Fatal(err)
						}
					}
					if !retained.Next() {
						t.Fatal(retained.Err())
					}
					var a, bvalue int64
					var n any
					var note string
					if err := retained.Scan(&a, &bvalue, &n, &note); err != nil || a != 9 || bvalue != 9 || n != nil || note != "old" {
						t.Fatal(a, bvalue, n, note, err)
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal(retained.Err())
					}
					retained.Close()
				}
				ck := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "t" violates check constraint "ck"`)
				ck.Detail, ck.Schema, ck.Table, ck.Constraint = "Failing row contains (9, 9, 1, bad).", "public", "t", "ck"
				heldWrite := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2,$3,$4)")
				fixture.RunStep(t, p.Ctx, heldWrite, b.Recorder, fixture.ErrorDiagnostic(heldWrite.SQL, ck, 9, 9, 1, "bad"))
				fixture.RunStep(t, p.Ctx, heldWrite, b.Recorder, fixture.ErrorDiagnostic(heldWrite.SQL, fixture.PrimaryKeyReferenceNull("t", "n", "1, 2, null, bad"), 1, 2, nil, "bad"))
				fixture.RunStep(t, p.Ctx, heldWrite, b.Recorder, fixture.ErrorDiagnostic(heldWrite.SQL, addConstraintFK("t", "fk", "a, b", "3, 4", "p"), 3, 4, 1, "bad"))
				late := []struct {
					query string
					want  fixture.Diagnostic
				}{
					{"ALTER TABLE t ADD CONSTRAINT late FOREIGN KEY(b) REFERENCES absent(id) NOT VALID", fixture.ControlsReferenceDiagnostic("42P01", `relation "absent" does not exist`)},
					{"ALTER TABLE t ADD CONSTRAINT late CHECK(missing>0) NOT VALID", fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)},
				}
				if b.PG {
					for _, failure := range late {
						for _, prepared := range []bool{false, true} {
							addConstraintError(t, p, c, failure.query, failure.want, prepared, "execute")
						}
					}
				}
				if !b.PG {
					run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
					run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
					before := addConstraintDump(t, p)
					ids := addConstraintCatalog(t, p, c)
					for _, failure := range late {
						script := string(before) + failure.query + ";"
						held, err := c.QueryContext(p.Ctx, "SELECT v FROM sentinel")
						if err != nil {
							t.Fatal(err)
						}
						restoreErr := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
						// Pinned ALTER cooking leaves Position absent for both failures.
						fixture.AssertDiagnostic(t, restoreErr, &failure.want)
						fixture.TransactionValues(t, held, 91)
						held.Close()
						if !bytes.Equal(before, addConstraintDump(t, p)) || !reflect.DeepEqual(ids, addConstraintCatalog(t, p, c)) {
							t.Fatal("late failure changed target identities/catalog/data")
						}
						if p.Postgres != nil {
							result, err := p.Postgres.ExecContext(p.Ctx, script)
							if result != nil {
								t.Fatal("failed PostgreSQL replay published a result")
							}
							fixture.AssertDiagnostic(t, err, &failure.want)
						}
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before)); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					ioerr := errors.New("add constraint input failure")
					if err := stuntgres.Restore(p.Ctx, p.Native, addConstraintFailedReader{ioerr}); !errors.Is(err, ioerr) {
						t.Fatal(err)
					}
					if !bytes.Equal(before, addConstraintDump(t, p)) {
						t.Fatal("ordinary import failure changed target")
					}
					run(fixture.Exec("CREATE TEMP TABLE temp_parent(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TEMP TABLE temp_child(pid INT REFERENCES temp_parent)", 0))
					run(fixture.Exec("INSERT INTO temp_parent VALUES(7)", 1))
					run(fixture.Exec("INSERT INTO temp_child VALUES(7)", 1))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select("SELECT pid FROM temp_child", []string{"pid"}, []string{"INT4"}, [][]any{{int64(7)}}))
					_, err := c.ExecContext(p.Ctx, "INSERT INTO temp_child VALUES(8)")
					got := fixture.ExtractDiagnostic(t, err)
					want := addConstraintFK("temp_child", "temp_child_pid_fkey", "pid", "8", "temp_parent")
					if !strings.HasPrefix(got.Schema, "pg_temp_") {
						t.Fatal("TEMP diagnostic lost actual namespace", got)
					}
					want.Schema = got.Schema // allocated TEMP namespace identity, not a fixed allocator value
					fixture.AssertDiagnostic(t, err, &want)
				}
				for _, q := range []string{"CREATE TABLE cycle_a(id INT PRIMARY KEY,bid INT)", "CREATE TABLE cycle_b(id INT PRIMARY KEY,aid INT)", "INSERT INTO cycle_a VALUES(1,2)", "INSERT INTO cycle_b VALUES(2,1)", "ALTER TABLE cycle_a ADD CONSTRAINT cycle_a_fk FOREIGN KEY(bid) REFERENCES cycle_b", "ALTER TABLE cycle_b ADD CONSTRAINT cycle_b_fk FOREIGN KEY(aid) REFERENCES cycle_a"} {
					count := int64(0)
					if strings.HasPrefix(q, "INSERT") {
						count = 1
					}
					run(fixture.Exec(q, count))
				}
				if !b.PG {
					cyclic := addConstraintDump(t, p)
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(cyclic)); err != nil {
						t.Fatal("valid cyclic pending FKs with installed invalid FK", err)
					}
					if !bytes.Equal(cyclic, addConstraintDump(t, p)) {
						t.Fatal("mixed cyclic/invalid canonical replay changed")
					}
					definitions()
					run(want)
					run(fixture.Select("SELECT id,bid FROM cycle_a", []string{"id", "bid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
					run(fixture.ErrorDiagnostic("INSERT INTO cycle_a VALUES(3,9)", addConstraintFK("cycle_a", "cycle_a_fk", "bid", "9", "cycle_b")))
				}
				run(fixture.Exec("UPDATE t SET a=1,b=2,n=1", 1))
				definitions()
			})
		})
	}
}
