package fixture

// Domain wire results flatten to the installed cube base, confirmed by the
// root's pinned PostgreSQL18.6 oracle run.
func EarthSelect(b Backend, q string, columns []string, rows [][]any, args ...any) Step {
	return CubeSelect(b, q, columns, rows, args...)
}
