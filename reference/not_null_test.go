//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceNotNullFormsAndAlter(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec("CREATE TABLE t(CONSTRAINT nn NOT NULL a,a INT,b TEXT CONSTRAINT ignored NULL)", 0))
			run(referenceExec("INSERT INTO t VALUES($1,$2)", 1, 7, nil))
			run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}))
			insert := referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}
			for _, q := range []string{"INSERT INTO t VALUES(NULL,'bad')", "INSERT INTO t(b) VALUES('bad')", "INSERT INTO t VALUES(8,'valid'),(NULL,'bad')"} {
				run(referenceImplicitError(q, insert))
			}
			run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}))
			run(referenceExec("ALTER TABLE t ADD CONSTRAINT other NOT NULL a", 0))
			run(referenceImplicitError("ALTER TABLE t DROP CONSTRAINT other", referenceDiagnostic{Code: "42704", Message: `constraint "other" of relation "t" does not exist`}))
			run(referenceExec("CREATE TABLE clone(LIKE t)", 0))
			run(referenceExec("ALTER TABLE ONLY(t) DROP CONSTRAINT nn", 0))
			run(referenceExec("INSERT INTO t VALUES(NULL,'keep')", 1))
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				run(referenceImplicitError(q, referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			}
			run(referenceExec("ALTER TABLE clone DROP CONSTRAINT nn", 0))
			run(referenceExec("INSERT INTO clone VALUES(NULL,NULL)", 1))
			run(referenceExec("CREATE TABLE valid(a INT,b TEXT)", 0))
			run(referenceExec("INSERT INTO valid VALUES(1,'keep')", 1))
			run(referenceExec("ALTER TABLE valid ALTER COLUMN a SET NOT NULL", 0))
			run(referenceExec("ALTER TABLE valid ALTER COLUMN a SET NOT NULL", 0))
			run(referenceExec("ALTER TABLE valid DROP CONSTRAINT valid_a_not_null", 0))
			run(referenceExec("ALTER TABLE valid ALTER a DROP NOT NULL", 0))
			run(referenceExec("INSERT INTO valid VALUES(NULL,NULL)", 1))
			run(referenceImplicitError("ALTER TABLE valid ALTER missing SET NOT NULL", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "valid" does not exist`}))
			run(referenceImplicitError("ALTER TABLE valid ADD NOT NULL ctid", referenceDiagnostic{Code: "0A000", Message: `cannot add not-null constraint on system column "ctid"`}))
		})
	}
}

func TestReferenceNotNullDeclarationsAndNames(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "nested")
	for _, tc := range []struct{ q, code, message, position string }{
		{"CREATE TABLE t (a INT NULL NOT NULL)", "42601", `conflicting NULL/NOT NULL declarations for column "a" of table "t"`, "28"},
		{"CREATE TABLE t (a INT NOT NULL NULL)", "42601", `conflicting NULL/NOT NULL declarations for column "a" of table "t"`, "32"},
		{"CREATE TABLE t (a INT CONSTRAINT x NOT NULL CONSTRAINT y NOT NULL)", "XX000", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t (a INT CONSTRAINT x NOT NULL, CONSTRAINT y NOT NULL a)", "42601", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t(CONSTRAINT x NOT NULL a,a INT NOT NULL CONSTRAINT y NOT NULL)", "42601", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", "42710", `constraint "nn" for relation "t" already exists`, ""},
		{"CREATE TABLE t(a INT,NOT NULL missing)", "42703", `column "missing" of relation "t" does not exist`, ""},
	} {
		p.step(t, referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position}))
	}
	for _, q := range []string{"CREATE TABLE t(a INT NULL NULL)", "DROP TABLE t", "CREATE TABLE t(a INT NOT NULL NOT NULL)", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t", "CREATE TABLE t(a INT NOT NULL CONSTRAINT nn NOT NULL)", "ALTER TABLE t DROP CONSTRAINT nn", "DROP TABLE t"} {
		p.step(t, referenceExec(q, 0))
	}
	p.step(t, referenceExec("CREATE TABLE occupied(x INT CONSTRAINT t_a_not_null NOT NULL)", 0))
	p.step(t, referenceExec("CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT survivor NOT NULL)", 0))
	p.step(t, referenceExec("ALTER TABLE t DROP a", 0))
	p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT survivor", 0))
	p.step(t, referenceExec("DROP TABLE occupied", 0))
	p.step(t, referenceExec("ALTER TABLE t ALTER b SET NOT NULL", 0))
	p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT t_b_not_null", 0))
	table, column := strings.Repeat("t", 40), strings.Repeat("a", 40)
	first := strings.Repeat("t", 27) + "_" + strings.Repeat("a", 26) + "_not_null"
	retry := strings.Repeat("t", 26) + "_" + strings.Repeat("a", 26) + "_not_null1"
	p.step(t, referenceExec(`CREATE TABLE occupied(x INT CONSTRAINT "`+first+`" NOT NULL)`, 0))
	p.step(t, referenceExec(`CREATE TABLE "`+table+`"("`+column+`" INT NOT NULL)`, 0))
	p.step(t, referenceExec(`ALTER TABLE "`+table+`" DROP CONSTRAINT "`+retry+`"`, 0))
	p.step(t, referenceExec(`CREATE TABLE "`+strings.Repeat("界", 20)+`"("`+strings.Repeat("文", 20)+`" INT NOT NULL)`, 0))
	p.step(t, referenceExec(`ALTER TABLE "`+strings.Repeat("界", 20)+`" DROP CONSTRAINT "`+strings.Repeat("界", 9)+`_`+strings.Repeat("文", 8)+`_not_null"`, 0))
	p.step(t, referenceExec("CREATE SCHEMA nested CREATE TABLE first(x INT CONSTRAINT second_a_not_null NOT NULL) CREATE TABLE second(a INT NOT NULL)", 0))
	p.step(t, referenceExec("ALTER TABLE nested.second DROP CONSTRAINT second_a_not_null1", 0))
}

func TestReferenceNotNullColumnLocalDeclarations(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, name string }{
			{"CREATE TABLE t(a INT NULL,NOT NULL a)", "t_a_not_null"},
			{"CREATE TABLE t(NOT NULL a,a INT NULL)", "t_a_not_null"},
			{"CREATE TABLE t(a INT CONSTRAINT ignored NULL,CONSTRAINT nn NOT NULL a)", "nn"},
			{"CREATE TABLE t(CONSTRAINT nn NOT NULL a,a INT CONSTRAINT ignored NULL)", "nn"},
		} {
			t.Run(tc.q+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferencePair(t)
				run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				run(referenceExec(tc.q, 0))
				s := referenceImplicitError("INSERT INTO t VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
				run(s)
				run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, nil))
				run(referenceExec("INSERT INTO t VALUES($1)", 1, 7))
				run(referenceExec("ALTER TABLE t DROP CONSTRAINT "+tc.name, 0))
				run(referenceExec("INSERT INTO t VALUES($1)", 1, nil))
				run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
			})
		}
		t.Run(fmt.Sprint("conflict", prepared), func(t *testing.T) {
			p := newReferencePair(t)
			s := referenceImplicitError("CREATE TABLE t(CONSTRAINT x NOT NULL a,a INT CONSTRAINT x NOT NULL CONSTRAINT y NOT NULL)", referenceDiagnostic{Code: "XX000", Message: `conflicting not-null constraint names "x" and "y"`})
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
		})
	}
}

func TestReferenceNotNullCreateSystemTarget(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"CREATE TABLE t(a INT,NOT NULL ctid)", "CREATE TABLE t(a INT,CONSTRAINT nn NOT NULL ctid)"} {
			t.Run(q+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferencePair(t)
				p.step(t, referenceExec("CREATE TABLE keeper(v INT)", 0))
				p.step(t, referenceExec("INSERT INTO keeper VALUES(91)", 1))
				s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: `cannot add not-null constraint on system column "ctid"`})
				s.prepared = prepared
				s.errorStage = "execute"
				p.step(t, s)
				s = referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"})
				p.step(t, s)
				p.step(t, referenceSelect("SELECT * FROM keeper", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			})
		}
	}
}

func TestReferenceNotNullCurrentCommandNamesAndCompatibleReuse(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "s", "renamed")
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			for _, tc := range []struct{ q, name string }{
				{"CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL)", "t_a_not_null"},
				{"CREATE TABLE t(NOT NULL a,a INT,b INT CONSTRAINT t_a_not_null NOT NULL)", "t_a_not_null"},
				{"CREATE TABLE t(a INT,b INT,NOT NULL a,CONSTRAINT t_a_not_null NOT NULL b)", "t_a_not_null"},
				{`CREATE TABLE "t""é\\x"(a INT NOT NULL,b INT CONSTRAINT "t""é\\x_a_not_null" NOT NULL)`, `t"é\\x_a_not_null`},
			} {
				referenceNotNullNameCollision(t, context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), p.memory, prepared, tc.q, tc.name)
				referenceNotNullNameCollision(t, context.WithValue(p.ctx, referenceRecorderKey{}, p.postgresRecord), p.postgres, prepared, tc.q, tc.name)
			}
			for _, tc := range []struct{ q, name string }{{"SELECT * FROM t", "t"}, {`SELECT * FROM "t""é\\x"`, `t"é\\x`}} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42P01", Message: `relation "` + tc.name + `" does not exist`, Position: "15"})
				run(s)
			}
			for _, q := range []string{
				"CREATE TABLE t(b INT CONSTRAINT t_a_not_null NOT NULL,a INT NOT NULL)",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null1", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE t(a INT,b INT CONSTRAINT t_a_not_null NOT NULL,NOT NULL a)",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null1", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL,CONSTRAINT nn NOT NULL a)",
				"ALTER TABLE t DROP CONSTRAINT nn", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE compatible(a INT CONSTRAINT x NOT NULL,b INT CONSTRAINT y NOT NULL)",
				"ALTER TABLE compatible ADD CONSTRAINT y NOT NULL a", "ALTER TABLE compatible DROP CONSTRAINT x",
				"CREATE SCHEMA s CREATE TABLE named(a INT NOT NULL)", "CREATE TABLE named(a INT NOT NULL)",
				"ALTER SCHEMA s RENAME TO renamed", "ALTER TABLE renamed.named DROP CONSTRAINT named_a_not_null",
				"ALTER TABLE named DROP CONSTRAINT named_a_not_null",
				"CREATE TABLE source(prefix INT,a INT CONSTRAINT nn NOT NULL)", "ALTER TABLE source DROP prefix",
				"CREATE TABLE clone(lead TEXT,LIKE source)", "DROP TABLE source", "ALTER TABLE clone DROP CONSTRAINT nn",
			} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO compatible VALUES(NULL,7)", 1))
			run(referenceExec("INSERT INTO clone VALUES('fresh',NULL)", 1))
			run(referenceSelect("SELECT * FROM clone", []string{"lead", "a"}, []string{"TEXT", "INT4"}, [][]any{{"fresh", nil}}))
			run(referenceExec("ALTER TABLE compatible DROP CONSTRAINT y", 0))
		})
	}
}

