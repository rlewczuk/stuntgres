//go:build !noservertests

package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerFrontendEncoding(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			bad := string([]byte{0xff})
			for _, c := range []struct {
				name    string
				packet  []byte
				message string
			}{
				{"parse_statement", serverParse(bad, "SELECT 99"), "0xff"},
				{"parse_name_before_count", serverPacket('P', serverCString(serverCString(nil, bad), "SELECT 99")), "0xff"},
				{"parse_source_before_unnamed_mutation", serverParse("", bad), "0xff"},
				{"parse_continuation", serverParse(string([]byte{0xc3, 0x28}), "SELECT 99"), "0xc3 0x28"},
				{"parse_truncated_utf8", serverParse(string([]byte{0xe2, 0x82}), "SELECT 99"), "0xe2 0x82"},
				{"bind_portal", serverBind(bad, "missing", nil, nil), "0xff"},
				{"bind_statement", serverBind("p", bad, nil, nil), "0xff"},
				{"bind_portal_before_fields", serverPacket('B', serverCString(nil, bad)), "0xff"},
				{"describe_statement", serverDescribe('S', bad), "0xff"},
				{"describe_portal", serverDescribe('P', bad), "0xff"},
				{"close_statement", serverClose('S', bad), "0xff"},
				{"close_portal", serverClose('P', bad), "0xff"},
				{"execute_portal", serverExecute(bad, 0), "0xff"},
				{"execute_name_before_count", serverPacket('E', serverCString(nil, bad)), "0xff"},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(backend))
					// Protocol names are case-sensitive opaque text, not SQL identifiers.
					statement := "Résumé_東京_" + strings.Repeat("x", 70)
					portal := "Portál_東京_" + strings.Repeat("y", 70)
					frames := serverCycle(t, w, serverParse(statement, "SELECT 42"), serverBind(portal, statement, nil, nil), serverExecute(portal, 0))
					serverWantTags(t, frames, "12DCZ")
					serverNotificationSuccess(t, frames, 'I', "SELECT 1")
					serverWantValues(t, frames, [][]string{{"42"}})
					frames = serverCycle(t, w, serverParse("", "SELECT 43"))
					serverWantTags(t, frames, "1Z")
					serverNotificationSuccess(t, frames, 'I')

					frames = serverCycle(t, w, c.packet, serverParse("encoding_skipped", "SELECT 99"))
					serverWantTags(t, frames, "EZ")
					serverWantReady(t, frames, 'I')
					serverNotificationError(t, frames, "22021", `invalid byte sequence for encoding "UTF8": `+c.message, "")

					// Decoder failure must not mutate the existing unnamed definition.
					frames = serverCycle(t, w, serverBind("", "", nil, nil), serverExecute("", 0))
					serverWantTags(t, frames, "2DCZ")
					serverNotificationSuccess(t, frames, 'I', "SELECT 1")
					serverWantValues(t, frames, [][]string{{"43"}})
					frames = serverCycle(t, w, serverDescribe('S', "encoding_skipped"))
					serverWantTags(t, frames, "EZ")
					serverWantReady(t, frames, 'I')
					serverNotificationError(t, frames, "26000", `prepared statement "encoding_skipped" does not exist`, "")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
