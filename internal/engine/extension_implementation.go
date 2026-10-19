package engine

import "stuntgres/internal/engine/catalog"

// Typed views of a member descriptor's extension implementation. Each returns
// the implementation when it has that concrete type, and nil otherwise, so a
// nil check still asks "was this installed by that extension".

func unaccentProviderOf(d *catalog.Procedure) *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}

func intarrayProviderOf(d *catalog.Procedure) *intarrayProvider {
	p, _ := d.Implementation.(*intarrayProvider)
	return p
}

func cubeProviderOf(d *catalog.Procedure) *cubeProvider {
	p, _ := d.Implementation.(*cubeProvider)
	return p
}

func fuzzystrmatchProviderOf(d *catalog.Procedure) *fuzzystrmatchProvider {
	p, _ := d.Implementation.(*fuzzystrmatchProvider)
	return p
}

func pgcryptoProviderOf(d *catalog.Procedure) *pgcryptoProvider {
	p, _ := d.Implementation.(*pgcryptoProvider)
	return p
}

func intaggProviderOf(d *catalog.Procedure) *intaggProvider {
	p, _ := d.Implementation.(*intaggProvider)
	return p
}

func uuidOsspProviderOf(d *catalog.Procedure) *uuidOsspProvider {
	p, _ := d.Implementation.(*uuidOsspProvider)
	return p
}

func unaccentTemplateProviderOf(d *catalog.TSTemplate) *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}

func unaccentDictionaryProviderOf(d *catalog.TSDictionary) *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}
