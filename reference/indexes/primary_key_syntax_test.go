package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePrimaryKeySyntaxNames(t *testing.T) {
	for _, c := range []struct{ name, ddl, key string }{
		{"column", "CREATE TABLE t(a INT PRIMARY KEY,b TEXT)", "t_pkey"},
		{"named_column", "CREATE TABLE t(a INT CONSTRAINT chosen PRIMARY KEY,b TEXT)", "chosen"},
		{"table_scalar", "CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(a))", "t_pkey"},
		{"named_table", "CREATE TABLE t(a INT,b TEXT,CONSTRAINT chosen PRIMARY KEY(a))", "chosen"},
		{"quoted", `CREATE TABLE t(a INT,b TEXT,CONSTRAINT "κλειδί" PRIMARY KEY(a))`, "κλειδί"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Prepared(fixture.Exec(c.ddl, 0)))
				fixture.PrimaryKeyReferenceIDs(t, p, b, c.key)
				run(fixture.Select("SELECT contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=$1", []string{"contype", "pg_get_constraintdef"}, []string{"CHAR", "TEXT"}, [][]any{{"p", "PRIMARY KEY (a)"}}, c.key))
				run(fixture.Exec("INSERT INTO t VALUES(1,'seed')", 1))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'bad')", fixture.UniqueReferenceDuplicate("t", c.key, "Key (a)=(1) already exists.")))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL,'bad')", fixture.PrimaryKeyReferenceNull("t", "a", "null, bad")))
				run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "seed"}}))
			})
		})
	}
}

func TestReferencePrimaryKeyInvalidDeclarations(t *testing.T) {
	// Character positions and complete diagnostics were confirmed against pinned
	// PostgreSQL18.0; positions are counted from each literal SQL input.
	for _, c := range []struct{ name, sql, code, message, token string }{
		{"multiple_column", "CREATE TABLE t(a INT PRIMARY KEY,b INT PRIMARY KEY)", "42P16", `multiple primary keys for table "t" are not allowed`, "PRIMARY KEY)"},
		{"multiple_identical", "CREATE TABLE t(a INT,PRIMARY KEY(a),PRIMARY KEY(a))", "42P16", `multiple primary keys for table "t" are not allowed`, "PRIMARY KEY(a))"},
		{"multiple_before_binding", "CREATE TABLE t(a INT,PRIMARY KEY(a),PRIMARY KEY(missing))", "42P16", `multiple primary keys for table "t" are not allowed`, "PRIMARY KEY(missing)"},
		{"missing", "CREATE TABLE t(a INT,PRIMARY KEY(missing))", "42703", `column "missing" named in key does not exist`, "PRIMARY"},
		{"repeated", "CREATE TABLE t(a INT,PRIMARY KEY(a,a))", "42701", `column "a" appears twice in primary key constraint`, "PRIMARY"},
		{"named_missing", "CREATE TABLE t(a INT,CONSTRAINT named PRIMARY KEY(missing))", "42703", `column "missing" named in key does not exist`, "CONSTRAINT"},
		{"unicode_missing", `CREATE TABLE "表"(a INT,PRIMARY KEY(missing))`, "42703", `column "missing" named in key does not exist`, "PRIMARY"},
		{"duplicate_column", "CREATE TABLE t(a INT,a INT PRIMARY KEY)", "42701", `column "a" specified more than once`, ""},
		{"empty", "CREATE TABLE t(a INT,PRIMARY KEY())", "42601", `syntax error at or near ")"`, "))"},
		{"trailing_comma", "CREATE TABLE t(a INT,PRIMARY KEY(a,))", "42601", `syntax error at or near ")"`, "))"},
		{"expression", "CREATE TABLE t(a INT,PRIMARY KEY((a)))", "42601", `syntax error at or near "("`, "(a)))"},
		{"nulls_policy", "CREATE TABLE t(a INT,PRIMARY KEY NULLS NOT DISTINCT(a))", "42601", `syntax error at or near "NULLS"`, "NULLS"},
		{"malformed_suffix", "CREATE TABLE t(a INT PRIMARY KEY); nonsense", "42601", `syntax error at or near "nonsense"`, "nonsense"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", c.name, prepared), func(t *testing.T) {
				fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					want := fixture.ControlsReferenceDiagnostic(c.code, c.message)
					if c.token != "" {
						want.Position = fixture.Position(utf8.RuneCountInString(c.sql[:strings.Index(c.sql, c.token)]) + 1)
					}
					s := fixture.ErrorDiagnostic(c.sql, want)
					s.Prepared = prepared
					// Syntax errors are detected at Prepare; CREATE utility
					// semantic validation is deferred to Execute in both modes.
					s.ErrorStage = "execute"
					if prepared && c.code == "42601" {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relname='t' OR relname='t_pkey' OR relname='named' OR relname='表'", []string{"relname"}, []string{"NAME"}, nil))
				})
			})
		}
	}
}

func TestReferencePrimaryKeyLocalNullability(t *testing.T) {
	for _, c := range []struct{ name, ddl, conflict string }{
		{"null_then_column_pk", "CREATE TABLE t(a INT NULL PRIMARY KEY)", "PRIMARY"},
		{"column_pk_then_null", "CREATE TABLE t(a INT PRIMARY KEY NULL)", "NULL"},
		{"named_column_pk", "CREATE TABLE t(a INT NULL CONSTRAINT pk PRIMARY KEY)", "CONSTRAINT"},
		{"table_null", "CREATE TABLE t(a INT NULL,PRIMARY KEY(a))", ""},
		{"table_first_null", "CREATE TABLE t(PRIMARY KEY(a),a INT NULL)", ""},
		{"explicit_nn", "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL CONSTRAINT pk PRIMARY KEY)", ""},
		{"pk_before_explicit_nn", "CREATE TABLE t(a INT CONSTRAINT pk PRIMARY KEY CONSTRAINT nn NOT NULL)", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				if c.conflict != "" {
					want := fixture.ControlsReferenceDiagnostic("42601", `conflicting NULL/NOT NULL declarations for column "a" of table "t"`)
					want.Position = fixture.Position(strings.Index(c.ddl, c.conflict) + 1)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(c.ddl, want)
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
					}
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='t' OR relname='pk' OR relname='t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
					return
				}
				run(fixture.Exec(c.ddl, 0))
				nn, pk := "t_a_not_null", "t_pkey"
				if strings.Contains(c.ddl, "CONSTRAINT nn") {
					nn, pk = "nn", "pk"
				}
				rows := [][]any{{nn, "n", "NOT NULL a", nil}, {pk, "p", "PRIMARY KEY (a)", nil}}
				if nn > pk {
					rows[0], rows[1] = rows[1], rows[0]
				}
				run(fixture.PrimaryKeyReferenceCatalog("t", rows))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL)", fixture.PrimaryKeyReferenceNull("t", "a", "null")))
				run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, "SELECT a FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
			})
		})
	}
}

func TestReferencePrimaryKeyNotNullCreationOrder(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(z INT NOT NULL,b INT,a INT,PRIMARY KEY(a,b))", 0))
		ids := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
		// Existing explicit NN precedes appended PK-generated NN; PK key order
		// (a,b), not physical column order (b,a), determines those additions.
		// Assert relative allocation order, never fixed release allocator values.
		run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1 ORDER BY oid", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"t_z_not_null", "n", "NOT NULL z"}, {"t_a_not_null", "n", "NOT NULL a"}, {"t_b_not_null", "n", "NOT NULL b"}, {"t_pkey", "p", "PRIMARY KEY (a, b)"}}, ids[1]))
	})
}
