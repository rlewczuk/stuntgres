package routine

import "stuntgres/internal/engine/ids"

// GinProfile is the immutable runtime authority for a native GIN family. It is
// shared by support binding, retained replacement and presentation so the
// non-contiguous jsonb strategy numbers and the ordinary path_ops slot-1
// comparator are described once.
type GinProfileSupport struct {
	Slot      int16
	Routine   ids.OID
	Intrinsic bool
}

type GinProfileStrategy struct {
	Number, Left, Right, Operator ids.OID
}

type GinProfile struct {
	Family     ids.OID
	Input      ids.OID
	Supports   []GinProfileSupport
	Strategies []GinProfileStrategy
}

var (
	ginProfileArraySupports = [...]GinProfileSupport{
		{2, 2743, true}, {3, 2774, true}, {4, 2744, true}, {6, 3920, true},
	}
	ginProfileArrayStrategies = [...]GinProfileStrategy{
		{1, 2277, 2277, 2750}, {2, 2277, 2277, 2751}, {3, 2277, 2277, 2752}, {4, 2277, 2277, 1070},
	}
	ginProfileVectorSupports = [...]GinProfileSupport{
		{1, 3724, true}, {2, 3656, true}, {3, 3657, true}, {4, 3658, true}, {5, 2700, true}, {6, 3921, true},
	}
	ginProfileVectorStrategies = [...]GinProfileStrategy{
		{1, 3614, 3615, 3636}, {2, 3614, 3615, 3660},
	}
	ginProfileJSONBOpsSupports = [...]GinProfileSupport{
		{1, 3480, true}, {2, 3482, true}, {3, 3483, true}, {4, 3484, true}, {6, 3488, true},
	}
	ginProfileJSONBOpsStrategies = [...]GinProfileStrategy{
		{7, 3802, 3802, 3246}, {9, 3802, 25, 3247}, {10, 3802, 1009, 3248},
		{11, 3802, 1009, 3249}, {15, 3802, 4072, 4012}, {16, 3802, 4072, 4013},
	}
	ginProfileJSONBPathOpsSupports = [...]GinProfileSupport{
		// Slot 1 of jsonb_path_ops is the ordinary installed btint4cmp, not a
		// GIN extraction/consistency intrinsic.
		{1, 351, false}, {2, 3485, true}, {3, 3486, true}, {4, 3487, true}, {6, 3489, true},
	}
	ginProfileJSONBPathOpsStrategies = [...]GinProfileStrategy{
		{7, 3802, 3802, 3246}, {15, 3802, 4072, 4012}, {16, 3802, 4072, 4013},
	}
	ginProfiles = [...]GinProfile{
		{Family: 2745, Input: 2277, Supports: ginProfileArraySupports[:], Strategies: ginProfileArrayStrategies[:]},
		{Family: 3659, Input: 3614, Supports: ginProfileVectorSupports[:], Strategies: ginProfileVectorStrategies[:]},
		{Family: 4036, Input: 3802, Supports: ginProfileJSONBOpsSupports[:], Strategies: ginProfileJSONBOpsStrategies[:]},
		{Family: 4037, Input: 3802, Supports: ginProfileJSONBPathOpsSupports[:], Strategies: ginProfileJSONBPathOpsStrategies[:]},
	}
)

// LookupGinProfile returns the native family profile by released family identity.
// Unknown families (installed extension classes) use their own binding path.
func LookupGinProfile(family ids.OID) (GinProfile, bool) {
	for i := range ginProfiles {
		if ginProfiles[i].Family == family {
			return ginProfiles[i], true
		}
	}
	return GinProfile{}, false
}

// GinProfileSupportFor returns the declared support slot, if any.
func (p GinProfile) SupportFor(slot int16) (GinProfileSupport, bool) {
	for i := range p.Supports {
		if p.Supports[i].Slot == slot {
			return p.Supports[i], true
		}
	}
	return GinProfileSupport{}, false
}