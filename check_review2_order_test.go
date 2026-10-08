package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func checkReview2OrderRestrict(t *testing.T, db *sql.DB, detail string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		checkExecError(t, db, prepared, "DROP SEQUENCE s", stuntgres.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	}
}

func TestCheckReview2OrderReverseMixed(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE SEQUENCE s")
	execSQL(t, db, "CREATE VIEW v AS SELECT nextval('s') AS n")
	execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	var before, after bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	checkReview2OrderRestrict(t, db, "view v depends on sequence s\nconstraint c on table t depends on sequence s")
	if err := stuntgres.Dump(context.Background(), db, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("RESTRICT changed definitions/data/provider state", err)
	}
	wantTuples(t, db, "SELECT a FROM t", [][]any{{int64(7)}})
	wantTuples(t, db, "SELECT n FROM v", [][]any{{int64(2)}})
}

func TestCheckReview2OrderLogicalRoundtrip(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "tables", true: "mixed_transitive_owned"}[mixed], func(t *testing.T) {
			ctx := context.Background()
			source := openLocal(t, stuntgres.NewDriver(), t.Name()+"_source")
			source.SetMaxOpenConns(1)
			execSQL(t, source, "CREATE SEQUENCE s MINVALUE -10 START 1")
			detail := "constraint cz on table z depends on sequence s\nconstraint ca on table a depends on sequence s"
			if mixed {
				// Names deliberately disagree with creation order; the child must
				// also replay after its source, retaining the existing closure target.
				execSQL(t, source, "CREATE VIEW zv AS SELECT nextval('s') AS n")
				execSQL(t, source, "CREATE VIEW av AS SELECT n FROM zv")
				detail = "view zv depends on sequence s\nview av depends on view zv\n" + detail
			}
			execSQL(t, source, "CREATE TABLE z(a INT CONSTRAINT cz CHECK(nextval('s')>0) NO INHERIT)")
			execSQL(t, source, "CREATE TABLE a(a INT CONSTRAINT ca CHECK(nextval('s')>0))")
			if mixed {
				execSQL(t, source, "ALTER SEQUENCE s OWNED BY z.a")
				// This separate table-backed chain exercises replay topology too.
				execSQL(t, source, "CREATE VIEW zrows AS SELECT a FROM z")
				execSQL(t, source, "CREATE VIEW arows AS SELECT a FROM zrows")
			}
			execSQL(t, source, "INSERT INTO z VALUES(11)")
			execSQL(t, source, "INSERT INTO a VALUES(22)")
			checkReview2OrderRestrict(t, source, detail)
			var dump, again bytes.Buffer
			if err := stuntgres.Dump(ctx, source, &dump); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, source, "SELECT currval('s')", [][]any{{int64(2)}})
			wantTuples(t, source, "SELECT nextval('s')", [][]any{{int64(3)}})
			if !strings.Contains(dump.String(), "NO INHERIT") || mixed && !strings.Contains(dump.String(), `OWNED BY "public"."z"."a"`) {
				t.Fatal("lost real CHECK/ownership definition", dump.String())
			}
			target := openLocal(t, stuntgres.NewDriver(), t.Name()+"_target")
			target.SetMaxOpenConns(1)
			if err := stuntgres.Restore(ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal("valid real definitions/data did not replay", err)
			}
			// Assert the entire independent record, not merely byte stability
			// or intra-table ordinals. RESTRICT must not evaluate providers.
			checkReview2OrderRestrict(t, target, detail)
			wantTuples(t, target, "SELECT a FROM z", [][]any{{int64(11)}})
			wantTuples(t, target, "SELECT a FROM a", [][]any{{int64(22)}})
			if mixed {
				wantTuples(t, target, "SELECT a FROM arows", [][]any{{int64(11)}})
			}
			if err := stuntgres.Dump(ctx, target, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("nondeterministic logical redump", err, dump.String(), again.String())
			}
			wantTuples(t, target, "SELECT nextval('s')", [][]any{{int64(3)}})
			if mixed {
				wantTuples(t, target, "SELECT n FROM av", [][]any{{int64(4)}})
			}
			wantTuples(t, target, "SELECT setval('s',-1)", [][]any{{int64(-1)}})
			checkExecError(t, target, true, "INSERT INTO z VALUES($1)", checkViolation("z", "cz", "33"), 33)
			wantTuples(t, target, "SELECT currval('s')", [][]any{{int64(0)}})
			wantTuples(t, target, "SELECT a FROM z", [][]any{{int64(11)}})
			if mixed {
				// Exercise actual restored ownership and transitive closure, not
				// just their spelling in the dump. The other table/data survive.
				execSQL(t, target, "DROP TABLE z CASCADE")
				for _, q := range []string{"SELECT nextval('s')", "SELECT n FROM zv", "SELECT n FROM av", "SELECT a FROM zrows", "SELECT a FROM arows"} {
					rows, err := target.Query(q)
					if rows != nil {
						rows.Close()
						t.Fatal("removed dependent exposed rows", q)
					}
					sqlState(t, err, "42P01")
				}
				execSQL(t, target, "INSERT INTO a VALUES(33)")
				wantTuples(t, target, "SELECT a FROM a ORDER BY a", [][]any{{int64(22)}, {int64(33)}})
			}
		})
	}
}
