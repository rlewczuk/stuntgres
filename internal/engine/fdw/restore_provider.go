package fdw

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// RebindReplacementProviders points every imported foreign procedure in next
// at the destination registry's token for the same name and signature, so
// execution authority stays in the destination installation. Neither imported labels/OIDs nor the presence of a live catalog
// declaration selects a callback: Reset must reseed even a dropped registration.
// Registry lookup happens only while constructing the private replacement;
// published execution continues to use immutable procedure tokens.
func RebindReplacementProviders(current catalog.Read, registry Registry, next *catalog.State) error {
	type key struct {
		name      catalog.ForeignFunctionName
		signature catalog.ProcedureSignature
	}
	var installed map[key]*catalog.ForeignProcedure
	for _, descriptor := range next.Descriptors() {
		imported, ok := descriptor.(*catalog.Procedure)
		if !ok || imported.Foreign == nil {
			continue
		}
		if installed == nil {
			installed = make(map[key]*catalog.ForeignProcedure)
			for token := range registry.ProcedureTokens() {
				signature := catalog.ForeignValidatorSignature
				if token.Wrapper != nil {
					signature = catalog.ForeignHandlerSignature
				}
				k := key{token.Name, signature}
				if installed[k] != nil {
					return fmt.Errorf("stuntgres: duplicate destination foreign provider")
				}
				installed[k] = token
			}
		}
		// The registry authorizes only the genuine immutable handler/validator
		// shape. It does not grant a callable capability to an arbitrary carrier.
		if err := (stored.ProcedureProvider{}).Validate(current.WithState(next), imported); err != nil {
			return err
		}
		destination := installed[key{imported.Foreign.Name, imported.Signature}]
		if destination == nil || imported.RoutineKind != catalog.FunctionRoutine || !imported.Strict || imported.Volatility != expr.Volatile || imported.SQL != nil || imported.SQLBody != nil || imported.SQLDefaults != nil || imported.Aggregate != nil || imported.Implementation != nil || imported.AccessMethod != catalog.NoAccessMethod || imported.Dictionary != (ids.ObjectAddress{}) || len(imported.Body) != 0 || (destination.Wrapper != nil) != (imported.Foreign.Wrapper != nil) || (destination.Validator != nil) != (imported.Foreign.Validator != nil) {
			return fmt.Errorf("stuntgres: replacement foreign provider is not installed in destination")
		}
		if imported.Foreign != destination {
			replacement := *imported
			replacement.Foreign = destination
			next.SetDescriptor(imported.Address(), &replacement)
		}
	}
	return nil
}
