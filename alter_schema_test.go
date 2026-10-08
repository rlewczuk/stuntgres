package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func alterSchemaFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{
		"CREATE SCHEMA s1", "CREATE SCHEMA s2",
		"CREATE TABLE s1.t(id INTEGER CONSTRAINT nn_id NOT NULL,note TEXT DEFAULT 'fallback')",
		"INSERT INTO s1.t VALUES(7,'keep'),(9,NULL)",
		"COMMENT ON TABLE s1.t IS 'table comment'", "COMMENT ON COLUMN s1.t.id IS 'id comment'",
		"CREATE SEQUENCE s1.q1 START 10 CACHE 1 OWNED BY s1.t.id",
		"CREATE SEQUENCE s1.q2 START 100 CACHE 1 OWNED BY s1.t.id",
		"CREATE SEQUENCE s1.free START 1000", "CREATE VIEW public.v AS SELECT id,note FROM s1.t",
	} {
		execSQL(t, db, q)
	}
	return db
}

func alterSchemaDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestAlterSchemaValuesAndDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := alterSchemaFixture(t)
			c, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			scalar := func(q string, want int64) {
				t.Helper()
				var got int64
				if err := c.QueryRowContext(context.Background(), q).Scan(&got); err != nil || got != want {
					t.Fatal(q, got, want, err)
				}
			}
			scalar("SELECT nextval('s1.q1')", 10)
			scalar("SELECT nextval('s1.q2')", 100)
			before := alterSchemaDump(t, db)
			var result sql.Result
			if prepared {
				s, err := c.PrepareContext(context.Background(), "ALTER TABLE s1.t SET SCHEMA s2")
				if err != nil {
					t.Fatal("Prepare", err)
				}
				defer s.Close()
				if got := alterSchemaDump(t, db); got != before {
					t.Fatal("Prepare changed catalog")
				}
				result, err = s.ExecContext(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.ExecContext(context.Background())
				alterDropDiagnostic(t, err, "42P01", `relation "s1.t" does not exist`, "")
			} else {
				result, err = c.ExecContext(context.Background(), "ALTER TABLE s1.t SET SCHEMA s2")
				if err != nil {
					t.Fatal(err)
				}
			}
			if n, err := result.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
			for _, mode := range []bool{false, true} {
				alterDropMetadata(t, db, mode, "SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true}, 2)
			}
			want := [][]any{{int64(7), "keep"}, {int64(9), nil}}
			wantTuples(t, db, "SELECT id,note FROM s2.t ORDER BY id", want)
			wantTuples(t, db, "SELECT id,note FROM public.v ORDER BY id", want)
			_, err = db.Query("SELECT id,note FROM s1.t ORDER BY id")
			alterDropDiagnostic(t, err, "42P01", `relation "s1.t" does not exist`, "21")
			scalar("SELECT nextval('s2.q1')", 11)
			scalar("SELECT currval('s2.q1')", 11)
			scalar("SELECT lastval()", 11)
			scalar("SELECT nextval('s2.q2')", 101)
			scalar("SELECT nextval('s1.free')", 1000)
			_, err = c.ExecContext(context.Background(), "INSERT INTO s2.t VALUES(NULL,'bad')")
			var d *stuntgres.Error
			wantError := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "s2", Table: "t", Column: "id"}
			if !errors.As(err, &d) || *d != wantError {
				t.Fatal(d, err, wantError)
			}
			wantTuples(t, db, "SELECT id,note FROM s2.t ORDER BY id", want)
		})
	}
}

func TestAlterSchemaFailuresAreAtomic(t *testing.T) {
	for _, collision := range []string{"CREATE TABLE s2.t(id INT)", "CREATE VIEW s2.t AS SELECT id FROM s1.t", "CREATE SEQUENCE s2.t", "CREATE SEQUENCE s2.q2"} {
		for _, prepared := range []bool{false, true} {
			t.Run(collision+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				db := alterSchemaFixture(t)
				execSQL(t, db, collision)
				before := alterSchemaDump(t, db)
				var err error
				if prepared {
					s, e := db.Prepare("ALTER TABLE s1.t SET SCHEMA s2")
					if e != nil {
						t.Fatal("Prepare must succeed", e)
					}
					defer s.Close()
					_, err = s.Exec()
				} else {
					_, err = db.Exec("ALTER TABLE s1.t SET SCHEMA s2")
				}
				name := "t"
				if strings.HasSuffix(collision, "q2") {
					name = "q2"
				}
				alterDropDiagnostic(t, err, "42P07", `relation "`+name+`" already exists in schema "s2"`, "")
				if after := alterSchemaDump(t, db); after != before {
					t.Fatal("failed move changed persistent closure", before, after)
				}
				wantTuples(t, db, "SELECT id,note FROM public.v ORDER BY id", [][]any{{int64(7), "keep"}, {int64(9), nil}})
			})
		}
	}
}

