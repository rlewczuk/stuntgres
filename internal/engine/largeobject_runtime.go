package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func (h *statementHost) LargeObject(r functions.LargeObjectRequest) (functions.LargeObjectResult, error) {
	s := h.scope()
	if err := s.active(); err != nil {
		return functions.LargeObjectResult{}, err
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	out := functions.LargeObjectResult{}
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		return out, ids.ErrCatalogReplaced
	}
	if r.Name == "lo_get" && r.Fragment && r.Length < 0 {
		return out, sqlError("22023", "requested length cannot be negative")
	}
	if r.Name != "lo_get" && s.owner.ReadOnly {
		return out, sqlError("25006", "cannot execute "+r.Name+"() in a read-only transaction")
	}
	// Freeze this query's read view before its first command candidate. SQL
	// body activation supplies its own fresh view and restores the outer one.
	if s.largeObjectRead.State() == nil {
		s.largeObjectRead = s.routineAuth.Catalog
		if s.largeObjectRead.State() == nil {
			s.largeObjectRead = e.privateCatalogLocked(s.owner.Catalog)
		}
	}
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return out, err
	}
	if r.Name == "lo_get" {
		auth.Catalog = s.largeObjectRead
		auth.Recapture(auth.Catalog)
		d, ok := auth.Catalog.Get(ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: r.OID}).(*catalog.LargeObject)
		if !ok {
			return out, largeObjectMissingError(r.OID)
		}
		if err := auth.Check(access.Requirement{Object: auth.Ref(d.Address()), Privileges: authorization.SelectPrivilege}); err != nil {
			return out, err
		}
		data, err := d.Content.Read(r.Offset, r.Length)
		if err != nil {
			return out, err
		}
		out.Data = data
		return out, nil
	}
	changes := s.catalog
	if changes == nil {
		if r.Name == "lo_put" && len(r.Data) == 0 {
			changes = s.owner.Catalog
		} else {
			changes = s.owner.Catalog.Clone()
		}
	}
	view := e.privateCatalogLocked(changes)
	auth.Catalog = view
	auth.Recapture(view)
	a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: r.OID}
	var d *catalog.LargeObject
	if r.Name == "lo_create" || r.Name == "lo_creat" || r.Name == "lo_from_bytea" {
		if a.ObjectID == 0 {
			for {
				oid, err := e.catalog.allocateOIDLocked()
				if err != nil {
					return out, err
				}
				a.ObjectID = oid
				if view.Get(a) == nil {
					break
				}
			}
		}
		if view.Get(a) != nil {
			return out, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_largeobject_metadata_oid_index"`, Detail: fmt.Sprintf("Key (oid)=(%d) already exists.", a.ObjectID), Schema: "pg_catalog", Table: "pg_largeobject_metadata", Constraint: "pg_largeobject_metadata_oid_index"}
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return out, err
		}
		d = &catalog.LargeObject{ObjectAddress: a, ObjectRevision: revision, ObjectName: strconv.FormatUint(uint64(a.ObjectID), 10), Owner: auth.State.CurrentUser}
		d.ACL = e.creationACLChangesLocked(changes, view, d.Owner, ids.ObjectAddress{}, authorization.DefaultLargeObjects)
		e.catalogChangeLocked(changes, a, d)
		if r.Name == "lo_from_bytea" {
			view = e.privateCatalogLocked(changes)
			auth.Catalog = view
			auth.Recapture(view)
		}
	} else {
		d, _ = view.Get(a).(*catalog.LargeObject)
		if d == nil {
			return out, largeObjectMissingError(r.OID)
		}
	}
	if r.Name == "lo_put" || r.Name == "lo_from_bytea" {
		if !operationReplayOrZero(s.op).largeObjectAuthority {
			if err := auth.Check(access.Requirement{Object: auth.Ref(a), Privileges: authorization.SelectPrivilege | authorization.UpdatePrivilege}); err != nil {
				return out, err
			}
		}
		content, err := d.Content.Write(r.Offset, r.Data)
		if err != nil {
			return out, err
		}
		if len(r.Data) == 0 {
			if r.Name == "lo_put" {
				out.OID = a.ObjectID
				return out, nil
			}
		} else {
			n := *d
			n.Content = content
			n.ObjectRevision, err = e.catalog.revisionLocked()
			if err != nil {
				return out, err
			}
			d = &n
			e.catalogChangeLocked(changes, a, d)
		}
	}
	if r.Name == "lo_unlink" {
		if err := auth.Check(access.Requirement{Object: auth.Ref(a), OwnerOnly: true}); err != nil {
			return out, err
		}
		e.catalogChangeLocked(changes, a, nil)
	}
	s.catalog = changes
	out.OID = a.ObjectID
	return out, nil
}

func largeObjectMissingError(oid ids.OID) error {
	return sqlError("42704", fmt.Sprintf("large object %d does not exist", oid))
}
