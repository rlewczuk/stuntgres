package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// UnaccentText follows the installed function's current schema, not the original
// member dictionary address. PostgreSQL's wrapper returns the first recognized
// lexeme, or the original token when the selected dictionary returns none.
func (h *statementHost) UnaccentText(procedure ids.ObjectRef, input string, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx := h.scope().Op.Context()
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	runtime, arrayType, err := h.defaultUnaccentDictionary(procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	lexemes, err := runtime.Lexize(ctx, input, arrayType)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !lexemes.IsNull() && lexemes.ArrayLen() != 0 {
		input, err = lexemes.ArrayElement(0).Text()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(result, input), ctx.Err()
}

// Capture immutable runtime authority under the ordinary current statement view;
// normalization/resource execution happens only after releasing Engine.mu.
func (h *statementHost) defaultUnaccentDictionary(reference ids.ObjectRef) (catalog.DictionaryRuntime, *sqltype.Descriptor, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := scope.namespaceAuthorizationLocked(e)
	if err != nil {
		return nil, nil, err
	}
	view := auth.Catalog
	procedure, ok := view.Procedure(reference.Address)
	if !ok || extension.UnaccentProviderOf(procedure) != extension.StockUnaccent {
		return nil, nil, fmt.Errorf("stuntgres: missing installed unaccent routine")
	}
	if err := (stored.ProcedureProvider{}).Validate(view, procedure); err != nil {
		return nil, nil, err
	}
	namespace, ok := view.Namespace(procedure.Namespace())
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing installed function namespace")
	}
	resolver := regobj.NewTextSearchInput(inputCapture(auth, nil))
	oid, err := resolver.ResolveRegdictionary(scope.Op.Context(), catalog.QuoteIdentifier(namespace.Name())+".unaccent")
	if err != nil {
		return nil, nil, err
	}
	dictionary, ok := view.TSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing resolved unaccent dictionary")
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, dictionary); err != nil {
		return nil, nil, err
	}
	arrayType, ok := view.LookupType(1009)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing dictionary result type")
	}
	return dictionary.Runtime, arrayType, nil
}
