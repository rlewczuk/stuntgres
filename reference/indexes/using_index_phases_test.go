package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceUsingIndexPrimaryNullability(t *testing.T) {
	usingIndexScenario(t, func(c *usingIndexCase) {
		c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
		c.exec("ALTER TABLE ux_t ADD CONSTRAINT existing_nn NOT NULL a", 0)
		var nn int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='existing_nn'").Scan(&nn); err != nil {
			c.t.Fatal(err)
		}
		c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_pk PRIMARY KEY USING INDEX ux_idx", 0)
		var current int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='existing_nn'").Scan(&current); err != nil || nn != current {
			c.t.Fatal("existing NN replaced", nn, current, err)
		}
		c.attributes([][]any{{"a", int64(23), true}, {"b", int64(25), true}, {"payload", int64(25), false}})
		c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(NULL,'z',NULL)", fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, z, null")))
		c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(3,NULL,NULL)", fixture.PrimaryKeyReferenceNull("ux_t", "b", "3, null, null")))
		c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(1,'x',NULL)", fixture.UniqueReferenceDuplicate("ux_t", "ux_pk", "Key (a, b)=(1, x) already exists.")))
		c.exec("INSERT INTO ux_t VALUES(3,'z',NULL)", 1)
	})
	for _, x := range []struct{ name, table, index, extra string }{
		{"null_a", "a INT,b TEXT,payload TEXT", "a,b", "INSERT INTO ux_t VALUES(NULL,'x','kept')"},
		{"null_b", "a INT,b TEXT,payload TEXT", "a,b", "INSERT INTO ux_t VALUES(1,NULL,'kept')"},
		{"both_null_reverse", "a INT,b TEXT,payload TEXT", "b,a", "INSERT INTO ux_t VALUES(NULL,NULL,'kept')"},
		{"nnd_null", "a INT,b TEXT,payload TEXT", "a,b", "INSERT INTO ux_t VALUES(NULL,'x','kept')"},
		{"existing_pk_nnd", "a INT PRIMARY KEY,b TEXT,payload TEXT", "a,b", "INSERT INTO ux_t VALUES(1,'x','kept')"},
		{"collision_null", "a INT,b TEXT,payload TEXT", "a,b", "INSERT INTO ux_t VALUES(NULL,'x','kept'); CREATE TABLE ux_pk(k INT)"},
	} {
		t.Run(x.name, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.exec("CREATE TABLE ux_t("+x.table+")", 0)
				if x.name == "collision_null" {
					c.exec("INSERT INTO ux_t VALUES(NULL,'x','kept')", 1)
					c.exec("CREATE TABLE ux_pk(k INT)", 0)
				} else {
					c.exec(x.extra, 1)
				}
				suffix := ""
				if x.name == "nnd_null" || x.name == "existing_pk_nnd" {
					suffix = " NULLS NOT DISTINCT"
				}
				c.exec("CREATE UNIQUE INDEX ux_idx ON ux_t("+x.index+") INCLUDE(payload)"+suffix, 0)
				original := c.snapshot()
				column := "a"
				if x.name == "null_b" {
					column = "b"
				}
				want := usingIndexDiagnostic("23502", fmt.Sprintf(`column "%s" of relation "ux_t" contains null values`, column))
				want.Schema, want.Table, want.Column = "public", "ux_t", column
				switch x.name {
				case "nnd_null":
					want = usingIndexDiagnostic("42P16", "primary keys cannot use NULLS NOT DISTINCT indexes")
				case "existing_pk_nnd":
					want = usingIndexDiagnostic("42P16", `multiple primary keys for table "ux_t" are not allowed`)
				case "collision_null":
					want = usingIndexDiagnostic("42P07", `relation "ux_pk" already exists`)
				}
				c.failure("ALTER TABLE ux_t ADD CONSTRAINT ux_pk PRIMARY KEY USING INDEX ux_idx", want, false, usingIndexRename("ux_idx", "ux_pk"))
				c.unchanged(original)
				notNull := x.name == "existing_pk_nnd"
				c.attributes([][]any{{"a", int64(23), notNull}, {"b", int64(25), false}, {"payload", int64(25), false}})
				var row []any
				switch x.name {
				case "null_b":
					row = []any{int64(1), nil, "kept"}
				case "both_null_reverse":
					row = []any{nil, nil, "kept"}
				case "existing_pk_nnd":
					row = []any{int64(1), "x", "kept"}
				default:
					row = []any{nil, "x", "kept"}
				}
				c.run(fixture.Select("SELECT a,b,payload FROM ux_t", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{row}))
				c.exec("INSERT INTO ux_t VALUES(3,'z',NULL)", 1)
			})
		})
	}
}