func TestReferenceNotNullLikeConstraintProcessingOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "src")
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec("CREATE SCHEMA src CREATE TABLE source(z INT CONSTRAINT clone_a_not_null NOT NULL)", 0))
			q := "CREATE TABLE clone(a INT NOT NULL,LIKE src.source)"
			referenceNotNullNameCollision(t, context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), p.memory, prepared, q, "clone_a_not_null")
			referenceNotNullNameCollision(t, context.WithValue(p.ctx, referenceRecorderKey{}, p.postgresRecord), p.postgres, prepared, q, "clone_a_not_null")
			for _, q := range []string{
				"CREATE TABLE clone(LIKE src.source,a INT NOT NULL)",
				"ALTER TABLE clone DROP CONSTRAINT clone_a_not_null1", "ALTER TABLE clone DROP CONSTRAINT clone_a_not_null", "DROP TABLE clone",
				"CREATE TABLE src.left_source(z INT CONSTRAINT z_copy NOT NULL,a INT CONSTRAINT a_copy NOT NULL)",
				"CREATE TABLE src.right_source(w INT CONSTRAINT z_copy NOT NULL,y INT CONSTRAINT a_copy NOT NULL)",
			} {
				run(referenceExec(q, 0))
			}
			s := referenceImplicitError("CREATE TABLE clone(LIKE src.left_source,LIKE src.right_source)", referenceDiagnostic{Code: "42710", Message: `constraint "a_copy" for relation "clone" already exists`})
			s.errorStage = "execute"
			run(s)
			s = referenceImplicitError("SELECT * FROM clone", referenceDiagnostic{Code: "42P01", Message: `relation "clone" does not exist`, Position: "15"})
			run(s)
			run(referenceSelect("SELECT * FROM src.left_source", []string{"z", "a"}, []string{"INT4", "INT4"}, nil))
			run(referenceSelect("SELECT * FROM src.right_source", []string{"w", "y"}, []string{"INT4", "INT4"}, nil))
		})
	}
}

