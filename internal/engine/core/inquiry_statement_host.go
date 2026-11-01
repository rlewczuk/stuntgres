package core

import (
	"context"
	"fmt"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/core/inquiry"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The inquiry hosts. Operation, the Engine.Mu pair, InquiryCatalog, the
// catalog epoch and journals, TransactionRef, CatalogDomain, LockManager and
// Ticket are shared with the other leaves and exec.Host. The statement's
// inquiry snapshot, its captures, lazy routine admission, authorization and
// the session, notification and transaction state stay core's.
var (
	_ inquiry.CatalogHost = (*statementHost)(nil)
	_ inquiry.SessionHost = (*statementHost)(nil)
)

// Read only the retained statement root. Comparing its immutable owner does not
// consult the live catalog frontier, which may already have been replaced.
func (h *statementHost) catalogInquiryCatalog() (catalog.Read, error) {
	scope := h.scope()
	view := scope.Inquiry.CommentCatalog
	if view.State() == nil {
		return catalog.Read{}, fmt.Errorf("stuntgres: missing catalog inquiry snapshot")
	}
	engine := scope.Engine()
	if view.Owner() != &engine.Catalog.Owner || !scope.Inquiry.commentRoles.BelongsTo(&engine.RoleStore.Domain) {
		return catalog.Read{}, fmt.Errorf("stuntgres: foreign catalog inquiry snapshot")
	}
	return view, nil
}

// addressLookup is the statement's object address snapshot. Namespace checks
// use the deparse authority as captured; notices go to the statement's
// operation.
func addressLookup(scope *StatementScope) regobj.AddressLookup {
	auth := scope.Inquiry.deparseAuth
	return regobj.AddressLookup{
		Catalog: scope.Inquiry.CommentCatalog,
		Roles:   scope.Inquiry.commentRoles,
		Capture: regobj.InputCapture{
			Authority: inputAuthority(auth), Path: scope.Inquiry.DeparsePath,
			Database: scope.Op.Session.DatabaseName, Token: scope.Op.Session.SessionToken, Temporary: temporaryNamespace(auth),
		},
		Namespace: func(name parser.ObjectName) (analyze.NamespaceSelection, error) {
			return LookupNamespacesSnapshot(auth, name, analyze.FunctionLookup)
		},
		Notice: scope.Op.EmitNotice,
	}
}

// deparseInputs reads the statement's inquiry snapshot for table.Deparse.
func (s *StatementScope) deparseInputs() table.DeparseInputs {
	auth := s.Inquiry.deparseAuth
	in := table.DeparseInputs{
		Catalog:        s.Inquiry.CommentCatalog,
		Path:           s.Inquiry.DeparsePath,
		Output:         s.Output,
		TemporaryOwner: CatalogSourceTemporaryOwner(auth),
		Temporary:      func() ids.ObjectAddress { return temporaryNamespace(auth) },
		ResolveRelation: func(name parser.ObjectName) (ids.ObjectRef, error) {
			return resolveRelationSnapshot(auth, name, true)
		},
	}
	// pg_get_serial_sequence never reads the string setting; a scope without
	// an operation still answers it.
	if s.Op != nil {
		in.NonstandardStrings = s.Op.NonstandardStrings
	}
	if auth.Session != nil {
		in.Database = auth.Session.DatabaseName
	}
	runtime := catalogrt.Runtime{View: auth.View, Presentation: stored.Presentation{Deparse: true, Path: in.Path, Output: in.Output, NonstandardStrings: in.NonstandardStrings}}
	in.BuiltinViewDefinition = func(ctx context.Context, read catalog.Read, relation *catalog.Relation, pretty bool) (string, error) {
		if relation.RelationKind != catalog.CatalogRelation || pgcatalog.SummaryViewDefinition(relation.Address().ObjectID) == "" {
			return "", nil
		}
		presentation := runtime
		presentation.Presentation.Pretty = pretty
		return presentation.BuiltinViewDefinition(ctx, read, relation)
	}
	return in
}

// Engine.mu is held. The execution frontier owns catalog and path, never mutable
// session settings or an authorization view subsequently consulted by Eval.
func (e *Engine) captureVisibilitySnapshotLocked(scope *StatementScope) error {
	if scope.Inquiry.visibilityCatalog.State() != nil {
		return nil
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	explicit, _, err := ExplicitPath(auth)
	if err != nil {
		return err
	}
	// effectivePath filters TEMP in place for conversion lookup. Give that
	// derived path its own backing store; the relation path remains immutable.
	relations := EffectivePath(auth, explicit, analyze.RelationLookup)
	otherExplicit := make([]ids.ObjectAddress, len(explicit))
	copy(otherExplicit, explicit)
	others := EffectivePath(auth, otherExplicit, analyze.ConversionLookup)
	scope.Inquiry.visibilityCatalog = auth.Catalog
	scope.Inquiry.visibilityRelationPath, scope.Inquiry.visibilityOtherPath = relations, others
	return nil
}

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

func captureAllocationLocked(e *Engine, scope *StatementScope, view catalog.Read, relation *catalog.Relation) storage.Allocation {
	a := storage.Allocation{Relation: relation}
	var pending *session.Journal
	if scope != nil {
		pending = scope.PendingCatalog()
	}
	if relation.SQLIndex != nil {
		a.Index = index.EffectiveRoot(e.Storage.IndexRoots, pending, relation.Address())
		a.Allocated = a.Index != nil
		a.Parent, _ = view.Relation(relation.SQLIndex.Parent)
		if stored := e.PrivateStorageLocked(pending, relation.SQLIndex.Parent); stored != nil {
			a.ParentVersions = stored.Versions
		}
		if scope != nil {
			if scope.ProjectedRoots {
				a.ParentOwn = scope.Stage.Rows[relation.SQLIndex.Parent]
			} else {
				a.ParentOwn = scope.OwnWrites[relation.SQLIndex.Parent]
				a.ParentStaged = scope.Stage.Rows[relation.SQLIndex.Parent]
				a.ReplaceOwn = scope.Stage.Replacing
			}
		}
		return a
	}
	if relation.Sequence != nil {
		if scope != nil {
			a.Allocated = e.SequenceGeneratorLocked(scope, relation.Address()) != nil
		} else {
			a.Allocated = e.Storage.Sequences[relation.Address()] != nil
		}
		return a
	}
	if stored := e.PrivateStorageLocked(pending, relation.Address()); stored != nil {
		a.Allocated, a.Versions = true, stored.Versions
		if scope != nil {
			if scope.ProjectedRoots {
				a.Own = scope.Stage.Rows[relation.Address()]
			} else {
				a.Own = scope.OwnWrites[relation.Address()]
				a.Staged = scope.Stage.Rows[relation.Address()]
				a.ReplaceOwn = scope.Stage.Replacing
			}
		}
	}
	return a
}

// AggregateAllocationsLocked admits a database or tablespace size inquiry: it
// resolves existence before CONNECT/CREATE or inherited statistics privileges
// under the invoker's routine authorization, then selects the registered
// target runtimes rather than treating the attached store as the cluster.
func (h *statementHost) AggregateAllocationsLocked(ctx context.Context, kind functions.SizeKind, arg sqltype.Value, view catalog.Read) ([]storage.Allocation, bool, error) {
	s, e := h.scope(), h.scope().Engine()
	class, label, privilege := ids.DatabaseClassID, "database", authorization.ConnectPrivilege
	if kind == functions.TablespaceSize {
		class, label, privilege = ids.TablespaceClassID, "tablespace", authorization.CreatePrivilege
	}
	var descriptor catalog.Descriptor
	if arg.Type().ID() == sqltype.Name {
		name, err := arg.Text()
		if err != nil {
			return nil, false, err
		}
		address := view.NameAddress(ids.NameKey{ClassID: class, Name: name})
		descriptor = view.Get(address)
		if descriptor == nil {
			code := "42704"
			if kind == functions.DatabaseSize {
				code = "3D000"
			}
			return nil, false, SQLError(code, fmt.Sprintf(`%s "%s" does not exist`, label, name))
		}
	} else {
		oid, err := arg.OID()
		if err != nil {
			return nil, false, err
		}
		descriptor = view.Get(ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(oid)})
		if descriptor == nil {
			return nil, false, SQLError("42704", fmt.Sprintf("%s with OID %d does not exist", label, oid))
		}
	}
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return nil, false, err
	}
	auth.Catalog = view
	auth.Recapture(view)
	allowed := kind == functions.TablespaceSize && descriptor.Address().ObjectID == catalog.DefaultTablespaceOID
	if !allowed {
		allowed, _, err = auth.ObjectPrivilege(ctx, auth, auth.State.CurrentUser.ID(), descriptor.Address(), privilege, 0)
		if err != nil {
			return nil, false, err
		}
	}
	if !allowed {
		stats, found := auth.Roles.Lookup("pg_read_all_stats")
		if !found {
			return nil, false, fmt.Errorf("stuntgres: missing pg_read_all_stats role")
		}
		allowed, err = authorization.HasRole(ctx, auth.Roles, &e.RoleStore.Domain, auth.State.CurrentUser.ID(), stats.Ref.ID(), authorization.RoleUsage)
		if err != nil {
			return nil, false, err
		}
	}
	if !allowed {
		return nil, false, SQLError("42501", "permission denied for "+label+" "+descriptor.Name())
	}
	if kind == functions.DatabaseSize && e.Cluster.Targets[descriptor.Name()] == nil {
		return nil, false, fmt.Errorf("stuntgres: database allocation runtime is not registered")
	}
	var captures []storage.Allocation
	for name, target := range e.Cluster.Targets {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if kind == functions.DatabaseSize && name != descriptor.Name() {
			continue
		}
		targetView := target.Catalog.Current()
		var scope *StatementScope
		if target == e {
			scope, targetView = s, view
		}
		if kind == functions.DatabaseSize && targetView.Owner().DatabaseAddress() != descriptor.Address() {
			return nil, false, ids.ErrCatalogReplaced
		}
		for _, d := range targetView.Descriptors() {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			relation, ok := d.(*catalog.Relation)
			if !ok {
				continue
			}
			placement := catalog.DefaultTablespaceOID
			if catalog.SharedRelation(relation) {
				placement = catalog.GlobalTablespaceOID
			}
			if kind == functions.DatabaseSize && placement == catalog.GlobalTablespaceOID || kind == functions.TablespaceSize && placement != descriptor.Address().ObjectID {
				continue
			}
			a := captureAllocationLocked(target, scope, targetView, relation)
			if a.Allocated {
				captures = append(captures, a)
			}
		}
	}
	return captures, true, nil
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

// The exec capability groups enter core/inquiry with the statement host.

func (h *catalogInquiryHost) FormatType(oid uint32, typmod int32, specified bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.FormatType(h.host(), oid, typmod, specified, result)
}

func (h *catalogInquiryHost) BaseType(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.BaseType(h.host(), oid, result)
}

func (h *catalogInquiryHost) CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.CatalogUserName(h.host(), oid, result)
}

