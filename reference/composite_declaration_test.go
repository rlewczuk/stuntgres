package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceCompositeDeclarations(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				for _, q := range []string{
					`CREATE TYPE empty_record AS ()`,
					`CREATE TYPE system_record AS (ctid text,tableoid integer,xmin integer)`,
					`CREATE TYPE "Odd Record" AS ("Mixed Field" integer,"quote""field" text COLLATE "C")`,
					`CREATE TYPE public.normal_record AS (i integer,t varchar(2),n numeric(4,1),b boolean,a integer[])`,
				} {
					s := referenceExec(q, 0)
					s.prepared = prepared
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				}
			})
		})
	}
}

func TestReferenceCompositeDeclarationDiagnostics(t *testing.T) {
	cases := []struct{ name, setup, query, code, message, position, stage string }{
		{"duplicate", "", `CREATE TYPE bad AS (x integer,X text)`, "42701", `column "x" specified more than once`, "", "execute"},
		{"missing_type", "", `CREATE TYPE bad AS (x absent_type)`, "42704", `type "absent_type" does not exist`, "", "execute"},
		{"missing_schema", "", `CREATE TYPE absent_schema.bad AS (x integer)`, "3F000", `schema "absent_schema" does not exist`, "", "execute"},
		{"pseudo", "", `CREATE TYPE bad AS (x record)`, "42P16", `column "x" has pseudo-type record`, "", "execute"},
		{"modifier", "", `CREATE TYPE bad AS (x integer(2))`, "42601", `syntax error at or near "("`, "30", "prepare"},
		{"collation", "", `CREATE TYPE bad AS (x integer COLLATE "C")`, "42804", `collations are not supported by type integer`, "", "execute"},
		{"constraint", "", `CREATE TYPE bad AS (x integer NOT NULL)`, "42601", `syntax error at or near "NOT"`, "31", "prepare"},
		{"type_collision", `CREATE TYPE occupied AS ENUM ('x')`, `CREATE TYPE occupied AS (x integer)`, "42710", `type "occupied" already exists`, "", "execute"},
		{"table_collision", `CREATE TABLE occupied(x integer)`, `CREATE TYPE occupied AS (x integer)`, "42710", `type "occupied" already exists`, "", "execute"},
		{"index_collision", `CREATE TABLE base(x integer); CREATE INDEX occupied ON base(x)`, `CREATE TYPE occupied AS (x integer)`, "42P07", `relation "occupied" already exists`, "", "execute"},
		{"sequence_collision", `CREATE SEQUENCE occupied`, `CREATE TYPE occupied AS (x integer)`, "42P07", `relation "occupied" already exists`, "", "execute"},
		{"table_after_type", `CREATE TYPE occupied AS (x integer)`, `CREATE TABLE occupied(x integer)`, "42P07", `relation "occupied" already exists`, "", "execute"},
		{"select_backing", `CREATE TYPE occupied AS (x integer)`, `SELECT * FROM occupied`, "42809", `cannot open relation "occupied"`, "15", "prepare"},
		{"insert_backing", `CREATE TYPE occupied AS (x integer)`, `INSERT INTO occupied VALUES (1)`, "42809", `cannot open relation "occupied"`, "13", "prepare"},
		{"drop_backing", `CREATE TYPE occupied AS (x integer)`, `DROP TABLE occupied`, "42809", `"occupied" is not a table`, "", "execute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				for _, q := range strings.Split(tc.setup, "; ") {
					if q != "" {
						compositeReferenceSetup(t, p, b, c, q)
					}
				}
				want := compositeReferenceDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				if tc.name == "drop_backing" {
					want.Hint = "Use DROP TYPE to remove a type."
				}
				if tc.name == "select_backing" || tc.name == "insert_backing" {
					want.Detail = "This operation is not supported for composite types."
				}
				compositeReferenceError(t, p, c, tc.query, tc.stage, want)
			})
		})
	}
}

func TestReferenceCompositeDeclarationLimits(t *testing.T) {
	for _, count := range []int{1600, 1601} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				fields := make([]string, count)
				for i := range fields {
					fields[i] = fmt.Sprintf("f%d integer", i)
				}
				q := "CREATE TYPE wide_record AS (" + strings.Join(fields, ",") + ")"
				if count == 1600 {
					compositeReferenceSetup(t, p, b, c, q)
				} else {
					compositeReferenceError(t, p, c, q, "execute", compositeReferenceDiagnostic("54011", "tables can have at most 1600 columns"))
				}
			})
		})
	}
}

func TestReferenceCompositeArrayNameDisplacement(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TYPE item AS (i integer)`, `CREATE TYPE _item AS (t text)`, `CREATE TABLE holder(v item,w _item)`)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES ('(7)','(word)')`, 1))
		enumReferenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(`SELECT v,w FROM holder`, []string{"v", "w"}, []string{"", ""}, [][]any{{"(7)", "(word)"}}))
	})
}
