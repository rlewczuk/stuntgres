package reference_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func clientSettingsNameTruncation(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		t := s.t
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, s.p, s.pg, &notices)
		actor := &physicalCase{t, s.p, conn, s.pg, recorder}
		for _, name := range []string{"default_table_access_method", "default_tablespace"} {
			for _, tc := range []struct{ input, clipped string }{
				{strings.Repeat("x", 64), strings.Repeat("x", 63)},
				{strings.Repeat("x", 62) + "é", strings.Repeat("x", 62)},
			} {
				for _, prepared := range []bool{false, true} {
					for _, function := range []bool{false, true} {
						query := "SET " + name + "=" + physicalLiteral(tc.input)
						if function {
							query = "SELECT set_config(" + physicalLiteral(name) + "," + physicalLiteral(tc.input) + ",false)"
						}
						detail := fmt.Sprintf(`Tablespace "%s" does not exist.`, tc.clipped)
						if name == "default_table_access_method" {
							detail = fmt.Sprintf(`Table access method "%s" does not exist.`, tc.clipped)
						}
						notices = nil
						actor.run(physicalError(query, clientInvalid(name, tc.clipped, detail, ""), prepared, function))
						want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, tc.input, tc.clipped)}}
						if !reflect.DeepEqual(notices, want) {
							t.Fatalf("notice before rejected hook: got %#v want %#v", notices, want)
						}
						initial := ""
						if name == "default_table_access_method" {
							initial = "heap"
						}
						actor.run(clientText("SHOW "+name, name, initial))
						actor.run(fixture.Exec("SET client_min_messages=warning", 0))
						notices = nil
						actor.run(physicalError(query, clientInvalid(name, tc.clipped, detail, ""), prepared, function))
						if len(notices) != 0 {
							t.Fatal("prior warning threshold failed to suppress NOTICE", notices)
						}
						actor.run(fixture.Exec("RESET client_min_messages", 0))
					}
				}
			}
		}
	})
}
