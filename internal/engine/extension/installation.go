package extension

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ValidateCurrentInstallation checks released provenance and current reciprocal
// membership independently. Complete recipe checks run only at install/update
// admission; removing a declaration does not invalidate its surviving siblings.
func ValidateCurrentInstallation(v catalog.Read, ext *catalog.Extension, implementation catalog.ExtensionImplementation) error {
	if ext == nil || !ext.Installation.Valid() || v.Owner() == nil || v.Owner().Extensions[ext.Name()] != implementation || ext.Installation.Implementation != implementation || ext.Installation.Provider != ext.Name() || ext.Installation.Version != ext.Version || ext.Installation.Cohort != ext.Address() || implementation.Versions().InstallPath(ext.Version) == nil {
		return fmt.Errorf("stuntgres: invalid extension installation provenance")
	}
	if err := catalog.ValidateExtensionMembership(v, ext); err != nil {
		return err
	}
	for _, a := range ext.Installation.Members {
		d := v.Get(a)
		if d == nil {
			continue
		}
		if !catalog.SameInstallation(d, ext.Installation) {
			return fmt.Errorf("stuntgres: invalid installed declaration provenance")
		}
		if err := validateInstalledDeclaration(v, d); err != nil {
			return err
		}
		if class, ok := d.(*catalog.OperatorClass); ok && class.Origin.Installation.Implementation == StockCube {
			if err := validateCubeClassBindings(v, class, class.Origin.Installation.Members); err != nil {
				return err
			}
		}
		if class, ok := d.(*catalog.OperatorClass); ok && class.Origin.Installation.Implementation == StockHstore {
			if err := validateHstoreClassBindings(v, class, class.Origin.Installation.Members); err != nil {
				return err
			}
		}
		if class, ok := d.(*catalog.OperatorClass); ok && class.Origin.Installation.Implementation == StockLtree {
			if err := ValidateLtreeClassBindings(v, class); err != nil {
				return err
			}
		}
	}
	return nil
}

// SealInstallation is called on a complete unpublished script candidate. New
// declarations acquire immutable release provenance; surviving UPDATE objects
// retain their original declaration recipe. This is not membership mutation.
func SealInstallation(stage Stage, ext *catalog.Extension, recipe []ids.ObjectAddress) error {
	implementation := stage.Read().Owner().Extensions[ext.Name()]
	previous := ext.Installation
	installation := catalog.NewInstallation(ext.Address(), ext.Name(), ext.Version, implementation, slices.Clone(recipe))
	installation.Declarations = make([]catalog.Descriptor, len(recipe))
	var previousDeclarations map[ids.ObjectAddress]catalog.Descriptor
	if previous != nil {
		previousDeclarations = make(map[ids.ObjectAddress]catalog.Descriptor, len(previous.Members))
		for i, a := range previous.Members {
			if i < len(previous.Declarations) {
				previousDeclarations[a] = previous.Declarations[i]
			}
		}
	}
	ext.Installation = installation
	for i, a := range recipe {
		d := stage.Get(a)
		canonical := previousDeclarations[a]
		if canonical == nil {
			if d == nil || catalog.DescriptorOrigin(d) != nil {
				return fmt.Errorf("stuntgres: missing canonical installed declaration")
			}
			canonical = d
		}
		installation.Declarations[i] = canonical
		if d == nil {
			continue
		}
		if catalog.DescriptorOrigin(d) != nil {
			continue
		}
		n := catalog.WithInstalledOrigin(d, &catalog.InstalledOrigin{Installation: installation, Declaration: i})
		if n == d {
			return fmt.Errorf("stuntgres: unsupported installed declaration class")
		}
		if err := stage.Replace(n); err != nil {
			return err
		}
	}
	return nil
}

func (p *AmcheckProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *CitextProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *CubeProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *EarthdistanceProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *FuzzystrmatchProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *HstoreProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *IntaggProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *IntarrayProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *IsnProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *LoProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *LtreeProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *PgcryptoProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *PgrowlocksProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *SegProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *UnaccentProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *UUIDOsspProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}

func ValidateProcedureOrigin(v catalog.Read, p *catalog.Procedure, implementation catalog.ExtensionImplementation) error {
	if p == nil || p.Implementation != implementation {
		return InvalidProviderDescriptor()
	}
	_, err := ProcedureSymbol(v, p)
	return err
}
