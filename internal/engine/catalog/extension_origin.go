package catalog

import "stuntgres/internal/engine/ids"

// Installation is an immutable released recipe. Cohort is logical provenance,
// not a foreign key to a currently installed extension. An UPDATE may publish a
// new recipe while surviving declarations retain their original release recipe.
// Members is the original ordered declaration inventory, never SQL membership.
type Installation struct {
	Cohort            ids.ObjectAddress
	Provider, Version string
	Implementation    ExtensionImplementation
	Members           []ids.ObjectAddress
	// Declarations are immutable pre-origin snapshots of genuine released
	// logical declarations. They spell even source-deleted baseline objects
	// during replay; historical owner/ACL fields grant no current authority.
	// They are never published or executed as live catalog descriptors.
	Declarations []Descriptor
	sealed       *Installation
}

func (i *Installation) Address() ids.ObjectAddress { return i.Cohort }
func (i *Installation) Name() string               { return i.Provider }

// NewInstallation accepts an immutable, detached recipe inventory owned by the
// installation stage. A copied/forged record is not declaration authority.
func NewInstallation(cohort ids.ObjectAddress, provider, version string, implementation ExtensionImplementation, members []ids.ObjectAddress) *Installation {
	i := &Installation{Cohort: cohort, Provider: provider, Version: version, Implementation: implementation, Members: members}
	i.sealed = i
	return i
}

func (i *Installation) Valid() bool {
	return i != nil && i.sealed == i && i.Cohort.ClassID == ids.ExtensionClassID && i.Cohort.ObjectID != 0 && i.Cohort.SubID == 0 && len(i.Members) > 0 && len(i.Declarations) == len(i.Members)
}

// InstalledOrigin binds a descriptor to one declaration in a genuine staged
// installation. Names, owners, namespaces and current membership are unrelated
// to this authority. Publication and remap preserve the shared recipe backing.
type InstalledOrigin struct {
	Installation *Installation
	Declaration  int
}

func (o *InstalledOrigin) Valid(address ids.ObjectAddress) bool {
	return o != nil && o.Installation.Valid() && o.Declaration >= 0 && o.Declaration < len(o.Installation.Members) && o.Installation.Members[o.Declaration] == address && o.Installation.Declarations[o.Declaration] != nil && o.Installation.Declarations[o.Declaration].Address() == address && DescriptorOrigin(o.Installation.Declarations[o.Declaration]) == nil
}

func DescriptorOrigin(d Descriptor) *InstalledOrigin {
	switch d := d.(type) {
	case *Procedure:
		return d.Origin
	case *Type:
		return d.Origin
	case *Operator:
		return d.Origin
	case *OperatorClass:
		return d.Origin
	case *OperatorFamily:
		return d.Origin
	case *TSTemplate:
		return d.Origin
	case *TSDictionary:
		return d.Origin
	case *Language:
		return d.Origin
	case *Cast:
		return d.Origin
	}
	return nil
}

// WithInstalledOrigin returns a detached descriptor. It never installs an
// implementation or manufactures a declaration from a public SQL label.
func WithInstalledOrigin(d Descriptor, origin *InstalledOrigin) Descriptor {
	switch d := d.(type) {
	case *Procedure:
		n := *d
		n.Origin = origin
		return &n
	case *Type:
		n := *d
		n.Origin = origin
		return &n
	case *Operator:
		n := *d
		n.Origin = origin
		return &n
	case *OperatorClass:
		n := *d
		n.Origin = origin
		return &n
	case *OperatorFamily:
		n := *d
		n.Origin = origin
		return &n
	case *TSTemplate:
		n := *d
		n.Origin = origin
		return &n
	case *TSDictionary:
		n := *d
		n.Origin = origin
		return &n
	case *Language:
		n := *d
		n.Origin = origin
		return &n
	case *Cast:
		n := *d
		n.Origin = origin
		return &n
	}
	return d
}

// SameInstallation compares logical cohort identity, not membership or release.
// Surviving objects from an earlier UPDATE release still share the cohort.
func SameInstallation(d Descriptor, installation *Installation) bool {
	if d == nil || !installation.Valid() {
		return false
	}
	o := DescriptorOrigin(d)
	return o.Valid(d.Address()) && o.Installation.Cohort == installation.Cohort && o.Installation.Implementation == installation.Implementation
}

func OriginInstallation(d Descriptor) (*Installation, bool) {
	if d == nil {
		return nil, false
	}
	o := DescriptorOrigin(d)
	if !o.Valid(d.Address()) {
		return nil, false
	}
	return o.Installation, true
}