func referenceNotNullNameCollision(t *testing.T, ctx context.Context, db *sql.DB, prepared bool, q, name string) {
	t.Helper()
	var err error
	if prepared {
		stmt, prepareErr := db.PrepareContext(ctx, q)
		if prepareErr != nil {
			t.Fatal("utility Prepare must succeed", prepareErr)
		}
		defer stmt.Close()
		_, err = stmt.ExecContext(ctx)
	} else {
		_, err = db.ExecContext(ctx, q)
	}
	got := referenceExtractDiagnostic(t, err)
	const prefix = "Key (conrelid, contypid, conname)=("
	suffix := ", 0, " + name + ") already exists."
	token, ok := strings.CutPrefix(got.Detail, prefix)
	if !ok {
		t.Fatal(got)
	}
	token, ok = strings.CutSuffix(token, suffix)
	oid, parseErr := strconv.ParseUint(token, 10, 32)
	if !ok || parseErr != nil || oid == 0 || strconv.FormatUint(oid, 10) != token {
		t.Fatal(got)
	}
	// The two real allocators need not choose equal IDs. Normalize only the
	// independently validated decimal token, retaining every other logical field.
	got.Detail = prefix + "<OID>" + suffix
	want := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
		Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`,
		Detail:  prefix + "<OID>" + suffix, Schema: "pg_catalog", Table: "pg_constraint",
		Constraint: "pg_constraint_conrelid_contypid_conname_index",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
}

func TestReferenceNotNullAbsenceAnalysisPhases(t *testing.T) {
	p := newReferencePair(t)
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			s := referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"})
			s.prepared, s.selectRows = prepared, queryRows
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
		s := referenceImplicitError("ALTER TABLE t ALTER a SET NOT NULL", referenceDiagnostic{Code: "42P01", Message: `relation "t" does not exist`})
		s.prepared, s.errorStage = prepared, "execute"
		p.step(t, s)
	}
}
