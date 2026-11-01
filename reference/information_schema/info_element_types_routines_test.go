package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesRoutines(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		etExec(t, p, b, c,
			"CREATE DOMAIN et_int AS integer",
			"CREATE DOMAIN et_array AS integer[]",
			"CREATE FUNCTION et_fun(a integer[],b et_array,c et_int[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT a'",
		)
		id := rsuIdentity(t, p, c, "public", "et_fun", "integer[], et_array, et_int[]")
		where := "object_schema='public' AND object_name='" + id + "'"
		want := [][]any{
			etRow(database, "public", id, "ROUTINE", 0, "integer", "pg_catalog", "int4"),
			etRow(database, "public", id, "ROUTINE", 1, "integer", "pg_catalog", "int4"),
			etRow(database, "public", id, "ROUTINE", 3, "USER-DEFINED", "public", "et_int"),
		}
		etCheck(t, p, b, c, etProjection(b.PG, where, want))
		etCheck(t, p, b, c, fixture.Select(`SELECT p.parameter_name,e.collection_type_identifier,e.dtd_identifier FROM information_schema.parameters p JOIN information_schema.element_types e ON e.object_catalog=p.specific_catalog AND e.object_schema=p.specific_schema AND e.object_name=p.specific_name AND e.object_type='ROUTINE' AND e.collection_type_identifier=p.dtd_identifier WHERE p.specific_schema='public' AND p.specific_name='`+id+`' ORDER BY p.ordinal_position`, []string{"parameter_name", "collection_type_identifier", "dtd_identifier"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"a", "1", "a1"}, {"c", "3", "a3"}}))
		etCheck(t, p, b, c, fixture.Select(`SELECT r.routine_name,e.collection_type_identifier,e.dtd_identifier FROM information_schema.routines r JOIN information_schema.element_types e ON e.object_catalog=r.specific_catalog AND e.object_schema=r.specific_schema AND e.object_name=r.specific_name AND e.object_type='ROUTINE' AND e.collection_type_identifier=r.dtd_identifier WHERE r.specific_schema='public' AND r.specific_name='`+id+`'`, []string{"routine_name", "collection_type_identifier", "dtd_identifier"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"et_fun", "0", "a0"}}))

		for _, x := range []struct {
			name, signature, ddl string
			elements             []struct {
				ordinal           int
				data, schema, udt string
			}
		}{
			{"et_out", "", "CREATE FUNCTION et_out(OUT answer integer[]) LANGUAGE SQL AS 'SELECT ARRAY[1]'", []struct {
				ordinal           int
				data, schema, udt string
			}{{0, "integer", "pg_catalog", "int4"}, {1, "integer", "pg_catalog", "int4"}}},
			{"et_inout", "integer[]", "CREATE FUNCTION et_inout(INOUT a integer[]) LANGUAGE SQL AS 'SELECT a'", []struct {
				ordinal           int
				data, schema, udt string
			}{{0, "integer", "pg_catalog", "int4"}, {1, "integer", "pg_catalog", "int4"}}},
			{"et_multi", "integer", "CREATE FUNCTION et_multi(IN a integer,OUT x text[],OUT y integer) LANGUAGE SQL AS 'SELECT ARRAY[a::text],a'", []struct {
				ordinal           int
				data, schema, udt string
			}{{2, "text", "pg_catalog", "text"}}},
			{"et_table", "integer[]", "CREATE FUNCTION et_table(a integer[]) RETURNS TABLE(k integer,v text[]) LANGUAGE SQL AS $$SELECT 1,ARRAY['x']$$", []struct {
				ordinal           int
				data, schema, udt string
			}{{1, "integer", "pg_catalog", "int4"}, {3, "text", "pg_catalog", "text"}}},
			{"et_variadic", "integer[]", "CREATE FUNCTION et_variadic(VARIADIC a integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", []struct {
				ordinal           int
				data, schema, udt string
			}{{1, "integer", "pg_catalog", "int4"}}},
			{"et_proc", "integer[]", "CREATE PROCEDURE et_proc(a integer[]) LANGUAGE SQL AS 'SELECT 1'", []struct {
				ordinal           int
				data, schema, udt string
			}{{1, "integer", "pg_catalog", "int4"}}},
			{"et_domain_result", "", "CREATE FUNCTION et_domain_result() RETURNS et_array LANGUAGE SQL AS 'SELECT ARRAY[1]::et_array'", nil},
			{"et_domain_element", "", "CREATE FUNCTION et_domain_element() RETURNS et_int[] LANGUAGE SQL AS 'SELECT ARRAY[1::et_int]'", []struct {
				ordinal           int
				data, schema, udt string
			}{{0, "USER-DEFINED", "public", "et_int"}}},
			{"et_default_mix", "integer[], bigint[]", "CREATE FUNCTION et_default_mix(IN a integer[],OUT b text[],INOUT c bigint[] DEFAULT '{}') LANGUAGE SQL AS 'SELECT ARRAY[a::text],c'", []struct {
				ordinal           int
				data, schema, udt string
			}{{1, "integer", "pg_catalog", "int4"}, {2, "text", "pg_catalog", "text"}, {3, "bigint", "pg_catalog", "int8"}}},
			{"et_pl", "integer[]", "CREATE FUNCTION et_pl(a integer[]) RETURNS integer[] LANGUAGE plpgsql AS 'BEGIN RETURN a; END'", []struct {
				ordinal           int
				data, schema, udt string
			}{{0, "integer", "pg_catalog", "int4"}, {1, "integer", "pg_catalog", "int4"}}},
		} {
			t.Run(x.name, func(t *testing.T) {
				etExec(t, p, b, c, x.ddl)
				identity := rsuIdentity(t, p, c, "public", x.name, x.signature)
				var expected [][]any
				for _, element := range x.elements {
					expected = append(expected, etRow(database, "public", identity, "ROUTINE", element.ordinal, element.data, element.schema, element.udt))
				}
				etCheck(t, p, b, c, etProjection(b.PG, "object_schema='public' AND object_name='"+identity+"'", expected))
			})
		}
		etCheck(t, p, b, c, etProjection(b.PG, "object_schema='pg_catalog' AND object_name='parse_ident_1268'", [][]any{etRow(database, "pg_catalog", "parse_ident_1268", "ROUTINE", 0, "text", "pg_catalog", "text")}))
		etCheck(t, p, b, c, etProjection(b.PG, "object_schema='pg_catalog' AND object_name='array_sort_6388'", nil))
	})
}
