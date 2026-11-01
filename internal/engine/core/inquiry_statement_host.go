package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// The inquiry hosts. Operation, the Engine.Mu pair, InquiryCatalog, the
// catalog epoch and journals, TransactionRef, CatalogDomain, LockManager and
// Ticket are shared with the other leaves and exec.Host. The statement's
// inquiry snapshot, its captures, lazy routine admission, authorization and
// the session, notification and transaction state stay core's.
var (
	_ CatalogHost = (*statementHost)(nil)
	_ SessionInquiryHost = (*statementHost)(nil)
)

func (h *statementHost) CommentSnapshot() (catalog.Read, authorization.Read) {
	return h.Inquiry.CommentCatalog, h.Inquiry.commentRoles
}

func (h *statementHost) RoleDomain() *authorization.Domain {
	return &h.scope().Engine().RoleStore.Domain
}

func (h *statementHost) VisibilitySnapshot() (catalog.Read, []ids.ObjectAddress, []ids.ObjectAddress) {
	in := &h.Inquiry
	return in.visibilityCatalog, in.visibilityRelationPath, in.visibilityOtherPath
}

func (h *statementHost) DeparsePath() []ids.ObjectAddress    { return h.Inquiry.DeparsePath }
func (h *statementHost) DeparseInputs() table.DeparseInputs  { return h.scope().deparseInputs() }
func (h *statementHost) DeparseView() access.View            { return h.Inquiry.deparseAuth.View }
func (h *statementHost) LookupAddress() regobj.AddressLookup { return addressLookup(h.scope()) }
func (h *statementHost) AdmitInquiryBody(c *exec.Context, provenance expr.Info) error {
	return admitInquiryBody(c, provenance)
}

func (h *statementHost) RelationAllocationLocked(view catalog.Read, relation *catalog.Relation) storage.Allocation {
	s := h.scope()
	return captureAllocationLocked(s.Engine(), s, view, relation)
}

func (h *statementHost) AggregateAllocationsLocked(ctx context.Context, kind functions.SizeKind, arg sqltype.Value, view catalog.Read) ([]storage.Allocation, bool, error) {
	return (*catalogInquiryHost)(h).captureAggregateSizeLocked(ctx, kind, arg, view)
}

func (h *statementHost) ImportSystemCollationsLocked(oid uint32) error {
	s := h.scope()
	auth, err := s.namespaceAuthorizationLocked(s.Engine())
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return SQLError("42501", "must be superuser to import system collations")
	}
	if _, exists := auth.Catalog.Descriptor(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(oid)}); !exists {
		return SQLError("3F000", fmt.Sprintf("schema with OID %d does not exist", oid))
	}
	return SQLError("0A000", "system collation import is unsupported in embedded mode")
}

func (h *statementHost) ExpressionIdentity(c *exec.Context) (exec.IdentitySnapshot, error) {
	s := h.scope()
	identity := s.Identity
	if s.Op.EffectiveUser == nil && c.Query != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Expression != nil {
		identity = c.Query.Source.Authority.Expression.Identity
	}
	if s.Op.Restricted == nil && s.ForeignActionAuth != nil {
		auth := s.ForeignActionAuth
		role, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return exec.IdentitySnapshot{}, err
		}
		name, _ := auth.Catalog.LookupType(sqltype.Name)
		identity.Current, identity.CurrentMissing = sqltype.String(name, role.Name), authorization.PrincipalRef{}
	}
	return identity, nil
}

func (h *statementHost) InquiryCacheable() bool {
	return h.ForeignActionAuth == nil && h.ruleAction.Auth == nil
}

func (h *statementHost) CurrentSchemaResult() *sqltype.Value  { return &h.Inquiry.CurrentSchemaResult }
func (h *statementHost) SearchPathResults() *[2]sqltype.Value { return &h.Inquiry.searchPathResults }

func (h *statementHost) CurrentSchemaPathLocked() (catalog.Read, []ids.ObjectAddress, error) {
	s := h.scope()
	auth, path, err := s.schemaInquiryPathLocked(s.Engine())
	if err != nil {
		return catalog.Read{}, nil, err
	}
	return auth.Catalog, path, nil
}

func (h *statementHost) SearchPathsLocked() (catalog.Read, [2][]ids.ObjectAddress, error) {
	s := h.scope()
	auth, explicit, err := s.schemaInquiryPathLocked(s.Engine())
	if err != nil {
		return catalog.Read{}, [2][]ids.ObjectAddress{}, err
	}
	effective := EffectivePath(auth, explicit, analyze.RelationLookup)
	return auth.Catalog, [2][]ids.ObjectAddress{explicit, effective}, nil
}

func (h *statementHost) TemporaryNamespaceLocked() (ids.ObjectAddress, error) {
	s := h.scope()
	auth, err := s.namespaceAuthorizationLocked(s.Engine())
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	return temporaryNamespace(auth), nil
}

func (h *statementHost) NamespaceSessionLocked() (catalog.Read, *session.Session, error) {
	s := h.scope()
	auth, err := s.namespaceAuthorizationLocked(s.Engine())
	if err != nil {
		return catalog.Read{}, nil, err
	}
	return auth.Catalog, auth.Session, nil
}

func (h *statementHost) ListeningChannelsLocked() (map[string]struct{}, error) {
	s := h.scope()
	if err := session.ValidateOperation(DomainOf(s.Engine()), s.Op); err != nil {
		return nil, err
	}
	if state := PayloadOf(s.Op.Session).notifications.State; state != nil {
		return state.Channels, nil
	}
	return nil, nil
}

func (h *statementHost) NotificationQueueUsageLocked() (float64, error) {
	s := h.scope()
	e := s.Engine()
	if err := session.ValidateOperation(DomainOf(e), s.Op); err != nil {
		return 0, err
	}
	return e.notificationQueueUsageLocked(), nil
}

func (h *statementHost) VisibilityLocked() *visibility.Registry {
	return &h.scope().Engine().Visibility
}

func (h *statementHost) TransactionAgeLocked(xid uint32) (int32, error) {
	return h.scope().Engine().transactionAgeLocked(h.TxRef, xid)
}

func (h *statementHost) AssignTransactionXIDLocked() error {
	return h.scope().Engine().AssignTransactionXIDLocked(h.TxRef)
}

func (h *statementHost) CurrentSnapshotLocked() (*sqltype.Snapshot, error) {
	s := h.scope()
	e := s.Engine()
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, s.Op); err != nil {
		return nil, err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	return s.currentSnapshot, nil
}
