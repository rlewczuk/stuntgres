package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableDiagnostics(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE row_source(id integer)", 0))
		run(fixture.Exec("CREATE TYPE mood AS ENUM('ok')", 0))
		run(fixture.Exec("CREATE DOMAIN wrapped AS pair", 0))
		for _, tc := range []struct{ q, code, message, detail, token, stage string }{
			{"CREATE TABLE bad OF missing", "42704", `type "missing" does not exist`, "", "missing", "execute"},
			{"CREATE TABLE bad OF int4", "42809", "type integer is not a composite type", "", "", "execute"},
			{"CREATE TABLE bad OF mood", "42809", "type mood is not a composite type", "", "", "execute"},
			{"CREATE TABLE bad OF wrapped", "42809", "type wrapped is not a composite type", "", "", "execute"},
			{"CREATE TABLE bad OF row_source", "42809", "type row_source is the row type of another table", "A typed table must use a stand-alone composite type created with CREATE TYPE.", "", "execute"},
			{"CREATE TABLE bad OF pair (absent DEFAULT 1)", "42703", `column "absent" does not exist`, "", "", "execute"},
			{"CREATE TABLE bad OF pair (id DEFAULT 1,id DEFAULT 2)", "42701", `column "id" specified more than once`, "", "", "execute"},
			{"CREATE TABLE bad OF pair (id integer)", "42601", `syntax error at or near "integer"`, "", "integer", "prepare"},
			{"CREATE TABLE bad OF pair ()", "42601", `syntax error at or near ")"`, "", ")", "prepare"},
			{"CREATE TABLE bad OF pair (id WITH OPTIONS GENERATED ALWAYS AS IDENTITY)", "0A000", "identity columns are not supported on typed tables", "", "", "execute"},
			{"CREATE TABLE bad OF pair (id WITH OPTIONS GENERATED ALWAYS AS (1) STORED)", "0A000", "generated columns are not supported on typed tables", "", "", "execute"},
			{"ALTER TABLE t ADD COLUMN extra integer", "42809", "cannot add column to typed table", "", "", "execute"},
			{"ALTER TABLE t ADD COLUMN IF NOT EXISTS id integer", "42809", "cannot add column to typed table", "", "", "execute"},
			{"ALTER TABLE t DROP COLUMN absent", "42809", "cannot drop column from typed table", "", "", "execute"},
			{"ALTER TABLE t DROP COLUMN IF EXISTS absent", "42809", "cannot drop column from typed table", "", "", "execute"},
			{"ALTER TABLE t RENAME COLUMN absent TO other", "42809", "cannot rename column of typed table", "", "", "execute"},
			{"ALTER TABLE t ALTER COLUMN id TYPE integer", "42809", "cannot alter column type of typed table", "", "id", "execute"},
			{"ALTER TABLE t ALTER COLUMN absent TYPE missing", "42703", `column "absent" of relation "t" does not exist`, "", "", "execute"},
			{"ALTER TYPE pair ADD ATTRIBUTE x integer DEFAULT 1", "42601", `syntax error at or near "DEFAULT"`, "", "DEFAULT", "prepare"},
			{"ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint USING id", "42601", `syntax error at or near "USING"`, "", "USING", "prepare"},
		} {
			d := typedTableError(tc.code, tc.message)
			d.Detail = tc.detail
			if tc.token != "" {
				d.Position = strconv.Itoa(strings.Index(tc.q, tc.token) + 1)
			}
			fixture.CompositeReferenceError(t, p, c, tc.q, tc.stage, d)
		}
		run(fixture.Select("SELECT * FROM t", []string{"id", "note"}, []string{"INT4", "VARCHAR"}, nil))
		run(fixture.TypedTableAttachment("t", "pair", true))
	})
}

func TestReferenceTypedTableIfNotExistsAndSystemColumn(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE t(id integer)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11)", 1))
		run(fixture.Exec("CREATE TABLE IF NOT EXISTS t OF missing", 0))
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE IF NOT EXISTS t OF pair (absent DEFAULT 1)", 0))
		run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
		run(fixture.TypedTableAttachment("t", "pair", false))
		run(fixture.Exec("CREATE TYPE sys AS (ctid integer)", 0))
		fixture.CompositeReferenceError(t, p, c, "CREATE TABLE bad OF sys", "execute", typedTableError("42701", `column name "ctid" conflicts with a system column name`))
	})
}
