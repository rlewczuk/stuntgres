package core

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func originNameError(name string) error {
	if strings.EqualFold(name, "any") || strings.EqualFold(name, "none") || strings.HasPrefix(name, "pg_") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42939", Message: fmt.Sprintf(`replication origin name "%s" is reserved`, name), Detail: `Origin names "any", "none", and names starting with "pg_" are reserved.`}
	}
	if len(name) > 512 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "replication origin name is too long", Detail: "Replication origin names must be no longer than 512 bytes."}
	}
	return nil
}
func originDuplicate(name string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_replication_origin_roname_index"`, Detail: fmt.Sprintf("Key (roname)=(%s) already exists.", name), Schema: "pg_catalog", Table: "pg_replication_origin", Constraint: "pg_replication_origin_roname_index"}
}
func (h DDLHost) ClearReplicationOrigin(id ids.OID) {
	read := h.StageRead()
	if d, ok := read.Get(ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: id}).(*catalog.ReplicationOrigin); ok {
		h.E.bumpOriginProgressLocked(read, d)
	}
	delete(h.E.Cluster.replicationOriginProgress, id)
}

// ReplicationOrigin resolves every demand through the current private catalog.
// The cluster mutex owns progress, which deliberately has no journal backing.
func (h *statementHost) ReplicationOrigin(kind functions.ReplicationOriginKind, values [2]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return sqltype.Value{}, err
	}
	name, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	if kind == functions.ReplicationOriginCreate {
		if err := originNameError(name); err != nil {
			return sqltype.Value{}, err
		}
	}
	capacity := 0
	if kind == functions.ReplicationOriginAdvance || kind == functions.ReplicationOriginProgress {
		text, _ := s.Op.Setting("max_active_replication_origins")
		capacity, err = strconv.Atoi(text)
		if err != nil {
			return sqltype.Value{}, err
		}
		if capacity == 0 {
			return sqltype.Value{}, SQLError("55000", `cannot query or manipulate replication origin when "max_active_replication_origins" is 0`)
		}
	}
	e := s.Engine()
	key := catalog.ReplicationOriginNameKey(name)
	if kind == functions.ReplicationOriginCreate || kind == functions.ReplicationOriginDrop {
		resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(s.TxRef.Epoch(), ids.ObjectAddress{}), Name: key}
		if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return sqltype.Value{}, err
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.Op.Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	auth, err := s.callableAuthorizationLocked(e)
	if err != nil {
		return sqltype.Value{}, err
	}
	view := auth.Catalog
	d, _ := view.Get(view.NameAddress(key)).(*catalog.ReplicationOrigin)
	if kind == functions.ReplicationOriginOID {
		if d == nil {
			return sqltype.Null(result), nil
		}
		return sqltype.OIDValue(result, uint32(d.Address().ObjectID))
	}
	if kind == functions.ReplicationOriginCreate {
		if d != nil {
			return sqltype.Value{}, originDuplicate(name)
		}
		occupancy := e.replicationOriginOccupancyLocked(false)
		for _, origin := range view.ReplicationOrigins() {
			occupancy.add(origin.Address().ObjectID)
		}
		id, err := occupancy.allocate(s.Op.Context())
		if err != nil {
			return sqltype.Value{}, err
		}
		revision, err := e.Catalog.RevisionLocked()
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := e.AssignTransactionXIDLocked(s.TxRef); err != nil {
			return sqltype.Value{}, err
		}
		d = &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: id}, ObjectRevision: revision, ObjectName: name}
		changes := s.Catalog
		if changes == nil {
			changes = s.PendingCatalog().Clone()
		}
		e.CatalogChangeLocked(changes, d.Address(), d)
		if e.Cluster.replicationOrigins == nil {
			e.Cluster.replicationOrigins = make(map[ids.OID]replicationOriginReservation)
		}
		e.Cluster.replicationOrigins[id] = replicationOriginReservation{Ref: s.TxRef, Address: d.Address()}
		s.Catalog = changes
		return sqltype.OIDValue(result, uint32(id))
	}
	if d == nil {
		return sqltype.Value{}, SQLError("42704", fmt.Sprintf(`replication origin "%s" does not exist`, name))
	}
	id := d.Address().ObjectID
	switch kind {
	case functions.ReplicationOriginDrop:
		changes := s.Catalog
		if changes == nil {
			changes = s.PendingCatalog().Clone()
		}
		if err := e.AssignTransactionXIDLocked(s.TxRef); err != nil {
			return sqltype.Value{}, err
		}
		delete(e.Cluster.replicationOriginProgress, id)
		e.bumpOriginProgressLocked(view, d)
		e.CatalogChangeLocked(changes, d.Address(), nil)
		s.Catalog = changes
		return sqltype.VoidValue(result), nil
	case functions.ReplicationOriginAdvance:
		position, err := values[1].PGLSN()
		if err != nil {
			return sqltype.Value{}, err
		}
		if _, active := e.Cluster.replicationOriginProgress[id]; !active && len(e.Cluster.replicationOriginProgress) >= capacity {
			return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "53400", Message: fmt.Sprintf("could not find free replication state slot for replication origin with ID %d", id), Hint: `Increase "max_active_replication_origins" and try again.`}
		}
		if e.Cluster.replicationOriginProgress == nil {
			e.Cluster.replicationOriginProgress = make(map[ids.OID]uint64)
		}
		e.Cluster.replicationOriginProgress[id] = position
		e.bumpOriginProgressLocked(view, d)
		return sqltype.VoidValue(result), nil
	case functions.ReplicationOriginProgress:
		flush, err := values[1].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
		if flush {
			return sqltype.Value{}, SQLError("0A000", "pg_replication_origin_progress with flush=true requires durable WAL flushing, which is unsupported in embedded mode")
		}
		position := e.Cluster.replicationOriginProgress[id]
		if position == 0 {
			return sqltype.Null(result), nil
		}
		return sqltype.PGLSNValue(result, position)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid replication origin operation")
}
