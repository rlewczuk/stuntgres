package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueDiagnostics(t *testing.T) {
	cases := []struct {
		name, setup, query string
		want               fixture.Diagnostic
		relations          [][]any
	}{
		{"missing", "", "CREATE TABLE t(a INT,UNIQUE(x))", fixture.Diagnostic{Code: "42703", Message: "column \"x\" named in key does not exist", Position: "22"}, nil},
		{"repeated", "", "CREATE TABLE t(a INT,UNIQUE(a,a))", fixture.Diagnostic{Code: "42701", Message: "column \"a\" appears twice in unique constraint", Position: "22"}, nil},
		{"empty", "", "CREATE TABLE t(a INT,UNIQUE())", fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \")\"", Position: "29"}, nil},
		{"unicode_missing", "", "CREATE TABLE t(é INT,CONSTRAINT u UNIQUE(x))", fixture.Diagnostic{Code: "42703", Message: "column \"x\" named in key does not exist", Position: "22"}, nil},
		{"duplicate_u", "", "CREATE TABLE t(a INT,b INT,CONSTRAINT u UNIQUE(a),CONSTRAINT u UNIQUE(b))", fixture.Diagnostic{Code: "42P07", Message: "relation \"u\" already exists"}, nil},
		{"check_before_unique", "", "CREATE TABLE t(a INT,CONSTRAINT u CHECK(a>0),CONSTRAINT u UNIQUE(a))", fixture.Diagnostic{Code: "42710", Message: "constraint \"u\" for relation \"t\" already exists"}, nil},
		{"unique_before_check", "", "CREATE TABLE t(a INT,CONSTRAINT u UNIQUE(a),CONSTRAINT u CHECK(a>0))", fixture.Diagnostic{Code: "42710", Message: "constraint \"u\" for relation \"t\" already exists"}, nil},
		{"not_null_before_unique", "", "CREATE TABLE t(a INT CONSTRAINT u NOT NULL,CONSTRAINT u UNIQUE(a))", fixture.Diagnostic{Code: "42710", Message: "constraint \"u\" for relation \"t\" already exists"}, nil},
		{"unique_before_not_null", "", "CREATE TABLE t(a INT CONSTRAINT u UNIQUE CONSTRAINT u NOT NULL)", fixture.Diagnostic{Code: "42710", Message: "constraint \"u\" for relation \"t\" already exists"}, nil},
		{"existing_table", "CREATE TABLE u(a INT)", "CREATE TABLE t(a INT CONSTRAINT u UNIQUE)", fixture.Diagnostic{Code: "42P07", Message: "relation \"u\" already exists"}, [][]any{{"u", "r"}}},
		{"existing_index", "CREATE TABLE other(a INT); CREATE INDEX u ON other(a)", "CREATE TABLE t(a INT CONSTRAINT u UNIQUE)", fixture.Diagnostic{Code: "42P07", Message: "relation \"u\" already exists"}, [][]any{{"other", "r"}, {"u", "i"}}},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", c.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
						namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, backend.DB, backend.PG)
						if c.setup != "" {
							for _, query := range strings.Split(c.setup, "; ") {
								run(fixture.Exec(query, 0))
							}
						}
						s := fixture.ImplicitError(c.query, c.want)
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
							if c.name == "empty" {
								s.ErrorStage = "prepare"
							}
						}
						run(s)
						run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relnamespace=$1 ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, c.relations, namespace))
					})
				}
			})
		}
	}
}

