package catalog

import "stuntgres/internal/engine/ids"

// ExtensionControl is a registered extension's control-file metadata.
type ExtensionControl struct {
	Version                         string
	Trusted, Superuser, Relocatable bool
	Comment, Schema                 *string
	Requires                        []string
}

// Detached returns a copy that shares no storage with c.
func (c ExtensionControl) Detached() ExtensionControl {
	if c.Comment != nil {
		value := *c.Comment
		c.Comment = &value
	}
	if c.Schema != nil {
		value := *c.Schema
		c.Schema = &value
	}
	c.Requires = append([]string(nil), c.Requires...)
	return c
}

// ExtensionImplementation is one registered extension. The catalog owner
// record and every member descriptor hold the same instance, so pointer
// identity proves which implementation installed a member.
type ExtensionImplementation interface {
	// Control returns the extension's control metadata as a detached copy.
	Control() ExtensionControl
	// Versions returns detached explicit bases and directed script metadata.
	Versions() ExtensionVersions
	// ValidateExtension checks an installed extension's manifest.
	ValidateExtension(Read, *Extension) error
}

// ProcedureDependencyProvider visits genuine released SQL-body references.
// The read view resolves prerequisite identities from sealed declaration origins,
// never current membership or names. False propagates the caller's early stop.
type ProcedureDependencyProvider interface {
	VisitProcedureDependencies(Read, *Procedure, func(ids.ObjectAddress) bool) bool
}