func TestReferenceUsingIndexAlterPasses(t *testing.T) {
	// Pinned 18.0 applies DROP before eligibility capture, TYPE before attachment,
	// and runtime attachment after SET validation, independently of textual order.
	for _, x := range []struct{ name, setup, actions string }{
		{"drop_pk_first", "CREATE TABLE ux_t(a INT PRIMARY KEY,b TEXT,payload TEXT); INSERT INTO ux_t VALUES(1,'x','kept'); CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)", "DROP CONSTRAINT ux_t_pkey, ADD CONSTRAINT ux_pk PRIMARY KEY USING INDEX ux_idx"},
		{"drop_pk_last", "CREATE TABLE ux_t(a INT PRIMARY KEY,b TEXT,payload TEXT); INSERT INTO ux_t VALUES(1,'x','kept'); CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)", "ADD CONSTRAINT ux_pk PRIMARY KEY USING INDEX ux_idx, DROP CONSTRAINT ux_t_pkey"},
		{"type_first", "", "ALTER a TYPE BIGINT, ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx"},
		{"type_last", "", "ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx, ALTER a TYPE BIGINT"},
		{"drop_key_first", "", "DROP a, ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx"},
		{"drop_key_last", "", "ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx, DROP a"},
		{"drop_include_first", "", "DROP payload, ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx"},
		{"drop_include_last", "", "ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx, DROP payload"},
		{"same_index_twice", "", "ADD CONSTRAINT ux_one UNIQUE USING INDEX ux_idx, ADD CONSTRAINT ux_two UNIQUE USING INDEX ux_idx"},
		{"two_pk", "", "ADD CONSTRAINT ux_one PRIMARY KEY USING INDEX ux_idx, ADD CONSTRAINT ux_two PRIMARY KEY USING INDEX missing"},
		{"missing_later", "", "ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx, ALTER missing SET NOT NULL"},
		{"missing_earlier", "", "ALTER missing SET NOT NULL, ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx"},
	} {
		t.Run(x.name, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				if x.setup == "" {
					c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
				} else {
					for _, s := range strings.Split(x.setup, "; ") {
						n := int64(0)
						if strings.HasPrefix(s, "INSERT") {
							n = 1
						}
						c.exec(s, n)
					}
				}
				old, parent := c.oid("ux_idx"), c.oid("ux_t")
				original := c.snapshot()
				q := "ALTER TABLE ux_t " + x.actions
				if strings.HasPrefix(x.name, "drop_pk") {
					oldNN := c.notNullIDs()
					c.notices = nil
					c.exec(q, 0)
					c.noticesWant(usingIndexRename("ux_idx", "ux_pk"))
					ids := c.ids("ux_pk")
					if ids[1] != parent || ids[2] != old {
						c.t.Fatal("replacement PK attachment replaced relation", ids, parent, old)
					}
					currentNN := c.notNullIDs()
					if len(oldNN) != 1 || len(currentNN) != 2 || currentNN[0] != oldNN[0] {
						c.t.Fatal("replacement PK lost original NN", oldNN, currentNN)
					}
					c.catalog([][]any{{"ux_pk", "p", "PRIMARY KEY (a, b) INCLUDE (payload)", nil}, {"ux_t_a_not_null", "n", "NOT NULL a", nil}, {"ux_t_b_not_null", "n", "NOT NULL b", nil}})
					c.attributes([][]any{{"a", int64(23), true}, {"b", int64(25), true}, {"payload", int64(25), false}})
					c.indexAttributes("ux_pk", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(25)}})
					c.run(fixture.Select("SELECT a,b,payload FROM ux_t", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "kept"}}))
					c.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ux_t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
					return
				}
				if strings.HasPrefix(x.name, "type") {
					c.notices = nil
					c.exec(q, 0)
					c.noticesWant(usingIndexRename("ux_idx", "ux_owned"))
					ids := c.ids("ux_owned")
					if ids[1] != parent || ids[2] == old {
						c.t.Fatal("TYPE must preserve parent and remint backing index", ids, parent, old)
					}
					c.catalog([][]any{{"ux_owned", "u", "UNIQUE (a, b) INCLUDE (payload)", nil}})
					c.attributes([][]any{{"a", int64(20), false}, {"b", int64(25), false}, {"payload", int64(25), false}})
					c.indexAttributes("ux_owned", [][]any{{"a", int64(20)}, {"b", int64(25)}, {"payload", int64(25)}})
					c.run(fixture.Select("SELECT a,b,payload FROM ux_t ORDER BY a", []string{"a", "b", "payload"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}}))
					return
				}
				want := usingIndexDiagnostic("42704", `index "ux_idx" does not exist`)
				var notices []fixture.Diagnostic
				switch x.name {
				case "drop_key_first":
					want.Position = "30"
				case "drop_include_first":
					want.Position = "36"
				case "drop_key_last", "drop_include_last":
					want.Position = "22"
				case "two_pk":
					want.Message, want.Position = `index "missing" does not exist`, "76"
				case "same_index_twice":
					want = usingIndexCatalogDuplicate(parent, "ux_two")
					notices = []fixture.Diagnostic{usingIndexRename("ux_idx", "ux_one"), usingIndexRename("ux_one", "ux_two")}
				case "missing_later", "missing_earlier":
					want = usingIndexDiagnostic("42703", `column "missing" of relation "ux_t" does not exist`)
				}
				c.failure(q, want, false, notices...)
				c.unchanged(original)
				c.rows()
				c.catalog(nil)
				c.attributes([][]any{{"a", int64(23), false}, {"b", int64(25), false}, {"payload", int64(25), false}})
				c.indexAttributes("ux_idx", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(25)}})
				c.exec("INSERT INTO ux_t VALUES(3,'z','reused')", 1)
			})
		})
	}
	usingIndexFocusedPrecedence(t)
	usingIndexPrimaryFailurePrecedence(t)
}