func TestReferenceUniqueSyntaxNames(t *testing.T) {
	longTable := strings.Repeat("界", 18)
	longColumn := strings.Repeat("語", 18)
	cases := []struct {
		name, setup, query     string
		constraints, relations [][]any
	}{
		{"column_default", "", "CREATE TABLE t(a INT UNIQUE)", [][]any{{"t_a_key", "u", "UNIQUE (a)", nil}}, [][]any{{"t", "r"}, {"t_a_key", "i"}}},
		{"column_explicit_distinct", "", "CREATE TABLE t(a INT UNIQUE NULLS DISTINCT)", [][]any{{"t_a_key", "u", "UNIQUE (a)", nil}}, [][]any{{"t", "r"}, {"t_a_key", "i"}}},
		{"column_not_distinct", "", "CREATE TABLE t(a INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", [][]any{{"u", "u", "UNIQUE NULLS NOT DISTINCT (a)", nil}}, [][]any{{"t", "r"}, {"u", "i"}}},
		{"composite", "", "CREATE TABLE t(a INT,b INT,UNIQUE(a,b))", [][]any{{"t_a_b_key", "u", "UNIQUE (a, b)", nil}}, [][]any{{"t", "r"}, {"t_a_b_key", "i"}}},
		{"same_order_transfer", "", "CREATE TABLE t(a INT,b INT,UNIQUE(a,b),CONSTRAINT chosen UNIQUE(a,b))", [][]any{{"chosen", "u", "UNIQUE (a, b)", nil}}, [][]any{{"chosen", "i"}, {"t", "r"}}},
		{"reverse_order_transfer", "", "CREATE TABLE t(a INT,b INT,UNIQUE(a,b),CONSTRAINT chosen UNIQUE(b,a))", [][]any{{"chosen", "u", "UNIQUE (b, a)", nil}, {"t_a_b_key", "u", "UNIQUE (a, b)", nil}}, [][]any{{"chosen", "i"}, {"t", "r"}, {"t_a_b_key", "i"}}},
		{"explicit_first", "", "CREATE TABLE t(a INT,b INT,CONSTRAINT chosen UNIQUE(a,b),UNIQUE(a,b))", [][]any{{"chosen", "u", "UNIQUE (a, b)", nil}}, [][]any{{"chosen", "i"}, {"t", "r"}}},
		{"two_explicit", "", "CREATE TABLE t(a INT,b INT,CONSTRAINT first_u UNIQUE(a,b),CONSTRAINT second_u UNIQUE(a,b))", [][]any{{"first_u", "u", "UNIQUE (a, b)", nil}}, [][]any{{"first_u", "i"}, {"t", "r"}}},
		{"different_null_policy", "", "CREATE TABLE t(a INT,UNIQUE(a),UNIQUE NULLS NOT DISTINCT(a))", [][]any{{"t_a_key", "u", "UNIQUE (a)", nil}, {"t_a_key1", "u", "UNIQUE NULLS NOT DISTINCT (a)", nil}}, [][]any{{"t", "r"}, {"t_a_key", "i"}, {"t_a_key1", "i"}}},
		{"occupied_generated_table", "CREATE TABLE t_a_key(a INT)", "CREATE TABLE t(a INT UNIQUE)", [][]any{{"t_a_key1", "u", "UNIQUE (a)", nil}}, [][]any{{"t", "r"}, {"t_a_key", "r"}, {"t_a_key1", "i"}}},
		{"occupied_generated_index", "CREATE TABLE other(a INT); CREATE INDEX t_a_key ON other(a)", "CREATE TABLE t(a INT UNIQUE)", [][]any{{"t_a_key1", "u", "UNIQUE (a)", nil}}, [][]any{{"other", "r"}, {"t", "r"}, {"t_a_key", "i"}, {"t_a_key1", "i"}}},
		{"namespace_constraint", "CREATE TABLE other(a INT CONSTRAINT t_a_key CHECK(a>0))", "CREATE TABLE t(a INT UNIQUE)", [][]any{{"t_a_key1", "u", "UNIQUE (a)", nil}}, [][]any{{"other", "r"}, {"t", "r"}, {"t_a_key1", "i"}}},
		{"quoted_labels", "", "CREATE TABLE t(\"A b\" INT,\"X\" TEXT,CONSTRAINT \"U q\" UNIQUE NULLS NOT DISTINCT(\"A b\",\"X\"))", [][]any{{"U q", "u", "UNIQUE NULLS NOT DISTINCT (\"A b\", \"X\")", nil}}, [][]any{{"U q", "i"}, {"t", "r"}}},
		{"balanced_unicode", "", "CREATE TABLE \"" + longTable + "\"(\"" + longColumn + "\" INT UNIQUE)", [][]any{{"界界界界界界界界界_語語語語語語語語語_key", "u", "UNIQUE (\"" + longColumn + "\")", nil}}, [][]any{{"界界界界界界界界界_語語語語語語語語語_key", "i"}, {longTable, "r"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, backend.DB, backend.PG)
					if c.setup != "" {
						for _, query := range strings.Split(c.setup, "; ") {
							run(fixture.Exec(query, 0))
						}
					}
					run(fixture.Prepared(fixture.Exec(c.query, 0)))
					// A neighboring CHECK's private pg_node_tree encoding is not a UNIQUE
					// contract. Assert every UNIQUE conbin is NULL, without pinning that tree.
					run(fixture.Select("SELECT c.conname,c.contype,pg_get_constraintdef(c.oid),c.conbin FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relnamespace=$1 AND c.contype='u' ORDER BY c.conname", []string{"conname", "contype", "pg_get_constraintdef", "conbin"}, []string{"NAME", "CHAR", "TEXT", "PG_NODE_TREE"}, c.constraints, namespace))
					run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relnamespace=$1 ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, c.relations, namespace))
					if c.name == "namespace_constraint" {
						run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='t_a_key'", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"t_a_key", "c", "CHECK ((a > 0))"}}))
					}
					if c.name == "quoted_labels" {
						run(fixture.Exec("INSERT INTO t VALUES(1,'hello')", 1))
						run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'hello')", fixture.UniqueReferenceDuplicate("t", "U q", "Key (\"A b\", \"X\")=(1, hello) already exists.")))
					}
				})
			}
		})
	}
}

