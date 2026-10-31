package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func ValidateConnectbyReturn(r functions.ConnectbyRequest) error { return validateConnectbyReturn(r) }
func ValidateConnectbySource(result *sqltype.Descriptor, schema []catalog.Column) error {
	return validateConnectbySource(result, schema)
}

func CaptureConnectbySourceRoots(s *StatementScope, e *Engine, refs []ids.ObjectRef) (*visibility.View, error) {
	return s.captureDataRootsAtLocked(e, refs, false, nil, false)
}
