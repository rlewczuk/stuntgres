package fixture

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// CatalogExtensionOwnerDirective selects a unique logical v3 cohort by provider
// and final presence. Stock plpgsql and absent older same-name cohorts are real
// records, not a reason to mutate whichever directive happens to occur first.
// It only inspects native public Dump output; it neither invokes engine APIs nor
// defines PostgreSQL expectations. end excludes the directive's newline.
func CatalogExtensionOwnerDirective(t *testing.T, script, provider string, present bool) (start, end int, data []byte) {
	t.Helper()
	const prefix = "-- stuntgres-extension-owners-v3 "
	start = -1
	for offset := 0; offset < len(script); {
		lineEnd := len(script)
		if n := strings.IndexByte(script[offset:], '\n'); n >= 0 {
			lineEnd = offset + n
		}
		line := script[offset:lineEnd]
		if strings.HasPrefix(line, prefix) {
			decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(line, prefix))
			if err != nil {
				t.Fatal("invalid emitted owner directive", err)
			}
			var identity struct {
				Name    string `json:"name"`
				Present bool   `json:"present"`
			}
			if err := json.Unmarshal(decoded, &identity); err != nil {
				t.Fatal("invalid emitted owner identity", err)
			}
			if identity.Name == provider && identity.Present == present {
				if start >= 0 {
					t.Fatal("duplicate emitted owner cohort", provider, present)
				}
				start, end, data = offset, lineEnd, decoded
			}
		}
		offset = lineEnd + 1
	}
	if start < 0 {
		t.Fatal("missing emitted owner cohort", provider, present)
	}
	return start, end, data
}
