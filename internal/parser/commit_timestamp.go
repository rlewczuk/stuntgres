package parser

import "strings"

// CommitTimestampFunctionName selects these ordinary scalar calls in every
// existing expression lane, including record-valued calls in FROM.
func CommitTimestampFunctionName(name string) bool {
	return strings.EqualFold(name, "pg_xact_commit_timestamp") || strings.EqualFold(name, "pg_xact_commit_timestamp_origin") || strings.EqualFold(name, "pg_last_committed_xact")
}
