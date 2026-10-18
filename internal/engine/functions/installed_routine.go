package functions

import "stuntgres/internal/engine/expr"

// Extension names an extension whose installed routines bind here. The engine
// maps it to the provider that owns installation and procedure identity.
type Extension uint8

const (
	UUIDOsspExtension Extension = iota + 1
)

// InstalledRoutine is an installed extension routine resolved by the engine.
// The engine owns its procedure identity; bind code validates it and attaches
// it to the node it builds.
type InstalledRoutine interface {
	// Validate checks the routine's installation manifest.
	Validate() error
	// Attach completes call's routine identity, records the routine as a
	// dependency of the expression being bound and returns the engine's
	// identity-carrying node.
	Attach(call expr.Expr) (expr.Expr, error)
}
