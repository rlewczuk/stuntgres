package reference_test

import "testing"

func TestReferencePGStatUserTablesSnapshotsAndLifetime(t *testing.T) {
	tableStatSnapshotsAndLifetime(t, tableStatFamily{user: true})
}
func TestReferencePGStatUserTablesNativeControls(t *testing.T) {
	tableStatNativeControls(t, tableStatFamily{user: true})
}
