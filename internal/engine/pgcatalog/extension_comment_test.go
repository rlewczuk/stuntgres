package pgcatalog

import (
	"testing"

	"stuntgres/internal/engine/catalog"
)

func TestExtensionCommentProjection(t *testing.T) {
	// The installed value, not a provider-name fallback, is authoritative.
	for _, text := range []string{"functions for implementing referential integrity (obsolete)", "explicit override", ""} {
		ext := &catalog.Extension{ObjectName: "refint", Comment: text}
		if got := DescriptorComment(ext, 0); got != text {
			t.Errorf("stored comment %q: got %q", text, got)
		}
		if got := Description(ext, 0); got != text {
			t.Errorf("description for stored comment %q: got %q", text, got)
		}
		if got := DescriptorComment(ext, 1); got != "" {
			t.Errorf("extension subobject acquired comment %q", got)
		}
	}
}