// Pinned PostgreSQL 18.0: eligibility precedes missing SET validation, whereas
// two eligible PK attachments emit both rename notices before multiple-PK failure.
func usingIndexFocusedPrecedence(t *testing.T) {
	for _, x := range []struct{ name, setup, actions string }{
		{"two_valid_pk_first", "CREATE UNIQUE INDEX ux_second ON ux_t(b) INCLUDE(payload)", "ADD CONSTRAINT ux_one PRIMARY KEY USING INDEX ux_idx, ADD CONSTRAINT ux_two PRIMARY KEY USING INDEX ux_second"},
		{"two_valid_pk_reversed", "CREATE UNIQUE INDEX ux_second ON ux_t(b) INCLUDE(payload)", "ADD CONSTRAINT ux_two PRIMARY KEY USING INDEX ux_second, ADD CONSTRAINT ux_one PRIMARY KEY USING INDEX ux_idx"},
		{"missing_index_first", "", "ADD CONSTRAINT ux_owned UNIQUE USING INDEX absent, ALTER missing SET NOT NULL"},
		{"missing_index_last", "", "ALTER missing SET NOT NULL, ADD CONSTRAINT ux_owned UNIQUE USING INDEX absent"},
		{"nonunique_index_first", "CREATE INDEX ux_bad ON ux_t(a)", "ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_bad, ALTER missing SET NOT NULL"},
		{"nonunique_index_last", "CREATE INDEX ux_bad ON ux_t(a)", "ALTER missing SET NOT NULL, ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_bad"},
	} {
		t.Run("focused/"+x.name, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
				if x.setup != "" {
					c.exec(x.setup, 0)
				}
				original := c.snapshot()
				c.notices = nil
				index, parent := c.oid("ux_idx"), c.oid("ux_t")
				q := "ALTER TABLE ux_t " + x.actions
				want := usingIndexDiagnostic("42704", `index "absent" does not exist`)
				want.Position = usingIndexConstraintPosition(q)
				var notices []fixture.Diagnostic
				switch x.name {
				case "two_valid_pk_first", "two_valid_pk_reversed":
					want = usingIndexDiagnostic("42P16", `multiple primary keys for table "ux_t" are not allowed`)
					notices = []fixture.Diagnostic{usingIndexRename("ux_idx", "ux_one"), usingIndexRename("ux_second", "ux_two")}
					if x.name == "two_valid_pk_reversed" {
						notices[0], notices[1] = notices[1], notices[0]
					}
				case "nonunique_index_first", "nonunique_index_last":
					want = usingIndexDiagnostic("42809", `"ux_bad" is not a unique index`)
					want.Detail = "Cannot create a primary key or unique constraint using such an index."
					want.Position = usingIndexConstraintPosition(q)
				}
				c.failure(q, want, false, notices...)
				c.unchanged(original)
				c.rows()
				c.catalog(nil)
				c.attributes([][]any{{"a", int64(23), false}, {"b", int64(25), false}, {"payload", int64(25), false}})
				c.indexAttributes("ux_idx", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(25)}})
				if strings.HasPrefix(x.name, "two_valid_pk") {
					c.indexAttributes("ux_second", [][]any{{"b", int64(25)}, {"payload", int64(25)}})
				}
				if strings.HasPrefix(x.name, "nonunique_index") {
					c.indexAttributes("ux_bad", [][]any{{"a", int64(23)}})
				}
				c.notices = nil
				c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_recovered UNIQUE USING INDEX ux_idx", 0)
				c.noticesWant(usingIndexRename("ux_idx", "ux_recovered"))
				ids := c.ids("ux_recovered")
				if ids[1] != parent || ids[2] != index {
					c.t.Fatal("retry replaced original relation", ids, parent, index)
				}
				c.exec("INSERT INTO ux_t VALUES(3,'z','reused')", 1)
			})
		})
	}
}

