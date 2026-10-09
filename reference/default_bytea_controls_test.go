package reference_test

import (
	"testing"
)

func TestReferenceDefaultByteaControls(t *testing.T) {
	// The existing logical reference fixture records BYTEA as raw strings.
	// Native public controls separately assert actual []byte and its metadata.
	want := []any{string([]byte{0, 128, 255, 92, 39}), ""}
	defaultConsumerReferenceControls(t, `CREATE TABLE d(id INT,v BYTEA DEFAULT '\x0080ff5c27',e BYTEA DEFAULT '\x')`, `DEFAULT '\x0080ff5c27'`, []string{"id", "v", "e"}, []string{"INT4", "BYTEA", "BYTEA"}, want, want)
}
