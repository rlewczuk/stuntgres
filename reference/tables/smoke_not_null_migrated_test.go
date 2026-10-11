package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeDropNotNullSystemColumnSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			m, db, ctx, notices := fixture.MigrationAlterFixture(t, p, backend)
			_ = ctx
			m.NullExec(db, false, "CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL)", 0)
			m.NullExec(db, false, "INSERT INTO t VALUES(7)", 1)
			for _, prepared := range []bool{false, true} {
				for _, column := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
					q := "ALTER TABLE t ALTER " + column + " DROP NOT NULL"
					var result sql.Result
					var err error
					if prepared {
						stmt, e := db.Prepare(q)
						if e != nil {
							t.Fatal("effect-free Prepare", e)
						}
						result, err = stmt.Exec()
						stmt.Close()
					} else {
						result, err = db.Exec(q)
					}
					if result != nil {
						t.Fatal("failed utility returned result")
					}
					m.NullDiagnostic(err, stuntgres.Error{Code: "0A000", Message: `cannot alter system column "` + column + `"`})
				}
			}
			_, err := db.Exec("UPDATE t SET a=NULL")
			m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
			m.NullQuery(db, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, [][]any{{int64(7)}})
			if len(*notices) != 0 {
				t.Fatal(*notices)
			}
		})
	}
}

func TestReferenceSmokeDropConstraintOwnedSequenceSmoke(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t DROP CONSTRAINT a_nn", "ALTER TABLE t DROP CONSTRAINT a_nn RESTRICT", "ALTER TABLE t DROP CONSTRAINT a_nn CASCADE", "ALTER TABLE t ALTER a DROP NOT NULL"} {
		for _, prepared := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						m, db, ctx, notices := fixture.MigrationAlterFixture(t, p, backend)
						m.NullExec(db, false, "CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,b INT DEFAULT 8 CONSTRAINT b_nn NOT NULL)", 0)
						m.NullExec(db, false, "CREATE SEQUENCE s START 17 OWNED BY t.a", 0)
						m.NullExec(db, false, "COMMENT ON COLUMN t.a IS 'survivor'", 0)
						m.NullExec(db, false, "INSERT INTO t VALUES(7,8)", 1)
						m.NullExec(db, prepared, q, 0)
						var n int64
						if err := db.QueryRow("SELECT nextval('s')").Scan(&n); err != nil || n != 17 {
							t.Fatal("drop changed owned generator", n, err)
						}
						m.NullExec(db, prepared, "UPDATE t SET a=NULL", 1)
						m.NullExec(db, prepared, "INSERT INTO t(a) VALUES(NULL)", 1)
						if !backend.PG {
							var dump bytes.Buffer
							if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
								t.Fatal(err)
							}
							if strings.Contains(dump.String(), `CONSTRAINT "a_nn"`) || !strings.Contains(dump.String(), `CONSTRAINT "b_nn" NOT NULL`) {
								t.Fatal("dump did not capture post-drop constraints", dump.String())
							}
							if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
								t.Fatal(err)
							}
							var redump bytes.Buffer
							if err := stuntgres.Dump(ctx, p.Native, &redump); err != nil || dump.String() != redump.String() {
								t.Fatal("post-drop roundtrip changed persistent state", err, redump.String())
							}
						}
						m.NullQuery(db, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, false}, [][]any{{nil, int64(8)}, {nil, int64(8)}})
						_, err := db.Exec("INSERT INTO t VALUES(NULL,NULL)")
						m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "b" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "t", Column: "b"})
						if err = db.QueryRow("SELECT nextval('s')").Scan(&n); err != nil || n != 18 {
							t.Fatal("restored generator state", n, err)
						}
						m.NullExec(db, false, "ALTER TABLE t DROP a", 0)
						_, err = db.Exec("SELECT nextval('s')")
						m.NullDiagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
						if len(*notices) != 0 {
							t.Fatal(*notices)
						}
					})
				}
			})
		}
	}
}

func TestReferenceSmokeNotNullSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			m, db, ctx, notices := fixture.MigrationAlterFixture(t, p, backend)
			_ = ctx
			if err := db.PingContext(ctx); err != nil {
				t.Fatal(err)
			}
			exec := func(q string, count int64, args ...any) { m.NullExec(db, false, q, count, args...) }
			exec("CREATE TABLE t(a INT NULL,b TEXT,CONSTRAINT nn NOT NULL a)", 0)
			m.NullExec(db, true, "INSERT INTO t VALUES($1,$2)", 1, 7, nil)
			want := stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}
			_, err := db.Exec("INSERT INTO t VALUES(8,'valid'),(NULL,'bad')")
			m.NullDiagnostic(err, want)
			stmt, err := db.Prepare("INSERT INTO t VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.Exec(nil, "bad")
			stmt.Close()
			m.NullDiagnostic(err, want)
			m.NullQuery(db, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, true}, [][]any{{int64(7), nil}})
			exec("ALTER TABLE t DROP CONSTRAINT nn", 0)
			exec("INSERT INTO t VALUES(NULL,'keep')", 1)
			_, err = db.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a")
			m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
			exec("CREATE TABLE valid(a INT)", 0)
			exec("INSERT INTO valid VALUES(7)", 1)
			validInsert, err := db.Prepare("INSERT INTO valid VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer validInsert.Close()
			validHeld := fixture.HeldStatement{Executor: db, Statement: validInsert, SQL: "INSERT INTO valid VALUES($1)"}
			m.NullExec(db, true, "ALTER TABLE valid ALTER a SET NOT NULL", 0)
			_, err = validHeld.Exec(validHeld.SQL, nil)
			m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "valid" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "valid", Column: "a"})
			m.NullQuery(db, "SELECT * FROM valid", []string{"a"}, []string{"INT4"}, []bool{false}, [][]any{{int64(7)}})
			exec("ALTER TABLE valid DROP CONSTRAINT valid_a_not_null", 0)
			nilResult, err := validHeld.Exec(validHeld.SQL, nil)
			if err != nil {
				t.Fatal(err)
			}
			nilCount, countErr := nilResult.RowsAffected()
			if countErr != nil || nilCount != 1 {
				t.Fatal(nilCount, countErr)
			}
			m.NullQuery(db, "SELECT * FROM valid ORDER BY a", []string{"a"}, []string{"INT4"}, []bool{true}, [][]any{{int64(7)}, {nil}})
			exec("CREATE TABLE occupied(x INT CONSTRAINT generated_a_not_null NOT NULL)", 0)
			exec("CREATE TABLE generated(a INT NOT NULL)", 0)
			exec("CREATE TABLE clone(LIKE generated)", 0)
			exec("ALTER TABLE generated DROP CONSTRAINT generated_a_not_null1", 0)
			exec("INSERT INTO generated VALUES(NULL)", 1)
			_, err = db.Exec("INSERT INTO clone VALUES(NULL)")
			m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "clone", Column: "a"})
			exec("ALTER TABLE clone DROP CONSTRAINT generated_a_not_null1", 0)
			_, err = db.Exec("CREATE TABLE sequential(a INT NOT NULL,b INT CONSTRAINT sequential_a_not_null NOT NULL)")
			referenceNotNullNameCollisionDiagnostic(t, err, "sequential_a_not_null")
			_, err = db.Query("SELECT * FROM sequential")
			m.NullDiagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "sequential" does not exist`, Position: "15"})
			m.NullExec(db, true, "CREATE TABLE sequential(b INT CONSTRAINT sequential_a_not_null NOT NULL,a INT NOT NULL)", 0)
			exec("ALTER TABLE sequential DROP CONSTRAINT sequential_a_not_null1", 0)
			exec("ALTER TABLE sequential DROP CONSTRAINT sequential_a_not_null", 0)
			_, err = db.Exec("CREATE TABLE system_target(a INT,NOT NULL ctid)")
			m.NullDiagnostic(err, stuntgres.Error{Code: "0A000", Message: `cannot add not-null constraint on system column "ctid"`})
			_, err = db.Query("SELECT * FROM system_target")
			m.NullDiagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "system_target" does not exist`, Position: "15"})
			if !backend.PG {
				for _, tc := range []struct{ q, token, message string }{{"ALTER TABLE t ADD NOT NULL a NOT VALID", "NOT VALID", "NOT NULL modifiers are unsupported"}} {
					_, err = db.Exec(tc.q)
					m.NullDiagnostic(err, stuntgres.Error{Code: "0A000", Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
				}
			}
			exec("ALTER TABLE t DROP CONSTRAINT IF EXISTS absent", 0)
			wantNotice := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "absent" of relation "t" does not exist, skipping`}}
			if !reflect.DeepEqual(*notices, wantNotice) {
				t.Fatal(*notices)
			}
		})
	}
}

func TestReferenceSmokeNotNullControlsSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			m, db, ctx, _ := fixture.MigrationAlterFixture(t, p, backend)
			m.NullExec(db, false, `CREATE TABLE "t"(a INT CONSTRAINT "n""é" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0)
			text := "O'Brien\n\\tail"
			m.NullExec(db, true, "INSERT INTO t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), text, true)
			m.NullExec(db, false, "INSERT INTO t VALUES(8,NULL,NULL,NULL)", 1)
			m.NullExec(db, false, "COMMENT ON TABLE t IS 'persistent'", 0)
			m.NullExec(db, false, "CREATE TABLE generated(a INT NOT NULL)", 0)
			m.NullQuery(db, "SELECT * FROM t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}})
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(dump.String(), `CONSTRAINT "n""é" NOT NULL`) || !strings.Contains(dump.String(), `CONSTRAINT "generated_a_not_null" NOT NULL`) {
					t.Fatal(dump.String())
				}
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				_, err := db.Query("SELECT * FROM t")
				m.NullDiagnostic(err, stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"})
				if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				m.NullQuery(db, "SELECT * FROM t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}})
				var redump bytes.Buffer
				if err = stuntgres.Dump(ctx, p.Native, &redump); err != nil || dump.String() != redump.String() {
					t.Fatal(err, redump.String())
				}
			}
			m.NullExec(db, false, `ALTER TABLE t DROP CONSTRAINT "n""é"`, 0)
			m.NullExec(db, true, "ALTER TABLE generated DROP CONSTRAINT generated_a_not_null", 0)
			m.NullExec(db, false, "CREATE TABLE sentinel(v INT)", 0)
			m.NullExec(db, false, "INSERT INTO sentinel VALUES(91)", 1)
			m.NullQuery(db, "SELECT * FROM sentinel", []string{"v"}, []string{"INT4"}, []bool{true}, [][]any{{int64(91)}})
			if !backend.PG {
				held, err := db.Query("SELECT * FROM sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT CONSTRAINT nn NOT NULL);INSERT INTO replacement VALUES(NULL)"))
				m.NullDiagnostic(err, stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "replacement" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "replacement", Column: "a"})
				if !held.Next() {
					t.Fatal(held.Err())
				}
				var value int64
				if err = held.Scan(&value); err != nil || value != 91 {
					t.Fatal(value, err)
				}
				held.Close()
				m.NullQuery(db, "SELECT * FROM sentinel", []string{"v"}, []string{"INT4"}, []bool{true}, [][]any{{int64(91)}})
				err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT NOT NULL,b INT CONSTRAINT replacement_a_not_null NOT NULL)"))
				referenceNotNullNameCollisionDiagnostic(t, err, "replacement_a_not_null")
				m.NullQuery(db, "SELECT * FROM sentinel", []string{"v"}, []string{"INT4"}, []bool{true}, [][]any{{int64(91)}})
			}
		})
	}

}
