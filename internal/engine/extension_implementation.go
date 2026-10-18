package engine

// Typed views of a member descriptor's extension implementation. Each returns
// the implementation when it has that concrete type, and nil otherwise, so a
// nil check still asks "was this installed by that extension".

func (d *procedureDescriptor) unaccentProvider() *unaccentProvider {
	p, _ := d.implementation.(*unaccentProvider)
	return p
}

func (d *procedureDescriptor) intarrayProvider() *intarrayProvider {
	p, _ := d.implementation.(*intarrayProvider)
	return p
}

func (d *procedureDescriptor) fuzzystrmatchProvider() *fuzzystrmatchProvider {
	p, _ := d.implementation.(*fuzzystrmatchProvider)
	return p
}

func (d *procedureDescriptor) pgcryptoProvider() *pgcryptoProvider {
	p, _ := d.implementation.(*pgcryptoProvider)
	return p
}

func (d *procedureDescriptor) intaggProvider() *intaggProvider {
	p, _ := d.implementation.(*intaggProvider)
	return p
}

func (d *procedureDescriptor) uuidOsspProvider() *uuidOsspProvider {
	p, _ := d.implementation.(*uuidOsspProvider)
	return p
}

func (d *tsTemplateDescriptor) unaccentProvider() *unaccentProvider {
	p, _ := d.implementation.(*unaccentProvider)
	return p
}

func (d *tsDictionaryDescriptor) unaccentProvider() *unaccentProvider {
	p, _ := d.implementation.(*unaccentProvider)
	return p
}
