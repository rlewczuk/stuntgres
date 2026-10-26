package catalog

import (
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestOperatorFamilySupportVisibility(t *testing.T) {
	if (OperatorFamilySupport{}).CatalogVisible {
		t.Fatal("default publication must deny")
	}
	// Finite implementation facts, independent of the seed initializer/helper.
	implemented := map[ids.OID]bool{}
	for _, oid := range []ids.OID{350, 351, 354, 355, 356, 358, 359, 360, 377, 400, 425, 432, 441, 442, 443, 444, 445, 446, 447, 448, 449, 450, 451, 452, 453, 454, 455, 780, 836, 842, 926, 949, 972, 1078, 1080, 1092, 1107, 1314, 1315, 1358, 1688, 1693, 1696, 1697, 1769, 1954, 2039, 2045, 2166, 2180, 2233, 2234, 2794, 2960, 2963, 3251, 3409, 3410, 3411, 3412, 3414, 3416, 3418, 3514, 3515, 4044, 4045, 5096, 6413, 6414, 6415, 6416, 6417, 6418, 6419, 6420, 6421, 6422, 6423, 6424, 6425, 6426, 246, 253, 2188, 2189, 2190, 2191, 2192, 2193, 2194, 2195, 2344, 2357, 2370, 2383, 2526, 2533, 181, 3383, 3384, 3385, 3386} {
		implemented[oid] = true
	}
	view := familyCatalogFixture()
	count := 0
	for _, descriptor := range view.Descriptors() {
		if d, ok := descriptor.(*OperatorFamilySupport); ok {
			count++
			if d.CatalogVisible != implemented[d.Function] {
				t.Fatal("eligibility", d)
			}
			selected, ok := view.FamilySupport(d.Family, d.Left, d.Right, d.Number)
			if !ok || selected != d {
				t.Fatal("internal membership lost", d)
			}
		}
	}
	if count != 376 {
		t.Fatal("internal inventory", count)
	}
	for _, key := range []struct {
		family, left, right ids.OID
		slot                int16
	}{{1976, 23, 23, 2}, {3253, 3220, 3220, 4}} {
		d, ok := view.FamilySupport(key.family, key.left, key.right, key.slot)
		if !ok || d.CatalogVisible {
			t.Fatal("metadata-only internal consumer", d)
		}
	}
}