// Pinned PostgreSQL 18.0: runtime attachment failures precede the final NULL scan.
func usingIndexPrimaryFailurePrecedence(t *testing.T) {
	for _, collision := range []bool{false, true} {
		name := "two_primary"
		if collision {
			name = "check_collision"
		}
		t.Run("null_precedence/"+name, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				table := "CREATE TABLE ux_t(a INT,b TEXT,payload TEXT)"
				if collision {
					table = "CREATE TABLE ux_t(a INT,b TEXT,payload TEXT,CONSTRAINT ux_owned CHECK(a>0))"
				}
				c.exec(table, 0)
				c.exec("INSERT INTO ux_t VALUES(NULL,'x','kept'),(2,'y',NULL)", 2)
				c.exec("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)", 0)
				q := "ALTER TABLE ux_t ADD CONSTRAINT ux_owned PRIMARY KEY USING INDEX ux_idx"
				want := usingIndexCatalogDuplicate(c.oid("ux_t"), "ux_owned")
				notices := []fixture.Diagnostic{usingIndexRename("ux_idx", "ux_owned")}
				if !collision {
					c.exec("CREATE UNIQUE INDEX ux_second ON ux_t(b) INCLUDE(payload)", 0)
					q = "ALTER TABLE ux_t ADD CONSTRAINT ux_one PRIMARY KEY USING INDEX ux_idx, ADD CONSTRAINT ux_two PRIMARY KEY USING INDEX ux_second"
					want = usingIndexDiagnostic("42P16", `multiple primary keys for table "ux_t" are not allowed`)
					notices = []fixture.Diagnostic{usingIndexRename("ux_idx", "ux_one"), usingIndexRename("ux_second", "ux_two")}
				}
				original := c.snapshot()
				index, parent := c.oid("ux_idx"), c.oid("ux_t")
				c.failure(q, want, false, notices...)
				c.unchanged(original)
				c.attributes([][]any{{"a", int64(23), false}, {"b", int64(25), false}, {"payload", int64(25), false}})
				c.indexAttributes("ux_idx", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(25)}})
				c.run(fixture.Select("SELECT a,b,payload FROM ux_t ORDER BY b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{nil, "x", "kept"}, {int64(2), "y", nil}}))
				c.notices = nil
				c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_recovered UNIQUE USING INDEX ux_idx", 0)
				c.noticesWant(usingIndexRename("ux_idx", "ux_recovered"))
				ids := c.ids("ux_recovered")
				if ids[1] != parent || ids[2] != index {
					c.t.Fatal("retry replaced original relation", ids, parent, index)
				}
				c.exec("INSERT INTO ux_t VALUES(3,'z','reused')", 1)
			})
		})
	}
}

