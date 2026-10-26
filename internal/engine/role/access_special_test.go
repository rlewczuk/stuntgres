package role

import "testing"

func TestAccessSpecialParameterNames(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"SORT_MEM", "work_mem"}, {"vacuum_MEM", "maintenance_work_mem"}, {"SSL_ECDH_CURVE", "ssl_groups"}, {"LC_MESSAGES", "lc_messages"}, {"", ""}, {"unknown", "unknown"}, {"Custom.DOTTED", "custom.dotted"}, {"É.NAME", "É.name"}} {
		if got := CanonicalInquiryParameter(tc.input); got != tc.want {
			t.Fatalf("%q: %q want %q", tc.input, got, tc.want)
		}
	}
}
