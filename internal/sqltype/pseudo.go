package sqltype

// PolymorphicFamily identifies independent signature variables, not value codecs.
// Zero includes the unconstrained builtin "any", which is not polymorphic.
func PolymorphicFamily(id ID) int {
	switch id {
	case 2283, 2277, 2776, 3500, 3831, 4537:
		return 1
	case 5077, 5078, 5079, 5080, 4538:
		return 2
	}
	return 0
}

// IsPseudo identifies released pseudo identities without granting execution,
// storage or codec authority to lookup-only descriptors.
func IsPseudo(id ID) bool {
	if PolymorphicFamily(id) != 0 {
		return true
	}
	switch id {
	case 2276, 2275, 2281, 2280, 3115, 269, 325, 3310, 2249, 2279, 3838, 32, Void, Unknown:
		return true
	}
	return false
}

func pseudoPGTypeMetadata(d *Descriptor) (PGTypeMetadata, bool) {
	f := PGTypeMetadata{Kind: "p", Category: "P", Modifier: -1}
	switch d.id {
	case 2275:
		f.Input, f.Output, f.Receive, f.Send = 2292, 2293, 2500, 2501
	case 2276:
		f.Input, f.Output = 2294, 2295
	case 2283:
		f.Input, f.Output = 2312, 2313
	case 2776:
		f.Input, f.Output = 2777, 2778
	case 3500:
		f.Input, f.Output = 3504, 3505
	case 3831:
		f.Input, f.Output = 3832, 3833
	case 4537:
		f.Input, f.Output = 4229, 4230
	case 5077:
		f.Input, f.Output = 5086, 5087
	case 5078:
		f.Input, f.Output, f.Receive, f.Send = 5088, 5089, 5090, 5091
	case 5079:
		f.Input, f.Output = 5092, 5093
	case 5080:
		f.Input, f.Output = 5094, 5095
	case 4538:
		f.Input, f.Output = 4226, 4227
	case 2281:
		f.Input, f.Output = 2304, 2305
	case 2280:
		f.Input, f.Output = 2302, 2303
	case 3115:
		f.Input, f.Output = 3116, 3117
	case 269:
		f.Input, f.Output = 267, 268
	case 325:
		f.Input, f.Output = 326, 327
	case 3310:
		f.Input, f.Output = 3311, 3312
	case 2279:
		f.Input, f.Output = 2300, 2301
	case 3838:
		f.Input, f.Output = 3594, 3595
	case 32:
		f.Input, f.Output, f.Receive, f.Send = 86, 87, 88, 90
	default:
		return PGTypeMetadata{}, false
	}
	f.PostgreSQLAttributeFacts = d.AttributeFacts()
	return f, true
}
