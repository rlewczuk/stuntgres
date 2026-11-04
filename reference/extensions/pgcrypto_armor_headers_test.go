package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoArmorHeaders(t *testing.T) {
	for _, tc := range []struct {
		name   string
		packet any
		rows   [][]any
	}{
		{"null", nil, [][]any{}},
		{"no_headers", pgcryptoArmorTest, [][]any{}},
		{"empty_data", pgcryptoArmorEmpty, [][]any{}},
		{"one_header", fixture.PgcryptoArmorZooka, [][]any{{"foo", "bar"}}},
		{"bad_crc", strings.Replace(fixture.PgcryptoArmorZooka, "=D5cR", "=ZZZZ", 1), [][]any{{"foo", "bar"}}},
		{"bad_body", strings.Replace(fixture.PgcryptoArmorZooka, "em9va2E=", "!not-base64!", 1), [][]any{{"foo", "bar"}}},
		{"missing_crc", strings.Replace(fixture.PgcryptoArmorZooka, "=D5cR\n", "", 1), [][]any{{"foo", "bar"}}},
		{"no_body_or_crc", "-----BEGIN PGP MESSAGE-----\nfoo: bar\n\n-----END PGP MESSAGE-----\n", [][]any{{"foo", "bar"}}},
		// Extraction's header scan also stops at the footer boundary; unlike
		// dearmor, it needs neither a blank separator nor body/checksum bytes.
		{"missing_separator", "-----BEGIN PGP MESSAGE-----\nfoo: bar\n-----END PGP MESSAGE-----\n", [][]any{{"foo", "bar"}}},
		{"duplicates_empty_split_spaces_utf8", "-----BEGIN PGP MESSAGE-----\nfoo: bar\nfoo: bar\n: empty key\nempty: \ninsane:key : text value here\n leading :  trailing \nfoo: a: b\né: 界\nlong: " + strings.Repeat("x", 160) + "\n\n!bad body!\n=ZZZZ\n-----END PGP MESSAGE-----\n", [][]any{{"foo", "bar"}, {"foo", "bar"}, {"", "empty key"}, {"empty", ""}, {"insane:key ", "text value here"}, {" leading ", " trailing "}, {"foo", "a: b"}, {"é", "界"}, {"long", strings.Repeat("x", 160)}}},
		{"crlf", "-----BEGIN PGP MESSAGE-----\r\nfoo: bar\r\nspace: value \r\ncr: value\r\r\n\r\n!bad!\r\n-----END PGP MESSAGE-----", [][]any{{"foo", "bar"}, {"space", "value "}, {"cr", "value\r"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers($1)", tc.rows, tc.packet))
				pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorHeaders("SELECT h.key,h.value FROM pgp_armor_headers($1) AS h", tc.rows, tc.packet))
			})
		})
	}
	t.Run("source_routes_and_stored_view", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			pgcryptoArmorBoth(t, p, c, r, fixture.Select("SELECT pgp_armor_headers($1)", []string{"pgp_armor_headers"}, []string{"RECORD"}, [][]any{{"(foo,bar)"}}, fixture.PgcryptoArmorZooka))
			pgcryptoArmorBoth(t, p, c, r, fixture.Select("SELECT k,v FROM pgp_armor_headers($1) AS h(k,v)", []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka))
			sorted := fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers($1) ORDER BY key,value", [][]any{{"Comment", "a"}, {"Version", "b"}}, pgcryptoArmorBadBody)
			sorted.Bag = false
			pgcryptoArmorBoth(t, p, c, r, sorted)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW armor_headers_view AS SELECT key,value FROM pgp_armor_headers('"+fixture.PgcryptoArmorZooka+"')", 0))
			// Stored-view nullability uses the view contract, not the computed-source
			// helper's unknown metadata rule.
			pgcryptoArmorBoth(t, p, c, r, fixture.Select("SELECT key,value FROM armor_headers_view ORDER BY key,value", []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"foo", "bar"}}))
		})
	})
	for _, tc := range []struct{ name, packet string }{
		{"missing_footer", "-----BEGIN PGP MESSAGE-----\nfoo: bar\n\n!bad!\n"},
		{"missing_begin", "foo: bar\n\n!bad!\n-----END PGP MESSAGE-----\n"},
		{"bad_marker", "-----BEGIN PGP MESSAGE----- \nfoo: bar\n\n!bad!\n-----END PGP MESSAGE-----\n"},
		{"invalid_header", "-----BEGIN PGP MESSAGE-----\nfoo: bar\ninvalid\n\n!bad!\n-----END PGP MESSAGE-----\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorCorrupt("SELECT * FROM pgp_armor_headers($1)", tc.packet))
			})
		})
	}
}
