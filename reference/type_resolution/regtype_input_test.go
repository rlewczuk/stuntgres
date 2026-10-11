package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegtypeInput(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		for _, s := range []fixture.Step{
			fixture.RegtypeSelect("SELECT 'int4'::regtype AS a,'integer'::regtype AS b,'INT'::regtype AS c,'pg_catalog.int4'::regtype AS d", "integer", "integer", "integer", "integer"),
			fixture.RegtypeSelect("SELECT 'bool'::regtype AS a,'bpchar'::regtype AS b,'varchar(12)'::regtype AS c,'float4'::regtype AS d,'double precision'::regtype AS e", "boolean", "character", "character varying", "real", "double precision"),
			fixture.RegtypeSelect("SELECT 'numeric(5,2)'::regtype AS a,'decimal(8,3)'::regtype AS b,'time(2)'::regtype AS c,'timestamp(4) with time zone'::regtype AS d", "numeric", "numeric", "time without time zone", "timestamp with time zone"),
			fixture.RegtypeSelect("SELECT 'float(24)'::regtype AS a,'float(25)'::regtype AS b,'float(53)'::regtype AS c", "real", "double precision", "double precision"),
			fixture.RegtypeSelect("SELECT 'integer[]'::regtype AS a,'integer[2][3]'::regtype AS b,'integer ARRAY[3]'::regtype AS c,'pg_catalog._int4'::regtype AS d", "integer[]", "integer[]", "integer[]", "integer[]"),
			fixture.RegtypeSelect("SELECT '23'::regtype AS a,'0'::regtype AS b,'-'::regtype AS c,'4294967295'::regtype AS d", "integer", "-", "-", "4294967295"),
			fixture.RegtypeSelect("SELECT '023'::regtype AS a,'010'::regtype AS b", "name", "8"),
			fixture.RegtypeSelect("SELECT NULL::regtype AS a,CAST(NULL AS pg_catalog.regtype) AS b", nil, nil),
			fixture.RegtypeSelect("SELECT 'int4 -- ignored SQL comment'::regtype AS a,'int4 /* ignored */'::regtype AS b", "integer", "integer"),
			fixture.RegtypeSelect("SELECT 'bit(3)[]'::regtype AS a,'pg_catalog._varbit(8)'::regtype AS b,'interval day to second(3)'::regtype AS c", "bit[]", "bit varying[]", "interval"),
			fixture.RegtypeSelect("SELECT 'pg_catalog.varchar(''12'')'::regtype AS a,'pg_catalog.varchar((12))'::regtype AS b", "character varying", "character varying"),
			fixture.Select("SELECT 'integer'::regtype::oid::bigint AS o,23::oid::regtype AS r,23::integer::regtype AS i,23::bigint::regtype AS b", []string{"o", "r", "i", "b"}, []string{"INT8", "REGTYPE", "REGTYPE", "REGTYPE"}, [][]any{{int64(23), "integer", "integer", "integer"}}),
			fixture.Select("SELECT '-1'::integer::regtype::bigint AS a,'4294967295'::regtype::integer AS b", []string{"a", "b"}, []string{"INT8", "INT4"}, [][]any{{int64(4294967295), int64(-1)}}),
			fixture.Select("SELECT 'integer'::text::regtype AS r,'integer'::regtype::text AS t", []string{"r", "t"}, []string{"REGTYPE", "TEXT"}, [][]any{{"integer", "integer"}}),
			fixture.Select("SELECT 'int4'::regtype", []string{"regtype"}, []string{"REGTYPE"}, [][]any{{"integer"}}),
		} {
			fixture.RegtypeBoth(t, p, conn, recorder, s)
		}
	})
}

func TestReferenceRegtypeRowsAndCurrentDatabase(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		fixture.RegtypeSetup(t, p, conn, recorder)
		for _, q := range []string{
			"CREATE TABLE regtype_a.row_subject(v integer,d regtype_a.same,a regtype_a.same[])",
			"CREATE VIEW regtype_a.row_view AS SELECT v,d FROM regtype_a.row_subject",
			"CREATE SEQUENCE regtype_a.sequence_subject",
		} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
		}
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'regtype_a.row_subject'::regtype AS a,'regtype_a.row_subject[]'::regtype AS b,'regtype_a.row_view'::regtype AS c,'regtype_a.row_view[]'::regtype AS d", "row_subject", "row_subject[]", "row_view", "row_view[]"))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT ('regtype_a.row_subject[]'::regtype::oid='regtype_a._row_subject'::regtype::oid) AS a,('regtype_a.row_view[]'::regtype::oid='regtype_a._row_view'::regtype::oid) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		var current string
		if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&current); err != nil {
			t.Fatal(err)
		}
		for _, fact := range []struct{ suffix, output string }{{".pg_catalog.int4", "integer"}, {".pg_catalog._int4", "integer[]"}, {".pg_catalog.int4[]", "integer[]"}, {".regtype_a.row_subject", "row_subject"}} {
			s := fixture.RegtypeSelect("SELECT $1::regtype AS a", fact.output)
			s.Args = []any{fixture.RegtypeIdentifier(current) + fact.suffix}
			fixture.RegtypeBoth(t, p, conn, recorder, s)
		}
		for _, input := range []string{"regtype_a.sequence_subject", fixture.RegtypeIdentifier(current) + ".absent_regtype_schema.int4"} {
			rows, err := conn.QueryContext(p.Ctx, "SELECT $1::regtype AS r", input)
			if rows != nil {
				rows.Close()
				t.Error("failed lookup exposed rows")
			}
			state := "42704"
			if input != "regtype_a.sequence_subject" {
				state = "3F000"
			}
			fixture.RegtypeAssertError(t, err, state, "", "SELECT $1::regtype AS r", "currentdb/sequence", "execute")
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO information_schema,pg_catalog", 0))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect("SELECT 'sql_identifier'::regtype AS a,'sql_identifier[]'::regtype AS b", "sql_identifier", "sql_identifier[]"))
		// No row identity for ordinary sequences; CREATE OR REPLACE retains view identity.
		var before, after int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'regtype_a.row_view'::regtype::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE OR REPLACE VIEW regtype_a.row_view AS SELECT v,d FROM regtype_a.row_subject", 0))
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'regtype_a.row_view'::regtype::bigint").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("replace view changed row identity", before, after)
		}
	})
}
