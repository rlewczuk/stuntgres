package stuntgres_test

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestNotNullCurrentCommandNamesAndCompatibleReuse(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			exec := func(q string) { tableCoreExec(t, db, prepared, q, 0) }
			for _, tc := range []struct{ q, name string }{
				{"CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL)", "t_a_not_null"},
				{"CREATE TABLE t(NOT NULL a,a INT,b INT CONSTRAINT t_a_not_null NOT NULL)", "t_a_not_null"},
				{"CREATE TABLE t(a INT,b INT,NOT NULL a,CONSTRAINT t_a_not_null NOT NULL b)", "t_a_not_null"},
				{`CREATE TABLE "t""é\\x"(a INT NOT NULL,b INT CONSTRAINT "t""é\\x_a_not_null" NOT NULL)`, `t"é\\x_a_not_null`},
			} {
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(tc.q)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(tc.q)
				}
				notNullNameCollisionDiagnostic(t, err, tc.name)
			}
			for _, tc := range []struct{ q, name string }{{"SELECT * FROM t", "t"}, {`SELECT * FROM "t""é\\x"`, `t"é\\x`}} {
				_, err := db.Query(tc.q)
				notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "` + tc.name + `" does not exist`, Position: "15"})
			}
			for _, q := range []string{
				"CREATE TABLE t(b INT CONSTRAINT t_a_not_null NOT NULL,a INT NOT NULL)",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null1",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE t(a INT,b INT CONSTRAINT t_a_not_null NOT NULL,NOT NULL a)",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null1",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL,CONSTRAINT nn NOT NULL a)",
				"ALTER TABLE t DROP CONSTRAINT nn",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE compatible(a INT CONSTRAINT x NOT NULL,b INT CONSTRAINT y NOT NULL)",
				"ALTER TABLE compatible ADD CONSTRAINT y NOT NULL a",
				"ALTER TABLE compatible DROP CONSTRAINT x",
				"CREATE SCHEMA s CREATE TABLE named(a INT NOT NULL)",
				"CREATE TABLE named(a INT NOT NULL)", "ALTER SCHEMA s RENAME TO renamed",
				"ALTER TABLE renamed.named DROP CONSTRAINT named_a_not_null",
				"ALTER TABLE named DROP CONSTRAINT named_a_not_null",
				"CREATE TABLE source(prefix INT,a INT CONSTRAINT nn NOT NULL)",
				"ALTER TABLE source DROP prefix", "CREATE TABLE clone(lead TEXT,LIKE source)",
				"DROP TABLE source", "ALTER TABLE clone DROP CONSTRAINT nn",
			} {
				exec(q)
			}
			tableCoreExec(t, db, prepared, "INSERT INTO compatible VALUES(NULL,7)", 1)
			exec("ALTER TABLE compatible DROP CONSTRAINT y")
			tableCoreExec(t, db, prepared, "INSERT INTO clone VALUES('fresh',NULL)", 1)
			wantTuples(t, db, "SELECT * FROM clone", [][]any{{"fresh", nil}})
		})
	}
}

func TestNotNullLikeConstraintProcessingOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			exec := func(q string) { tableCoreExec(t, db, prepared, q, 0) }
			exec("CREATE SCHEMA src CREATE TABLE source(z INT CONSTRAINT clone_a_not_null NOT NULL)")
			q := "CREATE TABLE clone(a INT NOT NULL,LIKE src.source)"
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = db.Exec(q)
			}
			notNullNameCollisionDiagnostic(t, err, "clone_a_not_null")
			exec("CREATE TABLE clone(LIKE src.source,a INT NOT NULL)")
			exec("ALTER TABLE clone DROP CONSTRAINT clone_a_not_null1")
			exec("ALTER TABLE clone DROP CONSTRAINT clone_a_not_null")
			exec("DROP TABLE clone")
			exec("CREATE TABLE src.left_source(z INT CONSTRAINT z_copy NOT NULL,a INT CONSTRAINT a_copy NOT NULL)")
			exec("CREATE TABLE src.right_source(w INT CONSTRAINT z_copy NOT NULL,y INT CONSTRAINT a_copy NOT NULL)")
			q = "CREATE TABLE clone(LIKE src.left_source,LIKE src.right_source)"
			if prepared {
				stmt, prepareErr := db.Prepare(q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = db.Exec(q)
			}
			notNullDiagnostic(t, err, stuntgres.Error{Code: "42710", Message: `constraint "a_copy" for relation "clone" already exists`})
			_, err = db.Query("SELECT * FROM clone")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "clone" does not exist`, Position: "15"})
			tableCoreMetadata(t, db, prepared, "SELECT * FROM src.left_source", []string{"z", "a"}, []string{"INT4", "INT4"}, []bool{false, false})
			tableCoreMetadata(t, db, prepared, "SELECT * FROM src.right_source", []string{"w", "y"}, []string{"INT4", "INT4"}, []bool{false, false})
		})
	}
}

// Only the allocator-dependent relation OID is normalized; the full record and
// every fixed component of the physical uniqueness diagnostic remain asserted.
func notNullNameCollisionDiagnostic(t *testing.T, err error, name string) {
	t.Helper()
	var got *stuntgres.Error
	if !errors.As(err, &got) {
		t.Fatal(err)
	}
	const prefix = "Key (conrelid, contypid, conname)=("
	suffix := ", 0, " + name + ") already exists."
	token, ok := strings.CutPrefix(got.Detail, prefix)
	if !ok {
		t.Fatal(got.Detail)
	}
	token, ok = strings.CutSuffix(token, suffix)
	oid, parseErr := strconv.ParseUint(token, 10, 32)
	if !ok || parseErr != nil || oid == 0 || strconv.FormatUint(oid, 10) != token {
		t.Fatal(got.Detail)
	}
	normalized := *got
	normalized.Detail = prefix + "<OID>" + suffix
	notNullDiagnostic(t, &normalized, stuntgres.Error{
		Code: "23505", Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`,
		Detail: prefix + "<OID>" + suffix, Schema: "pg_catalog", Table: "pg_constraint",
		Constraint: "pg_constraint_conrelid_contypid_conname_index",
	})
}
