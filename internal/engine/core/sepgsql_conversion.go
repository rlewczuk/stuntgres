package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) EnterEncodingConversionCallback(callback catalog.EncodingConversionCallback) (sqltype.RoutineBodyToken, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	e := s.Engine()
	if e.Sepgsql == nil {
		return sqltype.RoutineBodyToken{}, nil
	}
	mark := s.MarkSepgsqlObservations()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	var helper *catalog.Procedure
	if err == nil {
		if callback.Helper == nil || callback.Conversion == nil || callback.Ref.Domain() != auth.Catalog.Domain() || callback.Ref.Epoch != auth.Catalog.Epoch() || callback.Ref.Address != callback.Helper.Address() {
			err = ids.ErrCatalogReplaced
		} else {
			current, ok := auth.Catalog.Conversion(callback.Conversion.Address())
			if !ok || current != callback.Conversion || current.Handler != callback.Helper.Address() || current.Source != callback.Conversion.Source || current.Destination != callback.Conversion.Destination {
				err = ids.ErrCatalogReplaced
			} else {
				helper, ok = auth.Catalog.Procedure(current.Handler)
				if !ok || helper != callback.Helper || helper.Conversion != callback.Helper.Conversion || helper.Signature != catalog.ConversionSignature || helper.Result != 23 {
					err = fmt.Errorf("stuntgres: stale encoding conversion callback")
				}
			}
		}
	}
	target, transition := "", false
	if err == nil {
		target, transition, err = s.sepgsqlBodyDecision(auth, helper, false, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		s.RestoreSepgsqlObservations(mark)
		return sqltype.RoutineBodyToken{}, err
	}
	if !transition {
		return sqltype.RoutineBodyToken{}, nil
	}
	return s.Op.Session.Sepgsql.Enter(target), nil
}

func selectedConversionHandler(view catalog.Read, selected conversionDefault) (sqltype.EncodingConversionHandler, error) {
	if selected.procedure == nil {
		return sqltype.EncodingConversionHandler{}, nil
	}
	handler := selected.procedure.Conversion
	if view.Owner() == nil || view.Owner().Sepgsql == nil {
		return handler, nil
	}
	callback, err := catalog.CaptureEncodingConversion(view, selected.conversion, selected.procedure)
	if err != nil {
		return sqltype.EncodingConversionHandler{}, err
	}
	return handler.WithReference(callback), nil
}