func (h *catalogInquiryHost) TablespaceLocation(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.TablespaceLocation(h.host(), oid, result)
}

func (h *catalogInquiryHost) TablespaceDatabase(oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, error) {
	return inquiry.TablespaceDatabase(h.host(), oid, result)
}

func (h *catalogInquiryHost) SizeInquiry(kind functions.SizeKind, arg sqltype.Value, fork string, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.SizeInquiry(h.host(), kind, arg, fork, result)
}

func (h *catalogInquiryHost) DatabaseFilesInquiry(kind functions.DatabaseFilesKind, arg, second sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.DatabaseFilesInquiry(h.host(), kind, arg, second, result)
}

func (h *catalogInquiryHost) PartitionTree(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	return inquiry.PartitionTree(h.host(), oid, result)
}

func (h *catalogInquiryHost) PartitionAncestors(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	return inquiry.PartitionAncestors(h.host(), oid, result)
}

func (h *catalogInquiryHost) PartitionRoot(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.PartitionRoot(h.host(), oid, result)
}

func (h *catalogInquiryHost) RelationIsPublishable(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.RelationIsPublishable(h.host(), oid, result)
}

func (h *catalogInquiryHost) PublicationTables(names sqltype.Value, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	return inquiry.PublicationTables(h.host(), names, result)
}

