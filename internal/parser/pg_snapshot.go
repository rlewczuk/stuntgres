package parser

import "strings"

func SnapshotFunctionName(name string) bool {
	switch strings.ToLower(name) {
	case "pg_current_snapshot", "pg_snapshot_xmin", "pg_snapshot_xmax", "pg_snapshot_xip", "pg_visible_in_snapshot":
		return true
	}
	return false
}
