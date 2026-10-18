package engine

// Typed views of a member descriptor's extension implementation. Each returns
// the implementation when it has that concrete type, and nil otherwise, so a
// nil check still asks "was this installed by that extension".

func (d *procedureDescriptor) unaccentProvider() *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}

func (d *procedureDescriptor) intarrayProvider() *intarrayProvider {
	p, _ := d.Implementation.(*intarrayProvider)
	return p
}

func (d *procedureDescriptor) fuzzystrmatchProvider() *fuzzystrmatchProvider {
	p, _ := d.Implementation.(*fuzzystrmatchProvider)
	return p
}

func (d *procedureDescriptor) pgcryptoProvider() *pgcryptoProvider {
	p, _ := d.Implementation.(*pgcryptoProvider)
	return p
}

func (d *procedureDescriptor) intaggProvider() *intaggProvider {
	p, _ := d.Implementation.(*intaggProvider)
	return p
}

func (d *procedureDescriptor) uuidOsspProvider() *uuidOsspProvider {
	p, _ := d.Implementation.(*uuidOsspProvider)
	return p
}

func (d *tsTemplateDescriptor) unaccentProvider() *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}

func (d *tsDictionaryDescriptor) unaccentProvider() *unaccentProvider {
	p, _ := d.Implementation.(*unaccentProvider)
	return p
}
