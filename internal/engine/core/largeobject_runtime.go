package core

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
	if err := s.Active(); err != nil {
		return functions.LargeObjectResult{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	out := functions.LargeObjectResult{}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return out, ids.ErrCatalogReplaced
	}
	if r.Name == "lo_get" && r.Fragment && r.Length < 0 {
		return out, SQLError("22023", "requested length cannot be negative")
	}
	if r.Name != "lo_get" && s.Owner.ReadOnly {
		return out, SQLError("25006", "cannot execute "+r.Name+"() in a read-only transaction")
	}
	// Freeze this query's read view before its first command candidate. SQL
	// body activation supplies its own fresh view and restores the outer one.
	if s.largeObjectRead.State() == nil {
		s.largeObjectRead = s.RoutineAuth.Catalog
		if s.largeObjectRead.State() == nil {
			s.largeObjectRead = e.PrivateCatalogLocked(s.PendingCatalog())
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
			return out, LargeObjectMissingError(r.OID)
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
	changes := s.Catalog
	if changes == nil {
		if r.Name == "lo_put" && len(r.Data) == 0 {
			changes = s.PendingCatalog()
		} else {
			changes = s.PendingCatalog().Clone()
		}
	}
	view := e.PrivateCatalogLocked(changes)
	auth.Catalog = view
	auth.Recapture(view)
	a := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: r.OID}
	var d *catalog.LargeObject
	if r.Name == "lo_create" || r.Name == "lo_creat" || r.Name == "lo_from_bytea" {
		if a.ObjectID == 0 {
			for {
				oid, err := e.Catalog.AllocateOIDLocked()
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
		revision, err := e.Catalog.RevisionLocked()
		if err != nil {
			return out, err
		}
		d = &catalog.LargeObject{ObjectAddress: a, ObjectRevision: revision, ObjectName: strconv.FormatUint(uint64(a.ObjectID), 10), Owner: auth.State.CurrentUser}
		d.ACL = e.CreationACLChangesLocked(changes, view, d.Owner, ids.ObjectAddress{}, authorization.DefaultLargeObjects)
		e.CatalogChangeLocked(changes, a, d)
		if r.Name == "lo_from_bytea" {
			view = e.PrivateCatalogLocked(changes)
			auth.Catalog = view
			auth.Recapture(view)
		}
	} else {
		d, _ = view.Get(a).(*catalog.LargeObject)
		if d == nil {
			return out, LargeObjectMissingError(r.OID)
		}
	}
	if r.Name == "lo_put" || r.Name == "lo_from_bytea" {
		if !OperationReplayOrZero(s.Op).LargeObjectAuthority {
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
			n.ObjectRevision, err = e.Catalog.RevisionLocked()
			if err != nil {
				return out, err
			}
			d = &n
			e.CatalogChangeLocked(changes, a, d)
		}
	}
	if r.Name == "lo_unlink" {
		if err := auth.Check(access.Requirement{Object: auth.Ref(a), OwnerOnly: true}); err != nil {
			return out, err
		}
		e.CatalogChangeLocked(changes, a, nil)
	}
	s.Catalog = changes
	out.OID = a.ObjectID
	return out, nil
}

func LargeObjectMissingError(oid ids.OID) error {
	return SQLError("42704", fmt.Sprintf("large object %d does not exist", oid))
}
