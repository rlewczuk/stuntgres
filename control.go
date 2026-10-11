package stuntgres

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"stuntgres/internal/engine"
)

// withEngine releases the borrowed connection before performing I/O. The engine
// identity is owned by Driver and outlives the borrowed connection.
func withEngine(ctx context.Context, db *sql.DB, fn func(*engine.Engine) error) error {
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
		e = native.session.Engine()
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
	err := withEngine(ctx, db, func(e *engine.Engine) error {
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
// Live temporary tables inheriting persistent parents prevent Reset/Restore
// replacement; temporary-to-temporary graphs retain their exact identities.
// Persistent policies disappear with their tables; live TEMP table policies
// retain their identities. Incompatible live TEMP policy dependencies prevent
// replacement rather than adopting new same-name persistent providers.
// Temporary views retain exact source identities. Invalidated persistent table
// or view providers prevent replacement; unchanged pinned catalog providers survive.
// Same-owner temporary FK graphs retain exact definition/parent/key identities.
// Persistent registered extensions and their member identities are removed together.
// Live destination TEMP-target installations retain their exact namespace token,
// extension/member/provider identities, owner authority and owned TEMP consumers.
// Retained TEMP view manifests keep exact captured procedures; this contextual
// epoch authority does not make ordinary stale ObjectRefs valid.
// Destination current-database ownership/CREATE ACLs are attachment authority
// and survive Reset/Restore; they are not imported from a portable snapshot.
// Live temporary typed tables attached to persistent standalone types likewise
// prevent replacement rather than retargeting their source by name or numeric OID.
func Reset(ctx context.Context, db *sql.DB) error {
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Reset(ctx) })
}

// Dump writes a consistent PostgreSQL-compatible persistent SQL snapshot,
// including domains, independently reconstructed user collations, simple B-tree
// index definitions, sequence options, ownership, comments and called generator state.
// Named owners must already exist in the destination. Each relation owner group
// explicitly selects its session authorization, including the bootstrap owner;
// all tables precede sequence OWNED BY associations that depend on them.
// Session-private temporary objects, advisory ownership/counts and pending session
// caches are not serialized. Stored CHECK/view definitions remain ordinary SQL.
// Only committed data is serialized, never prepared private writes or GIDs.
// Inheritance parents precede children; ordered INHERITS and local declaration
// provenance reconstruct inherited defaults, generated columns, CHECK and NOT NULL.
// Table-attached policies are emitted in a final named-owner pass after their
// persistent providers. Policy conditions remain stored SQL, without evaluation.
// Persistent policies referencing TEMP objects are rejected before writer I/O.
// Protected raw rows are exported before independent enabled/forced flag ALTERs;
// policy-expression enforcement remains unsupported.
// FK declarations are ordinary CREATE syntax; native Restore handles their
// forward references/cycles independently of table definition output order.
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
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error {
	if dst == nil {
		return errors.New("stuntgres: nil dump writer")
	}
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Dump(ctx, dst) })
}

// Restore atomically replaces persistent objects after supported CREATE/ALTER/DROP
// SCHEMA, CREATE COLLATION (and ALTER COLLATION), CREATE TYPE AS ENUM, CREATE DOMAIN,
// CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY, registered CREATE EXTENSION unaccent1.1,
// parameter-free scalar INSERT (including integer
// arithmetic), database-local COMMENT, sequence OWNED BY associations, generated
// SELECT setval(name,value,is_called), and named owner directives succeed.
// Parameters are recursively rejected; other statements remain excluded.
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
// rows are validated after isolated table/index/data staging, before installation;
// this does not admit public ALTER ADD FOREIGN KEY or defer ordinary SQL checks.
// Named owners must already exist. Private Restore-stage CREATE TABLE/COLLATION
// destination authority preserves independent schema/object owners without ACL grants.
// Private stage INSERT and database-local COMMENT use validated captured bootstrap
// authority without changing named owner directives, session identity or ordinary
// SQL authorization. Neither command stamps ownership or synthesizes ACL grants.
// Policy TO roles must already exist and retain captured principal provenance.
// CREATE POLICY uses validated private import authority without granting ordinary
// SQL privileges. TO CURRENT_USER/CURRENT_ROLE/SESSION_USER resolve against the
// actual named stage-session identities, never the trusted authorization actor.
// Policy conditions and provider identities remap atomically;
// live TEMP policies retain their table lifetime and incompatible dependencies
// refuse replacement. Policies and enabled/forced flag ALTERs import independently.
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
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error {
	if src == nil {
		return errors.New("stuntgres: nil restore reader")
	}
	return withEngine(ctx, db, func(e *engine.Engine) error { return e.Restore(ctx, src) })
}
