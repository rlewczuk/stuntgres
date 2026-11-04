package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// InitializeIndexMethod is the genuine zero-argument handler factory. Its
// returned finite native authority is consumed by the operation dispatcher;
// ambuild, aminsert and other raw method pointers are not SQL procedures.
func InitializeIndexMethod(c *Context, oid ids.OID) (catalog.IndexMethod, error) {
	if c == nil {
		return 0, fmt.Errorf("stuntgres: missing access method factory owner")
	}
	if c.Context == nil {
		return 0, fmt.Errorf("stuntgres: missing access method execution context")
	}
	if err := c.Context.Err(); err != nil {
		return 0, err
	}
	method, ok := c.Catalog.AccessMethod(oid)
	if !ok {
		return 0, fmt.Errorf("stuntgres: unavailable index access method")
	}
	if err := (catalog.AccessMethodProvider{}).Validate(c.Catalog, method); err != nil {
		return 0, err
	}
	if method.MethodKind() != "i" {
		return 0, fmt.Errorf("stuntgres: invalid index access method factory")
	}
	if owner := c.Catalog.Owner(); owner != nil && owner.Sepgsql != nil {
		handler, ok := c.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
		if !ok {
			return 0, ids.ErrCatalogReplaced
		}
		site := DirectRoutineInitializationSite{Routine: handler, Reference: owner.Domain.Ref(c.Catalog.Epoch(), handler.Address()), Purpose: DirectAccessMethod, Source: owner.Domain.Ref(c.Catalog.Epoch(), method.Address())}
		if err := BeginDirectRoutineInitialization(c, site); err != nil {
			return 0, err
		}
		token, err := c.EnterInitializedDirectRoutine(site)
		if err != nil {
			return 0, err
		}
		defer token.Leave()
	}
	// This is the actual native static factory, not a user kernel. In
	// particular the trusted subject ends before any index key expression.
	implementation, ok := method.IndexImplementation()
	if !ok {
		return 0, fmt.Errorf("stuntgres: unavailable index operation authority")
	}
	return implementation, nil
}
