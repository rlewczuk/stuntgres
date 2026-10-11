package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeOptions(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(a INT DEFAULT 7,n INT NOT NULL)", 0))
		cases := []struct {
			options string
			value   any
		}{
			{"", nil}, {"EXCLUDING DEFAULTS", nil}, {"INCLUDING DEFAULTS EXCLUDING DEFAULTS", nil},
			{"INCLUDING DEFAULTS", int64(7)}, {"EXCLUDING DEFAULTS INCLUDING DEFAULTS", int64(7)},
			{"INCLUDING ALL EXCLUDING DEFAULTS", nil}, {"EXCLUDING DEFAULTS INCLUDING ALL", int64(7)},
			{"INCLUDING ALL EXCLUDING ALL INCLUDING DEFAULTS", int64(7)},
		}
		for i, tc := range cases {
			name := fmt.Sprintf("copy%d", i)
			run(fixture.Exec("CREATE TABLE "+name+"(LIKE src "+tc.options+")", 0))
			run(fixture.Exec("INSERT INTO "+name+"(n) VALUES(1)", 1))
			run(fixture.Select("SELECT * FROM "+name, []string{"a", "n"}, []string{"INT4", "INT4"}, [][]any{{tc.value, int64(1)}}))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM "+name, []string{"a", "n"}, []string{"INT4", "INT4"}, []bool{true, false}, 1)
		}
		run(fixture.Exec("CREATE TABLE other(b INT DEFAULT 9)", 0))
		run(fixture.Exec("CREATE TABLE mixed(LIKE src INCLUDING ALL EXCLUDING DEFAULTS,LIKE other INCLUDING DEFAULTS)", 0))
		run(fixture.Exec("INSERT INTO mixed(n) VALUES(2)", 1))
		run(fixture.Select("SELECT * FROM mixed", []string{"a", "n", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{nil, int64(2), int64(9)}}))
	})
}

func TestReferenceTableLikeAbsentOptions(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(a INT NOT NULL)", 0))
		for _, mode := range []string{"INCLUDING", "EXCLUDING"} {
			for _, option := range []string{"COMMENTS", "COMPRESSION", "CONSTRAINTS", "DEFAULTS", "GENERATED", "IDENTITY", "INDEXES", "STATISTICS", "STORAGE", "ALL"} {
				run(fixture.Exec("CREATE TABLE copy(LIKE src "+mode+" "+option+")", 0))
				run(fixture.Select("SELECT * FROM copy", []string{"a"}, []string{"INT4"}, nil))
				run(fixture.TableLikeError("INSERT INTO copy VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "copy" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "copy", Column: "a"}))
				run(fixture.Exec("INSERT INTO copy VALUES(11)", 1))
				run(fixture.Select("SELECT * FROM copy", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
				run(fixture.Exec("DROP TABLE copy", 0))
			}
		}
	})
}

func TestReferenceTableLikeShape(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(a INT NOT NULL,discard TEXT,b VARCHAR(4) COLLATE \"C\")", 0))
		run(fixture.Exec("INSERT INTO src VALUES(7,'gone','é')", 1))
		run(fixture.Exec("ALTER TABLE src DROP COLUMN discard", 0))
		run(fixture.Exec("CREATE TABLE clone(lead TEXT,LIKE src INCLUDING ALL,tail BOOL)", 0))
		names, types := []string{"lead", "a", "b", "tail"}, []string{"TEXT", "INT4", "VARCHAR", "BOOL"}
		run(fixture.Select("SELECT * FROM clone", names, types, nil))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM clone", names, types, []bool{true, false, true, true}, 0)
		run(fixture.Exec("INSERT INTO clone VALUES('x',8,'abcd',true)", 1))
		run(fixture.TableLikeError("INSERT INTO clone VALUES('x',8,'abcde',true)", fixture.Diagnostic{Code: "22001", Message: "value too long for type character varying(4)"}))
		run(fixture.Exec("DROP TABLE src RESTRICT", 0))
		run(fixture.Select("SELECT * FROM clone", names, types, [][]any{{"x", int64(8), "abcd", true}}))
	})
}

func TestReferenceTableLikeViews(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE src(a INT NOT NULL DEFAULT 7 UNIQUE,b INT GENERATED ALWAYS AS(a*2) STORED,CONSTRAINT positive CHECK(a>0))", 0))
		run(fixture.Exec("INSERT INTO src(a) VALUES(11)", 1))
		run(fixture.Exec("CREATE VIEW v AS SELECT a,b FROM src", 0))
		run(fixture.Exec("CREATE TABLE clone(LIKE v INCLUDING ALL)", 0))
		run(fixture.Select("SELECT * FROM clone", []string{"a", "b"}, []string{"INT4", "INT4"}, nil))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM clone", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 0)
		run(fixture.Exec("INSERT INTO clone DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO clone VALUES(-1,5),(-1,5)", 2))
		run(fixture.Exec("DROP VIEW v", 0))
		run(fixture.Exec("DROP TABLE src", 0))
		run(fixture.Select("SELECT * FROM clone ORDER BY a NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}, {int64(-1), int64(5)}, {int64(-1), int64(5)}}))
	})
}
