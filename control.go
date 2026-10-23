package stuntgres

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"stuntgres/internal/engine"
)

// withDatabaseTarget captures the immutable selected runtime and releases its
// borrower before I/O. Driver owns the target for the cluster's lifetime.
func withDatabaseTarget(ctx context.Context, db *sql.DB, fn func(*engine.Engine) error) error {
	if db == nil {
		return errors.New("stuntgres: nil database")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	var e *engine.Engine
	err = c.Raw(func(raw any) error {
		native, ok := raw.(*conn)
		if !ok {
			return errors.New("stuntgres: controls require a native stuntgres database, not a foreign or wrapped driver")
		}
		if err := native.check(ctx); err != nil {
			return err
		}
		e = native.engine
		return nil
	})
	closeErr := c.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return fn(e)
}

// IndexState is a detached observation of an index's publication state.
// Ready permits write maintenance; Valid permits query use; Live permits use at all.
type IndexState struct {
	Ready, Valid, Live bool
	// WaitingForOlderSnapshots observes the live builder's admitted older
	// snapshot cohort, not a persisted phase or a registered waiter count.
	WaitingForOlderSnapshots bool
	// WaitingForWriters observes a frozen granted-writer cohort. Ready=false
	// identifies the first cohort; Ready=true identifies the second cohort.
	WaitingForWriters bool
}

// InspectIndex observes the exact decoded schema and index name, without a
// session search_path. Missing schemas, missing indexes and non-index relations
// return ordinary control errors. Like Reset, it requires an unwrapped native pool.
func InspectIndex(ctx context.Context, db *sql.DB, schema, name string) (IndexState, error) {
	var state IndexState
	err := withDatabaseTarget(ctx, db, func(e *engine.Engine) error {
		ready, valid, live, older, writers, err := e.InspectIndex(ctx, schema, name)
		state = IndexState{Ready: ready, Valid: valid, Live: live, WaitingForOlderSnapshots: older, WaitingForWriters: writers}
		return err
	})
	return state, err
}

// Reset atomically clears persistent objects, retaining database identity,
// roles, session settings, live session-private temporary relations and held
// physical-session advisory counts. Old pending lock requests remain epoch-fenced.
// Prepared transactions prevent replacement without consuming their state.
// Startup commit-timestamp configuration survives; successful replacement retires
// runtime commit history and last completion without reusing assigned XIDs.
// Live temporary tables inheriting persistent parents prevent Reset/Restore
// replacement; temporary-to-temporary graphs retain their exact identities.
// Persistent policies disappear with their tables; live TEMP table policies
// retain their identities. Incompatible live TEMP policy dependencies prevent
// replacement rather than adopting new same-name persistent providers.
// Live TEMP rules likewise retain their event/action identities and exact
// dependency provenance. Incompatible dependencies refuse replacement; retained
// rule references advance to the installed catalog lifetime without importing IDs.
// Temporary views retain exact source identities. Invalidated persistent table
// or view providers prevent replacement; unchanged pinned catalog providers survive.
// Same-owner temporary FK action graphs retain exact definition/parent/key/list
// identities, storage, defaults and prepared provenance.
// Persistent registered extensions and their member identities are removed together.
// Persistent large objects and their bytes/owner/raw ACLs are removed together.
// Live destination TEMP-target installations retain their exact namespace token,
// extension/member/provider identities, owner authority and owned TEMP consumers.
// Retained TEMP view manifests keep exact captured procedures; this contextual
// epoch authority does not make ordinary stale ObjectRefs valid.
// Destination current-database ownership/CREATE ACLs are attachment authority
// and survive Reset/Restore; they are not imported from a portable snapshot.
// Live temporary typed tables attached to persistent standalone types likewise
// prevent replacement rather than retargeting their source by name or numeric OID.
func Reset(ctx context.Context, db *sql.DB) error {
	return withDatabaseTarget(ctx, db, func(e *engine.Engine) error { return e.Reset(ctx) })
}

// Dump writes a consistent PostgreSQL-compatible persistent SQL snapshot,
// including domains, independently reconstructed user collations/conversions,
// owned operator definitions and shells, simple B-tree indexes, ownership, comments and
// called generator state. Conversion seed deltas and legal duplicate defaults
// replay without serializing codec data or external routine libraries.
// Operator aliases replay by exact qualified signature, including nested carriers.
// Orphan shells use transient real generator DDL; a shell without a compatible
// registered implementation provider is rejected before writer I/O.
// Named owners must already exist in the destination. Each relation owner group
// explicitly selects its session authorization, including the bootstrap owner;
// all tables precede sequence OWNED BY associations that depend on them.
// Session-private temporary objects, advisory ownership/counts and pending session
// caches are not serialized. Stored CHECK/view definitions remain ordinary SQL.
// Only committed data is serialized, never prepared private writes or GIDs.
// Large objects retain fixed class-local OIDs, owners and exact raw ACLs.
// Sparse stored chunks (including terminal zeros), not holes, are serialized
// separately from relation declaration graph identities.
// Commit timestamp history and startup configuration are runtime-only, not dumped;
// legal inquiry expressions in defaults/views remain symbolic SQL.
// Inheritance parents precede children; ordered INHERITS and local declaration
// provenance reconstruct inherited defaults, generated columns, CHECK and NOT NULL.
// Table-attached policies are emitted in a final named-owner pass after their
// persistent providers. Policy conditions remain stored SQL, without evaluation.
// Persistent policies referencing TEMP objects are rejected before writer I/O.
// Protected raw rows are exported before independent enabled/forced flag ALTERs;
// policy-expression enforcement remains unsupported.
// Ordinary rewrite rules are emitted after persistent definitions and data.
// TEMP event rules are omitted; persistent rules referencing TEMP providers
// are rejected before writer I/O rather than exporting dangling dependencies.
// Valid FK declarations use ordinary CREATE syntax; invalid NOT NULL, CHECK and
// FK definitions follow ordinary data in eligible ALTER TABLE ADD statements.
// Native Restore handles valid FK forward references/cycles independently of
// table definition output order and preserves invalid definitions without
// disabling enforcement for newly inserted or updated rows.
// All admitted ON DELETE/UPDATE actions and DELETE SET subsets are serialized
// canonically using current stable-attribute names, without evaluating defaults.
// Registered extension installations precede their CHECK/view consumers. Native
// ownership metadata comments preserve independently named extension/member
// owners, even when destination role attributes differ; templates are ownerless.
// PostgreSQL ignores these comments and applies ordinary CREATE EXTENSION
// ownership on SQL replay. No arbitrary extension script/shared library is dumped.
// A committed TEMP-target installation returns the ordinary portability error
// "stuntgres: temporary extension installation is not portable" before writer I/O;
// it is not silently omitted alongside persistent consumers. Private installations
// remain excluded from the committed snapshot.
// Typed tables retain qualified OF sources and local WITH OPTIONS definitions;
// each standalone source precedes its dependent table, even with no members.
// SQL/MED wrappers, servers, named/PUBLIC mappings and foreign tables retain
// ordered options, supported owners and logical column metadata. Remote rows
// and executable handlers/validators are never serialized. Mapping credentials
// are included faithfully: callers must protect this sensitive output.
// Unsupported explicit foreign-object/relation/column ACLs fail before bytes.
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error {
	if dst == nil {
		return errors.New("stuntgres: nil dump writer")
	}
	return withDatabaseTarget(ctx, db, func(e *engine.Engine) error { return e.Dump(ctx, dst) })
}

// Restore atomically replaces persistent objects after supported CREATE/ALTER/DROP
// SCHEMA, CREATE COLLATION (and ALTER COLLATION), CREATE [DEFAULT] CONVERSION
// (and ALTER CONVERSION), CREATE/ALTER/DROP OPERATOR, CREATE TYPE AS ENUM, CREATE DOMAIN,
// CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, registered CREATE EXTENSION providers,
// parameter-free scalar INSERT (including integer
// arithmetic), database-local COMMENT, sequence OWNED BY associations, generated
// SELECT setval(name,value,is_called), generated literal large-object create/put
// SELECTs, exact single-object raw large-object ACL repairs, and named owner
// directives succeed. Parameters and arbitrary function SELECTs remain excluded.
// SQL/MED declarations and their owner transfers require authentic destination
// registrations and existing named roles. Option validators may run privately;
// importer, scan and modify callbacks never run. IMPORT, foreign writes and
// arbitrary foreign metadata mutations are excluded. Replay does not add grants.
// ROLE comments are shared authority metadata: Reset and Restore preserve them,
// and Dump excludes them. Query INSERT sources are deliberately not Restore
// syntax; Dump emits canonical literal VALUES.
// Caller-owned temporary tables, sequences and views remain live and are excluded
// by Dump. Invalidated persistent providers of live temporary views prevent final
// replacement; temporary-only chains and unchanged pinned catalog providers survive.
// Restore input cannot create temporary views.
// Held physical-session advisory counts survive successful installation; old
// pending requests remain epoch-fenced. Direct advisory INSERT scalars remain
// excluded; stored CHECK replay uses the existing isolated admitted stage session.
// Its initialized private catalog preserves legacy streams and reconstructs
// bootstrap namespace lifecycle changes without synthesizing ACL grants.
// FK owner/schema authority is captured at declaration. Attachments and final
// valid relationships are validated after isolated table/index/data staging,
// before installation. Eligible NOT VALID ADD declarations retain historical
// rows; arbitrary ALTER and VALIDATE CONSTRAINT remain excluded from Restore.
// Actions/lists survive import; Restore never cascades to repair invalid data.
// Named owners must already exist. Private Restore-stage CREATE TABLE/COLLATION
// destination authority preserves independent schema/object owners without ACL grants.
// Private stage INSERT and database-local COMMENT use validated captured bootstrap
// authority without changing named owner directives, session identity or ordinary
// SQL authorization. Neither command stamps ownership or synthesizes ACL grants.
// Exact generated large-object byte replay uses private import authority under
// the original owner, then installs raw ACLs without synthetic owner privileges.
// Policy TO roles must already exist and retain captured principal provenance.
// CREATE POLICY uses validated private import authority without granting ordinary
// SQL privileges. TO CURRENT_USER/CURRENT_ROLE/SESSION_USER resolve against the
// actual named stage-session identities, never the trusted authorization actor.
// Policy conditions and provider identities remap atomically;
// live TEMP policies retain their table lifetime and incompatible dependencies
// refuse replacement. Policies and enabled/forced flag ALTERs import independently.
// Ordinary rules bind in the isolated import catalog and become live only with
// atomic installation. Retained live TEMP rules keep their exact providers and
// identities; incompatible providers refuse replacement rather than rebinding.
// Strict native extension-owner comments require complete symbolic member
// identities and preexisting named principals, never imported OID authority.
// Missing/mismatching metadata or target principal drop/recreation rejects final
// replacement without disturbing the target's executable installed provider.
// Plain streams use ordinary trusted installation ownership; this is not a
// general ALTER FUNCTION/EXTENSION ownership or arbitrary package-loading API.
// Live destination TEMP installations retain exact namespace/provider/member and
// principal authority. Imported OID collisions are reminted, not retained TEMP
// identities; a conflicting global extension name rejects the whole replacement.
// Source TEMP installations cannot transfer physical-session authority, and plain
// replay cannot resolve names by borrowing the destination's TEMP namespaces.
// Arbitrarily blocking readers cannot be interrupted by context cancellation.
// Final installation refuses while prepared transactions exist, including entries
// prepared during reader I/O; refusal preserves the target catalog and resources.
// Live temporary-to-persistent inheritance also refuses final replacement,
// including an edge created during reader I/O, without changing the target.
// Successful replacement retires target commit history, never importing private
// replay commits; failure leaves target history and startup configuration intact.
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error {
	if src == nil {
		return errors.New("stuntgres: nil restore reader")
	}
	return withDatabaseTarget(ctx, db, func(e *engine.Engine) error { return e.Restore(ctx, src) })
}
