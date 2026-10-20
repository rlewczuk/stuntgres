package catalog

// IsSystemColumn reports whether name is one of the system column names
// every table reserves.
func IsSystemColumn(name string) bool {
	switch name {
	case "ctid", "xmin", "cmin", "xmax", "cmax", "tableoid":
		return true
	}
	return false
}
