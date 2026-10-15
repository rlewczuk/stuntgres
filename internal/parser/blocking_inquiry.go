package parser

import "strings"

// BlockingInquiryFunctionName admits only the two session blocker inquiries,
// preserving quoted spelling for the ordinary signature diagnostic.
func BlockingInquiryFunctionName(name string) bool {
	return strings.EqualFold(name, "pg_blocking_pids") || strings.EqualFold(name, "pg_safe_snapshot_blocking_pids")
}
