package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoArmorArraysAndNulls(t *testing.T) {
	for _, tc := range []struct{ name, keys, values, code, message string }{
		{"key_rank_two", "{{foo}}", "{bar}", "2202E", "wrong number of array subscripts"},
		{"value_rank_two", "{foo}", "{{bar}}", "2202E", "wrong number of array subscripts"},
		{"both_rank_two", "{{foo}}", "{{bar}}", "2202E", "wrong number of array subscripts"},
		{"empty_keys", "{}", "{bar}", "2202E", "wrong number of array subscripts"},
		{"empty_values", "{foo}", "{}", "2202E", "wrong number of array subscripts"},
		{"unequal_counts", "{foo,second}", "{bar}", "2202E", "mismatched array dimensions"},
		{"null_key", "{NULL}", "{bar}", "22004", "null value not allowed for header key"},
		{"null_value", "{foo}", "{NULL}", "22004", "null value not allowed for header value"},
		{"non_ascii_key", "{é}", "{bar}", "22023", "header key must not contain non-ASCII characters"},
		{"non_ascii_value", "{foo}", "{界}", "22023", "header value must not contain non-ASCII characters"},
		{"colon_space_key", "{\"foo: bar\"}", "{bar}", "22023", "header key must not contain \": \""},
		{"newline_key", "{\"foo\nbar\"}", "{bar}", "22023", "header key must not contain newlines"},
		{"newline_value", "{foo}", "{\"bar\nbaz\"}", "22023", "header value must not contain newlines"},
		{"key_before_value", "{NULL}", "{NULL}", "22004", "null value not allowed for header key"},
		{"ascii_before_colon_newline", "{\"é: \n\"}", "{NULL}", "22023", "header key must not contain non-ASCII characters"},
		{"colon_before_newline", "{\"foo: \n\"}", "{NULL}", "22023", "header key must not contain \": \""},
		{"key_before_same_value", "{\"foo\n\"}", "{NULL}", "22023", "header key must not contain newlines"},
		{"earlier_value_before_later_key", "{foo,NULL}", "{NULL,bar}", "22004", "null value not allowed for header value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorError("SELECT armor($1,$2,$3)", tc.code, tc.message, []byte("zooka"), tc.keys, tc.values))
				literal := "SELECT armor('zooka'::bytea,'" + tc.keys + "'::text[],'" + tc.values + "'::text[])"
				pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorError(literal, tc.code, tc.message))
			})
		})
	}
	t.Run("physical_pairs_and_controls", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1,$2,$3)", pgcryptoArmorTest, []byte("test"), "{}", "{}"))
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1,$2,$3)", fixture.PgcryptoArmorZooka, []byte("zooka"), "[0:0]={foo}", "[7:7]={bar}"))
			packet := "-----BEGIN PGP MESSAGE-----\nfoo: first\nfoo: second\n: \ncr\r\t\x01: value\r\t\x02\n\ndGVzdA==\n=+G7Q\n-----END PGP MESSAGE-----\n"
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1,$2,$3)", packet, []byte("test"), "{foo,foo,\"\",\"cr\r\t\x01\"}", "{first,second,\"\",\"value\r\t\x02\"}"))
		})
	})
	t.Run("strict_nulls_and_demand", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT armor(NULL::bytea)", []string{"armor"}, []string{"TEXT"}, [][]any{{nil}}),
				fixture.Select("SELECT dearmor(NULL::text)", []string{"dearmor"}, []string{"BYTEA"}, [][]any{{nil}}),
				fixture.Select("SELECT armor($1)", []string{"armor"}, []string{"TEXT"}, [][]any{{nil}}, nil),
				fixture.Select("SELECT dearmor($1)", []string{"dearmor"}, []string{"BYTEA"}, [][]any{{nil}}, nil),
				fixture.Select("SELECT armor($1,$2,$3)", []string{"armor"}, []string{"TEXT"}, [][]any{{nil}}, nil, "{NULL}", "{bad}"),
				fixture.Select("SELECT armor($1,$2,$3)", []string{"armor"}, []string{"TEXT"}, [][]any{{nil}}, []byte("test"), nil, "{{bad}}"),
				fixture.Select("SELECT armor($1,$2,$3)", []string{"armor"}, []string{"TEXT"}, [][]any{{nil}}, []byte("test"), "{NULL}", nil),
				fixture.Select("SELECT armor('test'::bytea,NULL::text[],ARRAY[NULL]::text[])", []string{"armor"}, []string{"TEXT"}, [][]any{{nil}}),
				fixture.Select("SELECT armor('test'::bytea,ARRAY[NULL]::text[],NULL::text[])", []string{"armor"}, []string{"TEXT"}, [][]any{{nil}}),
				fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers(NULL::text)", [][]any{}),
				fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers($1)", [][]any{}, nil),
			} {
				pgcryptoArmorBoth(t, p, c, r, s)
			}
			pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorError("SELECT armor($1::bytea,ARRAY[($2::integer/$3::integer)::text],ARRAY['value'])", "22012", "division by zero", nil, 1, 0))
			pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorError("SELECT armor(NULL::bytea,ARRAY[(1/0)::text],ARRAY['value'])", "22012", "division by zero"))
			pgcryptoArmorBoth(t, p, c, r, fixture.Select("SELECT CASE WHEN false THEN dearmor('not armor') ELSE NULL::bytea END", []string{"case"}, []string{"BYTEA"}, [][]any{{nil}}))
		})
	})
	t.Run("failed_statement_atomicity", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE TABLE armor_atomic(id integer,b bytea)", 0))
			run(fixture.Exec("INSERT INTO armor_atomic VALUES(0,'sentinel'::bytea)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.PgcryptoArmorCorrupt("INSERT INTO armor_atomic VALUES(1,dearmor($1)),(2,dearmor($2))", pgcryptoArmorTest, "invalid")
				s.SelectRows = false
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.Select("SELECT id,b FROM armor_atomic ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "sentinel"}}))
			}
		})
	})
}