func TestReferenceUsingIndexImmediateAttributes(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		t.Run(kind, func(t *testing.T) {
			for _, tail := range []string{"", "NOT DEFERRABLE", "INITIALLY IMMEDIATE", "NOT DEFERRABLE INITIALLY IMMEDIATE", "INITIALLY IMMEDIATE NOT DEFERRABLE", "NOT DEFERRABLE NOT DEFERRABLE", "INITIALLY IMMEDIATE INITIALLY IMMEDIATE", "DEFERRABLE", "DEFERRABLE INITIALLY IMMEDIATE", "INITIALLY DEFERRED", "DEFERRABLE INITIALLY DEFERRED", "NOT DEFERRABLE DEFERRABLE", "DEFERRABLE NOT DEFERRABLE", "INITIALLY IMMEDIATE INITIALLY DEFERRED", "NOT DEFERRABLE INITIALLY DEFERRED"} {
				t.Run(fmt.Sprintf("properties=%s", tail), func(t *testing.T) {
					usingIndexScenario(t, func(c *usingIndexCase) {
						c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b)")
						original := c.snapshot()
						index, parent := c.oid("ux_idx"), c.oid("ux_t")
						q := "ALTER TABLE ux_t ADD CONSTRAINT ux_owned " + kind + " USING INDEX ux_idx " + tail
						conflictToken := ""
						message := "conflicting constraint properties"
						switch tail {
						case "NOT DEFERRABLE DEFERRABLE":
							conflictToken = "DEFERRABLE"
						case "DEFERRABLE NOT DEFERRABLE":
							conflictToken = "NOT DEFERRABLE"
						case "INITIALLY IMMEDIATE INITIALLY DEFERRED":
							conflictToken = "INITIALLY DEFERRED"
						case "NOT DEFERRABLE INITIALLY DEFERRED":
							conflictToken, message = "INITIALLY DEFERRED", "constraint declared INITIALLY DEFERRED must be DEFERRABLE"
						}
						if conflictToken != "" {
							want := usingIndexDiagnostic("42601", message)
							want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q, conflictToken)]) + 1)
							c.failure(q, want, true)
							c.unchanged(original)
							c.rows()
							return
						}
						deferrable := tail == "DEFERRABLE" || tail == "DEFERRABLE INITIALLY IMMEDIATE" || tail == "INITIALLY DEFERRED" || tail == "DEFERRABLE INITIALLY DEFERRED"
						deferred := tail == "INITIALLY DEFERRED" || tail == "DEFERRABLE INITIALLY DEFERRED"
						c.notices = nil
						c.exec(q, 0)
						c.noticesWant(usingIndexRename("ux_idx", "ux_owned"))
						ids := c.ids("ux_owned")
						if ids[1] != parent || ids[2] != index {
							c.t.Fatal("property normalization replaced relation", ids, parent, index)
						}
						deparse := kind + " (a, b)"
						if deferrable {
							deparse += " DEFERRABLE"
						}
						if deferred {
							deparse += " INITIALLY DEFERRED"
						}
						required := kind == "PRIMARY KEY"
						constraintKind := "u"
						if required {
							constraintKind = "p"
						}
						constraints := [][]any{{"ux_owned", constraintKind, deparse, nil}}
						if required {
							constraints = append(constraints, []any{"ux_t_a_not_null", "n", "NOT NULL a", nil}, []any{"ux_t_b_not_null", "n", "NOT NULL b", nil})
						}
						c.catalog(constraints)
						c.run(fixture.Select("SELECT condeferrable,condeferred,conkey::text FROM pg_constraint WHERE conname='ux_owned'", []string{"condeferrable", "condeferred", "conkey"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{deferrable, deferred, "{1,2}"}}))
						c.attributes([][]any{{"a", int64(23), required}, {"b", int64(25), required}, {"payload", int64(25), false}})
						c.indexAttributes("ux_owned", [][]any{{"a", int64(23)}, {"b", int64(25)}})
						c.rows()
					})
				})
			}
		})
	}
}
