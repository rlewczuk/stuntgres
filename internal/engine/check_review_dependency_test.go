package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
)

func checkReviewDependencyValid(t *testing.T, e *core.Engine) {
	t.Helper()
	read := e.CatalogRead()
	for _, descriptor := range read.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, c := range d.Constraints.Check {
			for _, dep := range c.Dependencies {
				if err := validateObjectRef(e, read, dep.Ref); err != nil {
					t.Fatalf("orphan CHECK %s on %s: %v", c.Name, d.Name(), err)
				}
			}
		}
	}
	var dump strings.Builder
	if err := Dump(context.Background(), e, &dump); err != nil {
		t.Fatal("unusable catalog", err)
	}
}
