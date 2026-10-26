package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestLtxtqueryPreparedAuthorityRebind(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION ltree")
	done()
	ext := e.CatalogRead().MustExtension(e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	// The shared authority matrix rejects executable reuse after owner/ACL
	// changes, permits genuine nominal rebind, and retains environment,
	// DISCARD, family, extension, companion and executable-definition fences.
	testInstalledScalarPreparedAuthorityRebind(t, e, ext, "ltxtquery", 6)
}
