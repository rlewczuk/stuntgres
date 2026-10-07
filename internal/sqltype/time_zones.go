package sqltype

// PostgreSQL pg_get_timezone_offset requires agreement across the entire
// history, not merely the offset at today's date.
func fixedTimeZone(name string) bool {
	zone, err := ResolveZone(name)
	if err != nil {
		return false
	}
	return zone.isFixed()
}
