package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNotNullFormsAndAlter(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) {
				s.Prepared = prepared
				if s.Diagnostic != nil && !s.SelectRows {
					s.ErrorStage = "execute"
				}
				p.Step(t, s)
			}
			run(fixture.Exec("CREATE TABLE t(CONSTRAINT nn NOT NULL a,a INT,b TEXT CONSTRAINT ignored NULL)", 0))
			p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, 7, nil)))
			for _, b := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, true}, 1)
			}
			run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}))
			insert := fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}
			for _, q := range []string{"INSERT INTO t VALUES(NULL,'bad')", "INSERT INTO t(b) VALUES('bad')", "INSERT INTO t VALUES(8,'valid'),(NULL,'bad')"} {
				run(fixture.ImplicitError(q, insert))
			}
			run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}}))
			run(fixture.ImplicitError("ALTER TABLE t ADD CONSTRAINT other NOT NULL a", fixture.Diagnostic{Code: "55000", Message: `cannot create not-null constraint "other" on column "a" of table "t"`, Detail: `A not-null constraint named "nn" already exists for this column.`}))
			run(fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT other", fixture.Diagnostic{Code: "42704", Message: `constraint "other" of relation "t" does not exist`}))
			run(fixture.Exec("CREATE TABLE clone(LIKE t)", 0))
			run(fixture.Exec("ALTER TABLE ONLY(t) DROP CONSTRAINT nn", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL,'keep')", 1))
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
				run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}, {nil, "keep"}}))
				for _, b := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, 2)
				}
			}
			run(fixture.Exec("ALTER TABLE clone DROP CONSTRAINT nn", 0))
			run(fixture.Exec("INSERT INTO clone VALUES(NULL,NULL)", 1))
			run(fixture.Exec("CREATE TABLE valid(a INT,b TEXT)", 0))
			run(fixture.Exec("INSERT INTO valid VALUES(1,'keep')", 1))
			for _, b := range p.Backends() {
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
				stmt, err := b.DB.PrepareContext(ctx, "SELECT * FROM valid")
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer stmt.Close()
					s := fixture.Exec("ALTER TABLE valid ALTER COLUMN a SET NOT NULL", 0)
					s.Prepared = prepared
					fixture.RunStep(t, ctx, b.DB, b.Recorder, s)
					rows, err := stmt.QueryContext(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLColumnTypes(t, b.PG, columns, []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, true})
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
				}()
			}
			run(fixture.Exec("ALTER TABLE valid ALTER COLUMN a SET NOT NULL", 0))
			run(fixture.Exec("ALTER TABLE valid ALTER COLUMN a SET NOT NULL", 0))
			run(fixture.Exec("ALTER TABLE valid DROP CONSTRAINT valid_a_not_null", 0))
			run(fixture.Exec("ALTER TABLE valid ALTER a DROP NOT NULL", 0))
			run(fixture.Exec("INSERT INTO valid VALUES(NULL,NULL)", 1))
			run(fixture.ImplicitError("ALTER TABLE valid ALTER missing SET NOT NULL", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "valid" does not exist`}))
			run(fixture.ImplicitError("ALTER TABLE valid ADD NOT NULL ctid", fixture.Diagnostic{Code: "0A000", Message: `cannot add not-null constraint on system column "ctid"`}))
		})
	}
}

func TestReferenceNotNullDeclarationsAndNames(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "nested")
	for _, tc := range []struct{ q, code, message, position string }{
		{"CREATE TABLE t (a INT NULL NOT NULL)", "42601", `conflicting NULL/NOT NULL declarations for column "a" of table "t"`, "28"},
		{"CREATE TABLE t (a INT NOT NULL NULL)", "42601", `conflicting NULL/NOT NULL declarations for column "a" of table "t"`, "32"},
		{"CREATE TABLE t (a INT CONSTRAINT x NOT NULL CONSTRAINT y NOT NULL)", "XX000", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t (a INT CONSTRAINT x NOT NULL, CONSTRAINT y NOT NULL a)", "42601", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t(CONSTRAINT x NOT NULL a,a INT NOT NULL CONSTRAINT y NOT NULL)", "42601", `conflicting not-null constraint names "x" and "y"`, ""},
		{"CREATE TABLE t(a INT,b INT,CONSTRAINT nn NOT NULL a,CONSTRAINT nn NOT NULL b)", "42710", `constraint "nn" for relation "t" already exists`, ""},
		{"CREATE TABLE t(a INT,NOT NULL missing)", "42703", `column "missing" of relation "t" does not exist`, ""},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
		s.ErrorStage = "execute"
		p.Step(t, s)
	}
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
	p.Step(t, fixture.Exec("DROP TABLE t", 0))
	for _, q := range []string{"CREATE TABLE t(a INT NULL NULL)", "DROP TABLE t", "CREATE TABLE t(a INT NOT NULL NOT NULL)", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t", "CREATE TABLE t(a INT NOT NULL CONSTRAINT nn NOT NULL)", "ALTER TABLE t DROP CONSTRAINT nn", "DROP TABLE t"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("CREATE TABLE occupied(x INT CONSTRAINT t_a_not_null NOT NULL)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT survivor NOT NULL)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE clone(LIKE t)", 0))
	p.Step(t, fixture.Exec("ALTER TABLE t DROP a", 0))
	p.Step(t, fixture.Exec("ALTER TABLE t DROP CONSTRAINT survivor", 0))
	p.Step(t, fixture.Exec("ALTER TABLE clone DROP CONSTRAINT t_a_not_null1", 0))
	p.Step(t, fixture.Exec("INSERT INTO clone VALUES(NULL,2)", 1))
	p.Step(t, fixture.Select("SELECT * FROM clone", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(2)}}))
	p.Step(t, fixture.Exec("ALTER TABLE clone DROP CONSTRAINT survivor", 0))
	p.Step(t, fixture.Exec("DROP TABLE occupied", 0))
	p.Step(t, fixture.Exec("ALTER TABLE t ALTER b SET NOT NULL", 0))
	p.Step(t, fixture.Exec("ALTER TABLE t DROP CONSTRAINT t_b_not_null", 0))
	table, column := strings.Repeat("t", 40), strings.Repeat("a", 40)
	first := strings.Repeat("t", 27) + "_" + strings.Repeat("a", 26) + "_not_null"
	retry := strings.Repeat("t", 26) + "_" + strings.Repeat("a", 26) + "_not_null1"
	p.Step(t, fixture.Exec(`CREATE TABLE occupied(x INT CONSTRAINT "`+first+`" NOT NULL)`, 0))
	p.Step(t, fixture.Exec(`CREATE TABLE "`+table+`"("`+column+`" INT NOT NULL)`, 0))
	p.Step(t, fixture.Exec(`ALTER TABLE "`+table+`" DROP CONSTRAINT "`+retry+`"`, 0))
	p.Step(t, fixture.Exec(`CREATE TABLE "`+strings.Repeat("界", 20)+`"("`+strings.Repeat("文", 20)+`" INT NOT NULL)`, 0))
	p.Step(t, fixture.Exec(`ALTER TABLE "`+strings.Repeat("界", 20)+`" DROP CONSTRAINT "`+strings.Repeat("界", 9)+`_`+strings.Repeat("文", 8)+`_not_null"`, 0))
	p.Step(t, fixture.Exec("CREATE SCHEMA nested CREATE TABLE first(x INT CONSTRAINT second_a_not_null NOT NULL) CREATE TABLE second(a INT NOT NULL)", 0))
	p.Step(t, fixture.Exec("ALTER TABLE nested.second DROP CONSTRAINT second_a_not_null1", 0))
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
				p := fixture.NewScenario(t)
				run := func(s fixture.Step) {
					s.Prepared = prepared
					if s.Diagnostic != nil && !s.SelectRows {
						s.ErrorStage = "execute"
					}
					p.Step(t, s)
				}
				run(fixture.Exec(tc.q, 0))
				for _, b := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 0)
				}
				s := fixture.ImplicitError("INSERT INTO t VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
				run(s)
				run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, nil))
				run(fixture.Exec("INSERT INTO t VALUES($1)", 1, 7))
				for _, b := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
				}
				run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT "+tc.name, 0))
				run(fixture.Exec("INSERT INTO t VALUES($1)", 1, nil))
				for _, b := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 2)
				}
				run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
			})
		}
		t.Run(fmt.Sprint("conflict", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			s := fixture.ImplicitError("CREATE TABLE t(CONSTRAINT x NOT NULL a,a INT CONSTRAINT x NOT NULL CONSTRAINT y NOT NULL)", fixture.Diagnostic{Code: "XX000", Message: `conflicting not-null constraint names "x" and "y"`})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			p.Step(t, s)
			absent := fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"})
			absent.SelectRows = true
			p.Step(t, absent)
		})
	}
}

func TestReferenceNotNullCreateSystemTarget(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"CREATE TABLE t(a INT,NOT NULL ctid)", "CREATE TABLE t(a INT,CONSTRAINT nn NOT NULL ctid)"} {
			t.Run(q+fmt.Sprint(prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec("CREATE TABLE keeper(v INT)", 0))
				p.Step(t, fixture.Exec("INSERT INTO keeper VALUES(91)", 1))
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: `cannot add not-null constraint on system column "ctid"`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				p.Step(t, s)
				s = fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"})
				s.SelectRows = true
				p.Step(t, s)
				p.Step(t, fixture.Select("SELECT * FROM keeper", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			})
		}
	}
}

func TestReferenceNotNullCurrentCommandNamesAndCompatibleReuse(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "s", "renamed")
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			for _, tc := range []struct{ q, name string }{
				{"CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL)", "t_a_not_null"},
				{"CREATE TABLE t(NOT NULL a,a INT,b INT CONSTRAINT t_a_not_null NOT NULL)", "t_a_not_null"},
				{"CREATE TABLE t(a INT,b INT,NOT NULL a,CONSTRAINT t_a_not_null NOT NULL b)", "t_a_not_null"},
				{`CREATE TABLE "t""é\\x"(a INT NOT NULL,b INT CONSTRAINT "t""é\\x_a_not_null" NOT NULL)`, `t"é\\x_a_not_null`},
			} {
				for _, b := range p.Backends() {
					referenceNotNullNameCollision(t, context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), b.DB, prepared, tc.q, tc.name)
				}
			}
			for _, tc := range []struct{ q, name string }{{"SELECT * FROM t", "t"}, {`SELECT * FROM "t""é\\x"`, `t"é\\x`}} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P01", Message: `relation "` + tc.name + `" does not exist`, Position: "15"})
				s.SelectRows = true
				if prepared {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				run(s)
			}
			for _, q := range []string{
				"CREATE TABLE t(b INT CONSTRAINT t_a_not_null NOT NULL,a INT NOT NULL)",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null1", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE t(a INT,b INT CONSTRAINT t_a_not_null NOT NULL,NOT NULL a)",
				"ALTER TABLE t DROP CONSTRAINT t_a_not_null1", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL,CONSTRAINT nn NOT NULL a)",
				"ALTER TABLE t DROP CONSTRAINT nn", "ALTER TABLE t DROP CONSTRAINT t_a_not_null", "DROP TABLE t",
				"CREATE SCHEMA s CREATE TABLE named(a INT NOT NULL)", "CREATE TABLE named(a INT NOT NULL)",
				"ALTER SCHEMA s RENAME TO renamed", "ALTER TABLE renamed.named DROP CONSTRAINT named_a_not_null",
				"ALTER TABLE named DROP CONSTRAINT named_a_not_null",
				"CREATE TABLE source(prefix INT,a INT CONSTRAINT nn NOT NULL)", "ALTER TABLE source DROP prefix",
				"CREATE TABLE clone(lead TEXT,LIKE source)", "DROP TABLE source", "ALTER TABLE clone DROP CONSTRAINT nn",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("CREATE TABLE compatible(a INT CONSTRAINT x NOT NULL,b INT CONSTRAINT y NOT NULL)", 0))
			step := fixture.ImplicitError("ALTER TABLE compatible ADD CONSTRAINT y NOT NULL a", fixture.Diagnostic{Code: "55000", Message: `cannot create not-null constraint "y" on column "a" of table "compatible"`, Detail: `A not-null constraint named "x" already exists for this column.`})
			step.ErrorStage = "execute"
			run(step)
			for _, b := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM compatible", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{false, false}, 0)
			}
			run(fixture.Exec("ALTER TABLE compatible DROP CONSTRAINT x", 0))
			run(fixture.Exec("INSERT INTO compatible VALUES(NULL,7)", 1))
			run(fixture.Exec("INSERT INTO clone VALUES('fresh',NULL)", 1))
			run(fixture.Select("SELECT * FROM clone", []string{"lead", "a"}, []string{"TEXT", "INT4"}, [][]any{{"fresh", nil}}))
			run(fixture.Exec("ALTER TABLE compatible DROP CONSTRAINT y", 0))
		})
	}
}

