package reference_test

import "testing"

func TestReferencePGStatUserTablesFeatureSmoke(t *testing.T) {
	tableStatFeatureSmoke(t, tableStatFamily{user: true})
}