func TestReferenceUniqueTypeIdentity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			snapshot := func() (int64, int64, int64) {
				var constraint, parent, index int64
				var name, kind, definition string
				err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid,conname,contype,conrelid,pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='u'").Scan(&constraint, &name, &kind, &parent, &definition)
				if err != nil {
					t.Fatal(err)
				}
				if name != "u" || kind != "u" || definition != "UNIQUE (k)" {
					t.Fatal(name, kind, definition)
				}
				err = backend.DB.QueryRowContext(p.Ctx, "SELECT oid,relname,relkind FROM pg_class WHERE relname='u'").Scan(&index, &name, &kind)
				if err != nil {
					t.Fatal(err)
				}
				if name != "u" || kind != "i" || constraint <= 0 || parent <= 0 || index <= 0 || constraint == parent || constraint == index || parent == index {
					t.Fatal("invalid unique identities", constraint, parent, index, name, kind)
				}
				return constraint, parent, index
			}
			before, parent, beforeIndex := snapshot()
			run(fixture.Exec("ALTER TABLE t ALTER COLUMN k TYPE BIGINT", 0))
			after, afterParent, afterIndex := snapshot()
			if before == after || beforeIndex == afterIndex || parent != afterParent {
				t.Fatal("TYPE must rebuild unique identities and retain parent", before, after, parent, afterParent, beforeIndex, afterIndex)
			}
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
			run(fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Exec("CREATE TABLE collision(k TEXT CONSTRAINT collision_u UNIQUE)", 0))
			run(fixture.Exec("INSERT INTO collision VALUES('01'),('1')", 2))
			want := fixture.UniqueReferenceDuplicate("collision", "collision_u", "Key (k)=(1) is duplicated.")
			want.Message = "could not create unique index \"collision_u\""
			run(fixture.ErrorDiagnostic("ALTER TABLE collision ALTER COLUMN k TYPE INT USING k::INT", want))
			run(fixture.Select("SELECT k FROM collision ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"01"}, {"1"}}))
		})
	}
}
