package enginetests

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestProcedureCommentProjection(t *testing.T) {
	for _, oid := range []ids.OID{1346, 1368, 1738, 2169, 90001} {
		d := &catalog.Procedure{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid},
			ObjectName:    "pow",
			Comment:       "exponentiation",
		}
		// Clearing or replacing live text must never recover the released seed.
		for _, text := range []string{"exponentiation", "explicit override", ""} {
			d.Comment = text
			if got := pgcatalog.DescriptorComment(d, 0); got != text {
				t.Errorf("routine %d stored %q: got %q", oid, text, got)
			}
			if got := pgcatalog.Description(d, 0); got != text {
				t.Errorf("routine %d description for %q: got %q", oid, text, got)
			}
			for _, sub := range []int32{-1, 1, 2} {
				if got := pgcatalog.Description(d, sub); got != "" {
					t.Errorf("routine %d subobject %d acquired %q", oid, sub, got)
				}
			}
		}
	}
	if got := pgcatalog.Description(&catalog.Procedure{ObjectName: "power"}, 0); got != "" {
		t.Fatalf("unseeded name acquired comment %q", got)
	}
}
