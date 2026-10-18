package engine

// Typed views of a member descriptor's extension implementation. Each returns
// the implementation when it has that concrete type, and nil otherwise, so a
// nil check still asks "was this installed by that extension".

func unaccentProviderOf(d *procedureDescriptor) *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}

func intarrayProviderOf(d *procedureDescriptor) *intarrayProvider {
	p, _ := d.Implementation.(*intarrayProvider)
	return p
}

func fuzzystrmatchProviderOf(d *procedureDescriptor) *fuzzystrmatchProvider {
	p, _ := d.Implementation.(*fuzzystrmatchProvider)
	return p
}

func pgcryptoProviderOf(d *procedureDescriptor) *pgcryptoProvider {
	p, _ := d.Implementation.(*pgcryptoProvider)
	return p
}

func intaggProviderOf(d *procedureDescriptor) *intaggProvider {
	p, _ := d.Implementation.(*intaggProvider)
	return p
}

func uuidOsspProviderOf(d *procedureDescriptor) *uuidOsspProvider {
	p, _ := d.Implementation.(*uuidOsspProvider)
	return p
}

func unaccentTemplateProviderOf(d *tsTemplateDescriptor) *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}

func unaccentDictionaryProviderOf(d *tsDictionaryDescriptor) *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}