func (h *catalogInquiryHost) CommentInquiry(c *exec.Context, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.CommentInquiry(h.host(), c, kind, values, provenance, result)
}

func (h *catalogInquiryHost) DeparseInquiry(c *exec.Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.DeparseInquiry(h.host(), c, signature, values, result)
}

func (h *catalogInquiryHost) DescribeObject(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.DescribeObject(h.host(), c, values, result)
}

func (h *catalogInquiryHost) IdentifyObject(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.IdentifyObject(h.host(), c, values, result)
}

func (h *catalogInquiryHost) IdentifyObjectAddress(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.IdentifyObjectAddress(h.host(), c, values, result)
}

func (h *catalogInquiryHost) IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error) {
	return inquiry.IndexProperty(h.host(), kind, oid, ordinalValue, property)
}

func (h *catalogInquiryHost) LookupObjectAddress(c *exec.Context, values [3]sqltype.Value) (ids.ObjectAddress, error) {
	return inquiry.LookupObjectAddress(h.host(), c, values)
}

func (h *catalogInquiryHost) VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.VisibilityInquiry(h.host(), kind, value, result)
}

func (h *sessionInquiryHost) CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.CurrentSchemas(h.host(), implicit, result)
}

func (h *sessionInquiryHost) IdentityValue(c *exec.Context, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error) {
	return inquiry.IdentityValue(h.host(), c, kind, result)
}

