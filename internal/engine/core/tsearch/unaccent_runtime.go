package tsearch

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Unaccent invokes the exact selected dictionary, or resolves the default in
// the installed function's current schema. It returns the first recognized
// lexeme, or the original token when the callback returns none.
func Unaccent[A Authority[A]](h Host[A], procedure ids.ObjectRef, dictionary sqltype.Value, input string, result *sqltype.Descriptor) (out sqltype.Value, err error) {
	mark := h.MarkDictionaryInitialization()
	defer func() {
		if err != nil {
			h.AbortDictionaryInitialization(mark)
		}
	}()
	ctx := h.Operation().Context()
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var runtime dictionaryBinding
	capture := captureDictionaryInvocation(h)
	if dictionary.Type() == nil {
		runtime, err = defaultUnaccentDictionary(h, procedure, capture)
	} else {
		oid, inputErr := dictionary.RegTSOID()
		if inputErr != nil {
			return sqltype.Value{}, inputErr
		}
		runtime, err = capture.dictionary(h, ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	lexemes, err := lexizeDictionary(h, runtime, ctx, input)
	if err != nil {
		return sqltype.Value{}, err
	}
	if lexemes.Recognized && len(lexemes.Lexemes) != 0 {
		input = lexemes.Lexemes[0].Text
	}
	if err = capture.accept(ctx, h); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, input), ctx.Err()
}

// Capture immutable runtime authority under the ordinary current statement view;
// normalization/resource execution happens only after releasing Engine.mu.
func defaultUnaccentDictionary[A Authority[A]](h Host[A], reference ids.ObjectRef, capture *tsAcquisition[A]) (dictionaryBinding, error) {
	h.ReacquireEngine()
	locked := true
	defer func() {
		if locked {
			h.ReleaseEngineLocked()
		}
	}()
	view, resolver, err := h.TextSearchInputLocked()
	if err != nil {
		return dictionaryBinding{}, err
	}
	if reference.Domain() != view.Domain() || reference.Epoch != view.Epoch() {
		return dictionaryBinding{}, ids.ErrCatalogReplaced
	}
	procedure, ok := view.Procedure(reference.Address)
	if !ok || extension.UnaccentProviderOf(procedure) != extension.StockUnaccent {
		return dictionaryBinding{}, fmt.Errorf("stuntgres: missing installed unaccent routine")
	}
	if err := (stored.ProcedureProvider{}).Validate(view, procedure); err != nil {
		return dictionaryBinding{}, err
	}
	namespace, ok := view.Namespace(procedure.Namespace())
	if !ok {
		return dictionaryBinding{}, fmt.Errorf("stuntgres: missing installed function namespace")
	}
	oid, err := resolver.ResolveRegdictionary(h.Operation().Context(), catalog.QuoteIdentifier(namespace.Name())+".unaccent")
	if err != nil {
		return dictionaryBinding{}, err
	}
	dictionary, ok := view.TSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	if !ok {
		return dictionaryBinding{}, fmt.Errorf("stuntgres: missing resolved unaccent dictionary")
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, dictionary); err != nil {
		return dictionaryBinding{}, err
	}
	h.ReleaseEngineLocked()
	locked = false
	binding, err := acquireDictionaryFor(h, dictionary.Address(), capture, dictionary)
	if err == nil {
		if capture.runtimes == nil {
			capture.runtimes = make(map[ids.ObjectAddress]dictionaryBinding)
		}
		capture.runtimes[dictionary.Address()] = binding
	}
	return binding, err
}
