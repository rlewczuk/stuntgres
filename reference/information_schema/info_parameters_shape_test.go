package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoParametersShape(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		id := ipCreateMix(t, p, b, c)
		for _, s := range []fixture.Step{ipProjection(b.PG, ipIdentityWhere(id), ipMix(db, "public", id, "9")), ipProjection(b.PG, "specific_name='ip_absent'", nil), ipProjection(b.PG, "FALSE", nil), ipProjection(b.PG, "NULL", nil)} {
			irCheck(t, p, b, c, s)
			rows, err := c.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			ipMetadata(t, types, b.PG)
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			ipMetadata(t, types, b.PG)
		}
		var attributes [][]any
		for i, name := range ipColumns {
			base, collation, typmod := int64(19), int64(950), int64(-1)
			switch ipDomains[i] {
			case "character_data":
				base = 1043
			case "yes_or_no":
				base, typmod = 1043, 7
			case "cardinal_number":
				base, collation = 23, 0
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", ipDomains[i], "d", base, int64(-1), false, collation, int64(0), false, typmod})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,t.typtypmod FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.parameters'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "INT4"}, attributes))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.parameters'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		irCheck(t, p, b, c, fixture.Select("SELECT p.* FROM (SELECT 1 AS n) x LEFT JOIN information_schema.parameters p ON p.specific_name='ip_absent'", ipColumns, ipTypes, [][]any{make([]any, 32)}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT v.definition=pg_catalog.pg_get_viewdef('information_schema.parameters'::regclass) AS same_definition FROM pg_catalog.pg_views v WHERE v.schemaname='information_schema' AND v.viewname='parameters'`, []string{"same_definition"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT p.ordinal_position,p.dtd_identifier,r.routine_name FROM information_schema.parameters p JOIN information_schema.routines r ON r.specific_schema=p.specific_schema AND r.specific_name=p.specific_name WHERE p.specific_name='`+id+`' ORDER BY p.ordinal_position`, []string{"ordinal_position", "dtd_identifier", "routine_name"}, []string{"INT4", "NAME", "NAME"}, [][]any{{int64(1), "1", "ip_mix"}, {int64(2), "2", "ip_mix"}, {int64(3), "3", "ip_mix"}}))
	})
}

func TestReferenceInfoParametersPrepareBoundary(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		id := ipCreateMix(t, p, b, c)
		s := ipProjection(b.PG, "specific_schema='public'", ipMix(db, "public", id, "9"))
		held := rsuHeld(t, p, c, b.Recorder, s.Query)
		irCheck(t, p, b, c, s)
		held(s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE ip_statement(integer,text) AS SELECT $1,$2", 0))
		irCheck(t, p, b, c, s)
		held(s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DEALLOCATE ip_statement", 0))
		irCheck(t, p, b, c, s)
		held(s)
		bound := fixture.Select("SELECT parameter_name FROM information_schema.parameters WHERE specific_name=$1 ORDER BY ordinal_position", []string{"parameter_name"}, []string{"NAME"}, [][]any{{"a"}, {"b"}, {"c"}})
		bound.Args = []any{id}
		irCheck(t, p, b, c, bound)
	})
}

func TestReferenceInfoParametersDiagnostics(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		id := ipCreateMix(t, p, b, c)
		held := rsuHeld(t, p, c, b.Recorder, ipProjection(b.PG, ipIdentityWhere(id), nil).Query)
		for _, x := range []struct{ query, state, message, position string }{
			{"SELECT absent FROM information_schema.parameters", "42703", `column "absent" does not exist`, "8"},
			{"SELECT p.parameter_name FROM information_schema.parameters q", "42P01", `missing FROM-clause entry for table "p"`, "8"},
			{"SELECT NULL::ip_missing_type FROM information_schema.parameters", "42704", `type "ip_missing_type" does not exist`, "14"},
			{"SELECT * FROM information_schema.parameters WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer", "51"},
			{"/*é*/ SELECT absent FROM information_schema.parameters", "42703", `column "absent" does not exist`, "14"},
		} {
			d := fixture.ControlsReferenceDiagnostic(x.state, x.message)
			d.Position = x.position
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(x.query, d)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			held(ipProjection(b.PG, ipIdentityWhere(id), ipMix(db, "public", id, "9")))
		}
	})
}