func (h *sessionInquiryHost) MyTempNamespace() (uint32, error) {
	return inquiry.MyTempNamespace(h.host())
}

func (h *sessionInquiryHost) OtherTempNamespace(oid uint32) (bool, error) {
	return inquiry.OtherTempNamespace(h.host(), oid)
}

func (h *sessionInquiryHost) ListeningChannels(result *sqltype.Descriptor) ([]sqltype.Value, error) {
	return inquiry.ListeningChannels(h.host(), result)
}

func (h *sessionInquiryHost) NotificationQueueUsage() (float64, error) {
	return inquiry.NotificationQueueUsage(h.host())
}

func (h *transactionInquiryHost) TransactionAge(xid uint32) (int32, error) {
	return inquiry.TransactionAge(h.host(), xid)
}

func (h *transactionInquiryHost) MultiTransactionAge(xid uint32) (int32, error) {
	return inquiry.MultiTransactionAge(h.host(), xid)
}

func (h *transactionInquiryHost) CurrentTransactionID(assign bool) (uint64, error) {
	return inquiry.CurrentTransactionID(h.host(), assign)
}

func (h *transactionInquiryHost) CurrentSnapshot() (*sqltype.Snapshot, error) {
	return inquiry.CurrentSnapshot(h.host())
}

func (h *transactionInquiryHost) TransactionStatus(full uint64) (string, bool, error) {
	return inquiry.TransactionStatus(h.host(), full)
}

func (h *transactionInquiryHost) TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	return inquiry.TransactionMembers(h.host(), xid, result)
}

func (h *transactionInquiryHost) CommitTimestamp(xid uint32) (time.Time, uint32, bool, error) {
	return inquiry.CommitTimestamp(h.host(), xid)
}

func (h *transactionInquiryHost) LastCommittedTransaction() (uint32, time.Time, uint32, bool, error) {
	return inquiry.LastCommittedTransaction(h.host())
}
