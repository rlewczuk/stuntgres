package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoParametersFormals(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		id := ipCreateMix(t, p, b, c)
		irCheck(t, p, b, c, ipProjection(b.PG, ipIdentityWhere(id), ipMix(db, "public", id, "9")))
		cases := []struct {
			ddl, name, signature string
			rows                 func(string) [][]any
		}{
			{"CREATE FUNCTION ip_unnamed(integer,text) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "ip_unnamed", "integer, text", func(id string) [][]any {
				return [][]any{ipRow(db, "public", id, 1, nil, "IN", "integer", "pg_catalog", "int4", nil), ipRow(db, "public", id, 2, nil, "IN", "text", "pg_catalog", "text", nil)}
			}},
			{"CREATE FUNCTION ip_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 1'", "ip_out", "", func(id string) [][]any {
				return [][]any{ipRow(db, "public", id, 1, "answer", "OUT", "integer", "pg_catalog", "int4", nil)}
			}},
			{"CREATE FUNCTION ip_zero() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "ip_zero", "", func(string) [][]any { return nil }},
			{"CREATE FUNCTION ip_table(a integer) RETURNS TABLE(k integer,v text) LANGUAGE SQL AS 'SELECT a,a::text'", "ip_table", "integer", func(id string) [][]any {
				return [][]any{ipRow(db, "public", id, 1, "a", "IN", "integer", "pg_catalog", "int4", nil), ipRow(db, "public", id, 2, "k", "OUT", "integer", "pg_catalog", "int4", nil), ipRow(db, "public", id, 3, "v", "OUT", "text", "pg_catalog", "text", nil)}
			}},
			{"CREATE FUNCTION ip_variadic(VARIADIC a integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "ip_variadic", "integer[]", func(id string) [][]any {
				return [][]any{ipRow(db, "public", id, 1, "a", "IN", "ARRAY", "pg_catalog", "_int4", nil)}
			}},
			{"CREATE FUNCTION ip_pl(a integer) RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN a; END'", "ip_pl", "integer", func(id string) [][]any {
				return [][]any{ipRow(db, "public", id, 1, "a", "IN", "integer", "pg_catalog", "int4", nil)}
			}},
			{"CREATE PROCEDURE ip_proc(a integer,b text) LANGUAGE SQL AS 'SELECT 1'", "ip_proc", "integer, text", func(id string) [][]any {
				return [][]any{ipRow(db, "public", id, 1, "a", "IN", "integer", "pg_catalog", "int4", nil), ipRow(db, "public", id, 2, "b", "IN", "text", "pg_catalog", "text", nil)}
			}},
		}
		for _, x := range cases {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(x.ddl, 0))
			identity := rsuIdentity(t, p, c, "public", x.name, x.signature)
			irCheck(t, p, b, c, ipProjection(b.PG, ipIdentityWhere(identity), x.rows(identity)))
		}
		for _, x := range []struct {
			name, signature    string
			oid                int
			names              []any
			modes, types, udts []string
		}{
			{"abs", "integer", 1397, []any{nil}, []string{"IN"}, []string{"integer"}, []string{"int4"}},
			{"sum", "integer", 2108, []any{nil}, []string{"IN"}, []string{"integer"}, []string{"int4"}},
			{"lag", "integer", 3106, []any{nil}, []string{"IN"}, []string{"anyelement"}, []string{"anyelement"}},
			{"jsonb_each", "jsonb", 3219, []any{"from_json", "key", "value"}, []string{"IN", "OUT", "OUT"}, []string{"jsonb", "text", "jsonb"}, []string{"jsonb", "text", "jsonb"}},
		} {
			// The polymorphic signature is declared, not the call-site integer type.
			signature := x.signature
			if x.name == "lag" {
				signature = "anyelement"
			}
			identity := rsuIdentity(t, p, c, "pg_catalog", x.name, signature)
			var want [][]any
			for i := range x.names {
				want = append(want, ipRow(db, "pg_catalog", identity, i+1, x.names[i], x.modes[i], x.types[i], "pg_catalog", x.udts[i], nil))
			}
			irCheck(t, p, b, c, ipProjection(b.PG, "specific_schema='pg_catalog' AND "+ipIdentityWhere(identity), want))
		}
		zero := rsuIdentity(t, p, c, "pg_catalog", "row_number", "")
		irCheck(t, p, b, c, ipProjection(b.PG, ipIdentityWhere(zero), nil))
		long := strings.Repeat("é", 28) + "long"
		for _, typ := range []string{"integer", "text"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION \""+long+"\"(x "+typ+") RETURNS "+typ+" LANGUAGE SQL AS 'SELECT x'", 0))
			identity := rsuIdentity(t, p, c, "public", long, typ)
			if len(identity) > 63 || !strings.HasPrefix(identity, "é") {
				t.Fatalf("specific identity %q", identity)
			}
			udt := typ
			if typ == "integer" {
				udt = "int4"
			}
			irCheck(t, p, b, c, ipProjection(b.PG, ipIdentityWhere(identity), [][]any{ipRow(db, "public", identity, 1, "x", "IN", typ, "pg_catalog", udt, nil)}))
		}
	})
}

func TestReferenceInfoParametersTypes(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		for _, q := range []string{"CREATE DOMAIN ip_domain AS integer", "CREATE DOMAIN ip_array_domain AS integer[]", "CREATE FUNCTION ip_types(a integer,b varchar(7),c numeric(8,2),d timestamptz(2),e integer[],f ip_domain,g ip_domain[],h ip_array_domain) RETURNS integer LANGUAGE SQL AS 'SELECT a'"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		id := rsuIdentity(t, p, c, "public", "ip_types", "integer, character varying, numeric, timestamp with time zone, integer[], ip_domain, ip_domain[], ip_array_domain")
		var arrayName string
		if err := c.QueryRowContext(p.Ctx, "SELECT a.typname FROM pg_catalog.pg_type d JOIN pg_catalog.pg_type a ON a.oid=d.typarray WHERE d.oid='ip_domain'::regtype").Scan(&arrayName); err != nil {
			t.Fatal(err)
		}
		var want [][]any
		for i, x := range []struct{ name, data, schema, udt string }{{"a", "integer", "pg_catalog", "int4"}, {"b", "character varying", "pg_catalog", "varchar"}, {"c", "numeric", "pg_catalog", "numeric"}, {"d", "timestamp with time zone", "pg_catalog", "timestamptz"}, {"e", "ARRAY", "pg_catalog", "_int4"}, {"f", "USER-DEFINED", "public", "ip_domain"}, {"g", "ARRAY", "public", arrayName}, {"h", "USER-DEFINED", "public", "ip_array_domain"}} {
			want = append(want, ipRow(db, "public", id, i+1, x.name, "IN", x.data, x.schema, x.udt, nil))
		}
		irCheck(t, p, b, c, ipProjection(b.PG, ipIdentityWhere(id), want))
	})
}
