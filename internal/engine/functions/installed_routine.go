package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// Extension names an extension whose installed routines bind here. The engine
// maps it to the provider that owns installation and procedure identity.
type Extension uint8

const (
	UUIDOsspExtension Extension = iota + 1
	PgcryptoExtension
	FuzzystrmatchExtension
	UnaccentExtension
	IntaggExtension
	PgrowlocksExtension
	TablefuncExtension
	AmcheckExtension
	CubeExtension
	EarthdistanceExtension
	PageinspectExtension
	TcnExtension
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

// CatalogBuiltin is the routine InstalledRoutine returns when the search path
// reaches pg_catalog before any namespace holding the installed routine.
// pgcrypto's gen_random_uuid then binds as the core builtin.
type CatalogBuiltin struct{}

func (CatalogBuiltin) Validate() error { return nil }

func (CatalogBuiltin) Attach(expr.Expr) (expr.Expr, error) {
	return nil, fmt.Errorf("stuntgres: a catalog builtin has no installed identity")
}

// installedArgumentNames spells analyzed arguments for 42883 diagnostics.
func installedArgumentNames(b Binder, raw parser.FunctionExpr, operands []Argument) []string {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = "unknown"
		if o.Usage.Type != nil {
			names[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	return names
}
