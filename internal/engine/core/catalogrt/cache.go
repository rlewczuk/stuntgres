package catalogrt

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

// CaptureCache keeps the latest pg_type, pg_proc and pg_depend captures.
// These producers rebuild every row of a large catalog, and catalog probes
// repeat them between changes. Engine.Mu owns every slot.
type CaptureCache struct {
	types, procedures, depends catalogCaptureSlot
}

func (c *CaptureCache) slot(oid ids.OID) *catalogCaptureSlot {
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

// CaptureLocked reuses the slot's rows when the input is the published
// catalog and role record (published, publishedRoles) it was built from.
// roles is the record in.Roles derives from alone, or nil. Transaction-private
// catalogs or roles always capture fresh. Errors are never stored, and a hit
// still reports the caller's cancellation. Both return the slot's key index,
// so oid equality filters visit only matching rows. A hit serves a capture
// with Keep too: the shared rows include every row Keep would keep, and the
// filter still rejects the rest. A miss with Keep builds only the kept rows
// and is not stored. Caller holds Engine.Mu.
func (c *CaptureCache) CaptureLocked(in pgcatalog.Input, produce pgcatalog.Producer, roles *access.RoleRecord, published *catalog.State, publishedRoles *access.RoleRecord) (*exec.Source, error) {
	slot := c.slot(in.Source.Address().ObjectID)
	state := in.Catalog.State()
	if slot == nil || state == nil || state != published || roles == nil || roles != publishedRoles {
		return CaptureRows(in, produce)
	}
	key := catalogCaptureKey{state: state, roles: roles, source: in.Source, ref: in.Ref}
	if slot.rows != nil && slot.key == key {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		return &exec.Source{Ref: in.Ref, Anonymous: slot.rows, CatalogKeys: slot.keys}, nil
	}
	if in.Keep.Test != nil {
		return CaptureRows(in, produce)
	}
	source, err := CaptureRows(in, produce)
	if err == nil {
		slot.key, slot.rows, slot.keys = key, source.Anonymous, exec.NewCatalogKeyIndex(source.Anonymous)
		source.CatalogKeys = slot.keys
	}
	return source, err
}

// CaptureRows is the sole constructor of a catalog exec.Source, including
// error paths.
func CaptureRows(in pgcatalog.Input, produce pgcatalog.Producer) (*exec.Source, error) {
	rows, err := produce(in)
	if err != nil {
		return nil, err
	}
	// Anonymous nil denotes a physical source requiring a visibility view.
	// A catalog producer's empty result is still an owned anonymous rowset.
	if rows == nil {
		rows = [][]sqltype.Value{}
	}
	return &exec.Source{Ref: in.Ref, Anonymous: rows}, nil
}
