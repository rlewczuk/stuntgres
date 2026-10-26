package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoParametersDefaults(t *testing.T) {
	t.Run("presentation", ipCheckDefaultPresentation)
	t.Run("checked", func(t *testing.T) {
		ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SEQUENCE ip_seq START 41",
				"CREATE FUNCTION ip_defaults(a integer DEFAULT 9, OUT label text, b integer DEFAULT NULL, c text DEFAULT 'O''Brien', d bigint DEFAULT nextval('ip_seq'::regclass)) LANGUAGE SQL AS 'SELECT c'",
			} {
				run(fixture.Exec(q, 0))
			}
			id := rsuIdentity(t, p, c, "public", "ip_defaults", "integer, integer, text, bigint")
			want := [][]any{ipRow(db, "public", id, 1, "a", "IN", "integer", "pg_catalog", "int4", "9"), ipRow(db, "public", id, 2, "label", "OUT", "text", "pg_catalog", "text", nil), ipRow(db, "public", id, 3, "b", "IN", "integer", "pg_catalog", "int4", "NULL::integer"), ipRow(db, "public", id, 4, "c", "IN", "text", "pg_catalog", "text", "'O''Brien'::text"), ipRow(db, "public", id, 5, "d", "IN", "bigint", "pg_catalog", "int8", "nextval('ip_seq'::regclass)")}
			s := ipProjection(b.PG, ipIdentityWhere(id), want)
			irCheck(t, p, b, c, s)
			held := rsuHeld(t, p, c, b.Recorder, s.Query)
			held(s)
			held(s)
			run(fixture.Exec("SET search_path=pg_catalog", 0))
			qualified := make([][]any, len(want))
			for i, row := range want {
				qualified[i] = append([]any(nil), row...)
			}
			qualified[4][31] = "nextval('public.ip_seq'::regclass)"
			held(ipProjection(b.PG, ipIdentityWhere(id), qualified))
			run(fixture.Exec("RESET search_path", 0))
			run(fixture.Select("SELECT nextval('ip_seq') AS first_value", []string{"first_value"}, []string{"INT8"}, [][]any{{int64(41)}}))
		})
	})
	// Independent finite release inventory. Each declaration has its own scenario
	// so fixture deadlines do not depend on the size of the complete inventory.
	for _, x := range []struct {
		oid      int
		name     string
		arity    int
		defaults map[int]string
	}{
		{3464, "make_interval", 7, map[int]string{1: "0", 2: "0", 3: "0", 4: "0", 5: "0", 6: "0", 7: "0.0"}},
		{3579, "jsonb_insert", 4, map[int]string{4: "false"}},
		{3261, "json_strip_nulls", 2, map[int]string{2: "false"}},
		{4005, "jsonb_path_exists", 4, map[int]string{3: "'{}'::jsonb", 4: "false"}},
		{4006, "jsonb_path_query", 4, map[int]string{3: "'{}'::jsonb", 4: "false"}},
		{4007, "jsonb_path_query_array", 4, map[int]string{3: "'{}'::jsonb", 4: "false"}},
		{4008, "jsonb_path_query_first", 4, map[int]string{3: "'{}'::jsonb", 4: "false"}},
		{4009, "jsonb_path_match", 4, map[int]string{3: "'{}'::jsonb", 4: "false"}},
		{3960, "json_populate_record", 3, map[int]string{3: "false"}},
		{3961, "json_populate_recordset", 3, map[int]string{3: "false"}},
		{4350, "normalize", 2, map[int]string{2: "'NFC'::text"}},
		{4351, "is_normalized", 2, map[int]string{2: "'NFC'::text"}},
		{6212, "random_normal", 2, map[int]string{1: "0", 2: "1"}},
		{3305, "jsonb_set", 4, map[int]string{4: "true"}},
		{5054, "jsonb_set_lax", 5, map[int]string{4: "true", 5: "'use_json_null'::text"}},
		{1268, "parse_ident", 2, map[int]string{2: "true"}},
		{6388, "array_sort", 1, nil}, {6389, "array_sort", 2, nil}, {6390, "array_sort", 3, nil},
	} {
		t.Run(fmt.Sprintf("%s_%d", x.name, x.oid), func(t *testing.T) {
			ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
				// Fixture superuser has owner-role USAGE, independently of PUBLIC EXECUTE.
				where := fmt.Sprintf("specific_schema='pg_catalog' AND specific_name='%s_%d'", x.name, x.oid)
				want := ipBuiltinDefaultRows(db, x.name, x.oid, x.defaults)
				if len(want) != x.arity {
					t.Fatalf("independent input signature width=%d want%d", len(want), x.arity)
				}
				s := ipProjection(b.PG, where, want)
				irCheck(t, p, b, c, s)
			})
		})
	}
	t.Run("installed", func(t *testing.T) {
		// Extension cleanup precedes schema cleanup; PUBLIC is reset by the fixture.
		ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION amcheck", 0))
			defer func() { _, _ = c.ExecContext(p.Ctx, "DROP EXTENSION amcheck") }()
			id := rsuIdentity(t, p, c, "public", "verify_heapam", "regclass, boolean, boolean, text, bigint, bigint")
			names := []string{"relation", "on_error_stop", "check_toast", "skip", "startblock", "endblock", "blkno", "offnum", "attnum", "msg"}
			data := []string{"regclass", "boolean", "boolean", "text", "bigint", "bigint", "bigint", "integer", "integer", "text"}
			udts := []string{"regclass", "bool", "bool", "text", "int8", "int8", "int8", "int4", "int4", "text"}
			defs := []any{nil, "false", "false", "'none'::text", "NULL::bigint", "NULL::bigint", nil, nil, nil, nil}
			var want [][]any
			for i, name := range names {
				mode := "IN"
				if i >= 6 {
					mode = "OUT"
				}
				want = append(want, ipRow(db, "public", id, i+1, name, mode, data[i], "pg_catalog", udts[i], defs[i]))
			}
			irCheck(t, p, b, c, ipProjection(b.PG, ipIdentityWhere(id), want))
		})
	})
}

func ipCheckDefaultPresentation(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN ip_default_domain AS integer", 0))
		run(fixture.Exec("CREATE FUNCTION ip_present(x ip_default_domain DEFAULT 9,s text DEFAULT E'a\\\\b',n integer DEFAULT '12'::integer) RETURNS integer LANGUAGE SQL AS 'SELECT n'", 0))
		id := rsuIdentity(t, p, c, "public", "ip_present", "ip_default_domain, text, integer")
		want := [][]any{ipRow(db, "public", id, 1, "x", "IN", "USER-DEFINED", "public", "ip_default_domain", "9"), ipRow(db, "public", id, 2, "s", "IN", "text", "pg_catalog", "text", "'a\\b'::text"), ipRow(db, "public", id, 3, "n", "IN", "integer", "pg_catalog", "int4", "12")}
		s := ipProjection(b.PG, ipIdentityWhere(id), want)
		irCheck(t, p, b, c, s)
		held := rsuHeld(t, p, c, b.Recorder, s.Query)
		run(fixture.Exec("SET standard_conforming_strings=off", 0))
		changed := [][]any{want[0], append([]any(nil), want[1]...), want[2]}
		changed[1][31] = "'a\\\\b'::text"
		held(ipProjection(b.PG, ipIdentityWhere(id), changed))
		run(fixture.Exec("RESET standard_conforming_strings", 0))
		held(s)
	})
}