func TestAlterSchemaMalformedCanceledAndTransactionBoundaries(t *testing.T) {
	db := alterSchemaFixture(t)
	before := alterSchemaDump(t, db)
	q := "ALTER TABLE s1.t SET SCHEMA s2, DROP COLUMN missing"
	_, err := db.Exec(q)
	alterDropDiagnostic(t, err, "42601", `syntax error at or near ","`, "31")
	_, err = db.Prepare(q)
	alterDropDiagnostic(t, err, "42601", `syntax error at or near ","`, "31")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = db.ExecContext(ctx, "ALTER TABLE s1.t SET SCHEMA s2")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	utility := "ALTER TABLE s1.t SET SCHEMA s2"
	held, err := c.PrepareContext(context.Background(), utility)
	if err != nil {
		t.Fatal("autocommit utility Prepare", err)
	}
	defer held.Close()
	for _, readOnly := range []bool{false, true} {
		for _, route := range []string{"prepare_in_transaction", "direct", "held_prepared"} {
			tx, err := c.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: readOnly})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			switch route {
			case "prepare_in_transaction":
				s, prepareErr := tx.Prepare(utility)
				if s != nil {
					s.Close()
				}
				err = prepareErr
			case "direct":
				_, err = tx.Exec(utility)
			case "held_prepared":
				_, err = held.ExecContext(context.Background())
			}
			if readOnly && route != "prepare_in_transaction" {
				alterDropDiagnostic(t, err, "25006", "cannot execute ALTER TABLE in a read-only transaction", "")
			} else {
				alterDropDiagnostic(t, err, "0A000", "statement is unsupported in an explicit transaction", "")
			}
			_, err = tx.Exec("SELECT id FROM s1.t")
			alterDropDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var id int64
			if err := c.QueryRowContext(context.Background(), "SELECT id FROM s1.t WHERE id=7").Scan(&id); err != nil || id != 7 {
				t.Fatal("connection or source unusable after rollback", id, err)
			}
		}
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if after := alterSchemaDump(t, db); after != before {
		t.Fatal("rejected operation changed state")
	}
}

func TestAlterSchemaNativeControls(t *testing.T) {
	db := alterSchemaFixture(t)
	held, err := db.Query("SELECT id,note FROM s1.t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	execSQL(t, db, "ALTER TABLE s1.t SET SCHEMA s2")
	dump := alterSchemaDump(t, db)
	for _, fragment := range []string{"nn_id", "fallback", "table comment", "id comment", "OWNED BY", "s2", "free", "CREATE VIEW"} {
		if !strings.Contains(dump, fragment) {
			t.Fatal(fragment, dump)
		}
	}
	if strings.Contains(dump, "SET SCHEMA") {
		t.Fatal("dump records history", dump)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	if got := alterSchemaDump(t, db); got != dump {
		t.Fatal("noncanonical roundtrip", dump, got)
	}
	wantTuples(t, db, "SELECT id,note FROM s2.t ORDER BY id", [][]any{{int64(7), "keep"}, {int64(9), nil}})
	wantTuples(t, db, "SELECT id,note FROM public.v ORDER BY id", [][]any{{int64(7), "keep"}, {int64(9), nil}})
	labels, err := held.Columns()
	if err != nil || !reflect.DeepEqual(labels, []string{"id", "note"}) {
		t.Fatal(labels, err)
	}
	var got [][]any
	for held.Next() {
		var id, note any
		if err := held.Scan(&id, &note); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{id, note})
	}
	if held.Err() != nil || !reflect.DeepEqual(got, [][]any{{int64(7), "keep"}, {int64(9), nil}}) {
		t.Fatal(got, held.Err())
	}
	tableCoreExec(t, db, true, "INSERT INTO s2.t(id) VALUES(11)", 1)
	wantTuples(t, db, "SELECT note FROM s2.t WHERE id=11", [][]any{{"fallback"}})
	_, err = db.Query("SELECT id,note FROM s1.t ORDER BY id")
	alterDropDiagnostic(t, err, "42P01", `relation "s1.t" does not exist`, "21")
}
