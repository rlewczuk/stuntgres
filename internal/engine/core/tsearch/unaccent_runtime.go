package tsearch

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Unaccent follows the installed function's current schema, not the original
// member dictionary address. PostgreSQL's wrapper returns the first recognized
// lexeme, or the original token when the selected dictionary returns none.
func Unaccent[A Authority[A]](h Host[A], procedure ids.ObjectRef, input string, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx := h.Operation().Context()
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := defaultUnaccentDictionary(h, procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	lexemes, err := runtime.Lexize(ctx, input)
	if err != nil {
		return sqltype.Value{}, err
	}
	if lexemes.Recognized && len(lexemes.Lexemes) != 0 {
		input = lexemes.Lexemes[0].Text
	}
	return sqltype.String(result, input), ctx.Err()
}

// Capture immutable runtime authority under the ordinary current statement view;
// normalization/resource execution happens only after releasing Engine.mu.
func defaultUnaccentDictionary[A Authority[A]](h Host[A], reference ids.ObjectRef) (catalog.DictionaryRuntime, error) {
	h.ReacquireEngine()
	defer h.ReleaseEngineLocked()
	view, resolver, err := h.TextSearchInputLocked()
	if err != nil {
		return nil, err
	}
	procedure, ok := view.Procedure(reference.Address)
	if !ok || extension.UnaccentProviderOf(procedure) != extension.StockUnaccent {
		return nil, fmt.Errorf("stuntgres: missing installed unaccent routine")
	}
	if err := (stored.ProcedureProvider{}).Validate(view, procedure); err != nil {
		return nil, err
	}
	namespace, ok := view.Namespace(procedure.Namespace())
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing installed function namespace")
	}
	oid, err := resolver.ResolveRegdictionary(h.Operation().Context(), catalog.QuoteIdentifier(namespace.Name())+".unaccent")
	if err != nil {
		return nil, err
	}
	dictionary, ok := view.TSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing resolved unaccent dictionary")
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, dictionary); err != nil {
		return nil, err
	}
	return dictionary.Runtime, nil
}
