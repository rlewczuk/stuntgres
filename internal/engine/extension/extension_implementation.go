package extension

import "stuntgres/internal/engine/catalog"

// Typed views of a member descriptor's extension implementation. Each returns
// the implementation when it has that concrete type, and nil otherwise, so a
// nil check still asks "was this installed by that extension".

func UnaccentProviderOf(d *catalog.Procedure) *UnaccentProvider {
	p, _ := d.Implementation.(*UnaccentProvider)
	return p
}

func HstoreProviderOf(d *catalog.Procedure) *HstoreProvider {
	p, _ := d.Implementation.(*HstoreProvider)
	return p
}

func IntarrayProviderOf(d *catalog.Procedure) *IntarrayProvider {
	p, _ := d.Implementation.(*IntarrayProvider)
	return p
}

func LtreeProviderOf(d *catalog.Procedure) *LtreeProvider {
	p, _ := d.Implementation.(*LtreeProvider)
	return p
}

func CubeProviderOf(d *catalog.Procedure) *CubeProvider {
	p, _ := d.Implementation.(*CubeProvider)
	return p
}
func EarthdistanceProviderOf(d *catalog.Procedure) *EarthdistanceProvider {
	p, _ := d.Implementation.(*EarthdistanceProvider)
	return p
}

func CitextProviderOf(d *catalog.Procedure) *CitextProvider {
	p, _ := d.Implementation.(*CitextProvider)
	return p
}

func IsnProviderOf(d *catalog.Procedure) *IsnProvider {
	p, _ := d.Implementation.(*IsnProvider)
	return p
}

func FuzzystrmatchProviderOf(d *catalog.Procedure) *FuzzystrmatchProvider {
	p, _ := d.Implementation.(*FuzzystrmatchProvider)
	return p
}

func PgcryptoProviderOf(d *catalog.Procedure) *PgcryptoProvider {
	p, _ := d.Implementation.(*PgcryptoProvider)
	return p
}

func IntaggProviderOf(d *catalog.Procedure) *IntaggProvider {
	p, _ := d.Implementation.(*IntaggProvider)
	return p
}

func UUIDOsspProviderOf(d *catalog.Procedure) *UUIDOsspProvider {
	p, _ := d.Implementation.(*UUIDOsspProvider)
	return p
}

func UnaccentTemplateProviderOf(d *catalog.TSTemplate) *UnaccentProvider {
	p, _ := d.Implementation.(*UnaccentProvider)
	return p
}

func UnaccentDictionaryProviderOf(d *catalog.TSDictionary) *UnaccentProvider {
	p, _ := d.Implementation.(*UnaccentProvider)
	return p
}
