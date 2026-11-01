package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoArmorMalformed(t *testing.T) {
	for _, tc := range []struct{ name, packet string }{
		{"bad_checksum", strings.Replace(pgcryptoArmorZooka, "=D5cR", "=ZZZZ", 1)},
		{"missing_checksum", strings.Replace(pgcryptoArmorZooka, "=D5cR\n", "", 1)},
		{"missing_begin", "foo: bar\n\nem9va2E=\n=D5cR\n-----END PGP MESSAGE-----\n"},
		{"missing_footer", strings.Replace(pgcryptoArmorZooka, "-----END PGP MESSAGE-----\n", "", 1)},
		{"invalid_begin_suffix", strings.Replace(pgcryptoArmorZooka, "MESSAGE-----\n", "MESSAGE----- \n", 1)},
		{"indented_begin", " " + pgcryptoArmorZooka},
		{"inline_begin", "prefix " + pgcryptoArmorZooka},
		{"no_separator", strings.Replace(pgcryptoArmorZooka, "bar\n\n", "bar\n", 1)},
		{"truncated_quartet", strings.Replace(pgcryptoArmorTest, "dGVzdA==", "dGVzdA=", 1)},
		{"invalid_base64", strings.Replace(pgcryptoArmorZooka, "em9va2E=", "!m9va2E=", 1)},
		{"invalid_crc_characters", strings.Replace(pgcryptoArmorZooka, "=D5cR", "=!!!!", 1)},
		{"short_crc", strings.Replace(pgcryptoArmorZooka, "=D5cR", "=D5c", 1)},
		{"crc_trailing_equals", strings.Replace(pgcryptoArmorTest, "=+G7Q", "=+G7Qjunk=", 1)},
		{"changed_payload", strings.Replace(pgcryptoArmorZooka, "em9va2E=", "e29va2E=", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorCorrupt("SELECT dearmor($1)", tc.packet))
				literal := "SELECT dearmor('" + tc.packet + "')"
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorCorrupt(literal))
				// The same connection must remain usable after either failure route.
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1)", []byte("test"), pgcryptoArmorTest))
			})
		})
	}
	for _, tc := range []struct{ name, packet string }{
		{"malformed_first_header", strings.Replace(pgcryptoArmorZooka, "foo: bar", "foo:", 1)},
		{"malformed_later_header", strings.Replace(pgcryptoArmorZooka, "foo: bar", "foo: bar\nbad", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1)", []byte("zooka"), tc.packet))
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorCorrupt("SELECT * FROM pgp_armor_headers($1)", tc.packet))
				// Assert no partial first header is returned before the later error.
				for _, prepared := range []bool{false, true} {
					t.Run(map[bool]string{false: "eager_direct", true: "eager_prepared"}[prepared], func(t *testing.T) {
						var rows *sql.Rows
						var err error
						if prepared {
							stmt, e := c.PrepareContext(p.Ctx, "SELECT * FROM pgp_armor_headers($1)")
							if e != nil {
								t.Fatal("parameterized prepare", e)
							}
							defer stmt.Close()
							rows, err = stmt.QueryContext(p.Ctx, tc.packet)
						} else {
							rows, err = c.QueryContext(p.Ctx, "SELECT * FROM pgp_armor_headers($1)", tc.packet)
						}
						if rows != nil {
							defer rows.Close()
							if rows.Next() {
								t.Fatal("invalid later header exposed a row")
							}
							if err == nil {
								err = rows.Err()
							}
						}
						d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39000", Message: "Corrupt ascii-armor"}
						fixture.AssertDiagnostic(t, err, &d)
					})
				}
			})
		})
	}
}
