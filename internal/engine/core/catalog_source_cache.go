package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// catalogCaptureKey identifies everything the shared producers read. Published
// catalog states and role records are never written again, so equal pointers
// mean equal content; any DDL, role or ACL publication changes the key.
type catalogCaptureKey struct {
	state  *catalog.State
	roles  *access.RoleRecord
	source *catalog.Relation
	ref    ids.ObjectRef
}

type catalogCaptureSlot struct {
	key  catalogCaptureKey
	rows [][]sqltype.Value // Shared read-only by every statement that hits the key.
	keys *exec.CatalogKeyIndex
}

// dependOID is pg_depend's relation OID.
const dependOID ids.OID = 2608

// catalogCaptureCache keeps the latest pg_type, pg_proc and pg_depend captures.
// These producers rebuild every row of a large catalog, and catalog probes
// repeat them between changes. Engine.Mu owns every slot.
type catalogCaptureCache struct {
	types, procedures, depends catalogCaptureSlot
}

func (c *catalogCaptureCache) slot(oid ids.OID) *catalogCaptureSlot {
	switch oid {
	case pgcatalog.TypeOID:
		return &c.types
	case ids.ProcedureClassID:
		return &c.procedures
	case dependOID:
		return &c.depends
	}
	return nil
}

// captureCatalogSourceRowsLocked reuses the slot's rows when the input is the
// published catalog and role record it was built from. Transaction-private
// catalogs or roles always capture fresh. Errors are never stored, and a hit
// still reports the caller's cancellation. Both return the slot's key index,
// so oid equality filters visit only matching rows. A hit serves a capture
// with Keep too: the shared rows include every row Keep would keep, and the
// filter still rejects the rest. A miss with Keep builds only the kept rows
// and is not stored. Caller holds Engine.Mu.
func (e *Engine) captureCatalogSourceRowsLocked(in pgcatalog.Input, produce pgcatalog.Producer, roles *access.RoleRecord) (*exec.Source, error) {
	slot := e.catalogCaptures.slot(in.Source.Address().ObjectID)
	state := in.Catalog.State()
	if slot == nil || state == nil || state != e.Catalog.State || roles == nil || roles != e.RoleStore.Published {
		return CaptureCatalogSourceRows(in, produce)
	}
	key := catalogCaptureKey{state: state, roles: roles, source: in.Source, ref: in.Ref}
	if slot.rows != nil && slot.key == key {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		return &exec.Source{Ref: in.Ref, Anonymous: slot.rows, CatalogKeys: slot.keys}, nil
	}
	if in.Keep.Test != nil {
		return CaptureCatalogSourceRows(in, produce)
	}
	source, err := CaptureCatalogSourceRows(in, produce)
	if err == nil {
		slot.key, slot.rows, slot.keys = key, source.Anonymous, exec.NewCatalogKeyIndex(source.Anonymous)
		source.CatalogKeys = slot.keys
	}
	return source, err
}