func TestReferenceNotNullLikeConstraintProcessingOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "src")
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec("CREATE SCHEMA src CREATE TABLE source(z INT CONSTRAINT clone_a_not_null NOT NULL)", 0))
			q := "CREATE TABLE clone(a INT NOT NULL,LIKE src.source)"
			for _, b := range p.Backends() {
				referenceNotNullNameCollision(t, context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), b.DB, prepared, q, "clone_a_not_null")
			}
			for _, q := range []string{
				"CREATE TABLE clone(LIKE src.source,a INT NOT NULL)",
				"ALTER TABLE clone DROP CONSTRAINT clone_a_not_null1", "ALTER TABLE clone DROP CONSTRAINT clone_a_not_null", "DROP TABLE clone",
				"CREATE TABLE src.left_source(z INT CONSTRAINT z_copy NOT NULL,a INT CONSTRAINT a_copy NOT NULL)",
				"CREATE TABLE src.right_source(w INT CONSTRAINT z_copy NOT NULL,y INT CONSTRAINT a_copy NOT NULL)",
			} {
				run(fixture.Exec(q, 0))
			}
			s := fixture.ImplicitError("CREATE TABLE clone(LIKE src.left_source,LIKE src.right_source)", fixture.Diagnostic{Code: "42710", Message: `constraint "a_copy" for relation "clone" already exists`})
			s.ErrorStage = "execute"
			run(s)
			s = fixture.ImplicitError("SELECT * FROM clone", fixture.Diagnostic{Code: "42P01", Message: `relation "clone" does not exist`, Position: "15"})
			s.SelectRows = true
			if prepared {
				s.ErrorStage = "prepare"
			} else {
				s.ErrorStage = "execute"
			}
			run(s)
			run(fixture.Select("SELECT * FROM src.left_source", []string{"z", "a"}, []string{"INT4", "INT4"}, nil))
			run(fixture.Select("SELECT * FROM src.right_source", []string{"w", "y"}, []string{"INT4", "INT4"}, nil))
			for _, b := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM src.left_source", []string{"z", "a"}, []string{"INT4", "INT4"}, []bool{false, false}, 0)
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM src.right_source", []string{"w", "y"}, []string{"INT4", "INT4"}, []bool{false, false}, 0)
			}
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
	referenceNotNullNameCollisionDiagnostic(t, err, name)
}

func referenceNotNullNameCollisionDiagnostic(t *testing.T, err error, name string) {
	t.Helper()
	got := fixture.ExtractDiagnostic(t, err)
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
	want := fixture.Diagnostic{
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
	p := fixture.NewScenario(t)
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"})
			s.Prepared, s.SelectRows = prepared, queryRows
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
		}
		s := fixture.ImplicitError("ALTER TABLE t ALTER a SET NOT NULL", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`})
		s.Prepared, s.ErrorStage = prepared, "execute"
		p.Step(t, s)
	}
}
