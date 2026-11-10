# Glossary

The engine's working vocabulary. Each term has one meaning. Use these words in
identifiers, comments, package READMEs and task descriptions, and do not use a
defined word for anything else.

PostgreSQL terms (relation, tuple, snapshot isolation, ACL, …) keep their
PostgreSQL meaning and are not repeated here. This file defines only the words
the engine adds, and the words it uses more narrowly than everyday English.

## Rules

- **One word, one meaning.** If a new concept needs a name, pick a word that is
  not listed here.
- **Qualify a homonym, do not rely on context.** Where a word is listed under
  [Words with a reserved sense](#words-with-a-reserved-sense), use the qualified
  form given there for the other sense.
- **Name things by what they are, not by how carefully they were made.** Words
  such as "genuine", "authentic", "exact" and "actual" add no information to an
  identifier or a contract; say what is compared or checked instead.

## State and commit

The engine keeps committed state in immutable structures and replaces them
atomically.

| Term | Meaning |
|---|---|
| **root** | The top pointer of one immutable structure (a catalog `State`, a relation's versions, an index, the role graph). Readers hold a root and are never affected by later writes. |
| **committed** | Reachable from the engine's current roots; visible to new statements subject to snapshot rules. |
| **private** | Built by one statement or transaction and not yet published. Nobody else can see it. |
| **capture** | An immutable record of facts taken at one moment, usually under `Engine.Mu`, so that later work can run without the lock. A capture borrows roots; it copies nothing mutable and grants no right to write. |
| **view** (unqualified: avoid) | See [reserved senses](#words-with-a-reserved-sense). A paired catalog-and-role capture is an **authorization view**; a relation-version capture is a **data view**. |
| **stage** | The private set of changes one statement accumulates before publishing. `CatalogStage` holds catalog changes; `WriteStage` holds row changes. A stage is discarded on failure and never partially published. |
| **candidate** | One proposed replacement value inside a stage: a new descriptor, role graph or storage root, together with the predecessor it expects to replace. |
| **journal** | A transaction's ordered record of its private changes and fences, kept so that savepoints can roll back and commit can publish. |
| **publish** | Install new roots as the committed state, in one step with no failure point and no callbacks. Everything fallible happens before. In identifiers use `Publish…`; `Publication` names only the SQL object (see reserved senses). |
| **fence** | A precondition recorded while preparing a change (for example "this relation's storage root is still X") and rechecked under the lock immediately before publish. A failed fence aborts or retries; it never retargets the change. |
| **frontier** | The high-water mark of an allocator or of visibility: the next OID, the next revision, the commit sequence a snapshot reads up to. A frontier only advances, and only on successful publish. |
| **epoch** | The identity of one whole catalog. Reset and Restore start a new epoch; every reference minted in the old epoch then fails with `ErrCatalogReplaced`. |
| **revision** | The version counter of one object's definition. It changes when that object is altered. |
| **generation** | The version of a shared shape that stored expressions are tagged with: a type's representation, an enum's labels, an index layout. Changing the shape creates a new generation, and stored expressions are **retagged** to it. |
| **incarnation** | One lifetime of a named thing. Dropping and recreating a database, or rewriting a table's storage, gives a new incarnation under the same name. |
| **lineage** | The recorded ancestry of physical data across rewrites: which storage a copy was made from. Used to decide whether an earlier observation still applies. |
| **provenance** | Which engine, epoch and owner a value was minted by. Two values with equal numbers but different provenance are different; provenance is checked before any other test. |
| **backing** | The immutable memory behind a value (a slice, a map, a row payload). "Shares backing" means no copy was made. |
| **retire** | End the life of an object or resource for good: its identity is never reused. Not a synonym for SQL `DROP`, which is the statement; a drop retires the objects it removes. |
| **replacement** | Swapping the whole catalog during Reset or Restore. Distinct from publishing an ordinary change. |
| **seed** | The built-in catalog contents created at engine start, before any user statement. |

## Sessions, statements and locks

| Term | Meaning |
|---|---|
| **session** | One client connection's state: settings, temporary objects, prepared statements, transaction attachment. |
| **operation** | One client command as the session sees it, with its effective settings and output environment. A command that contains several statements is still one operation. |
| **statement scope** | The engine-side state of one executing statement (`StatementScope`): its transaction, authority, stage and results. Created at statement start and finished exactly once. |
| **transaction owner** | The record that holds one transaction's private rows, journal, snapshot roots and savepoints (`TransactionOwner`). |
| **admission** | The checked decision to let something proceed: a statement entering its transaction, a connection entering a database, a restore statement being accepted. Admission is granted once and released explicitly. |
| **lease** | A statement's identity token inside the session layer (`StatementLease`), compared by address. Session code uses it to tell whether the statement that reserved something is still the current one. |
| **ticket** | A transaction's registration with the lock manager. Locks are granted to tickets. |
| **lock window** | A stretch of code that runs with `Engine.Mu` held. Callbacks, waits, expression evaluation and I/O happen outside lock windows. |
| **`…Locked` suffix** | The caller holds `Engine.Mu` on entry, and the function returns with it held. A function that releases the lock on any path does not carry this suffix. |
| **mark** | A position in a journal or lock-grant history that a rollback can return to. A SQL savepoint owns one mark; PL/pgSQL exception blocks use internal marks. |
| **checkpoint** | The saved statement state that a PL/pgSQL exception block restores on error. Not related to the SQL `CHECKPOINT` command. |
| **effect** | Work a statement queues for later in the same command or transaction: AFTER triggers, deferred constraints, notifications. |
| **slot** | A field that holds one feature's state inside a shared struct without the struct knowing its type. The feature stores and recovers it through one checked helper; nil means absent. |
| **two-phase** | `PREPARE TRANSACTION` and its commit or rollback. Never called "prepared" (see reserved senses). |

## Identity and authority

| Term | Meaning |
|---|---|
| **domain** | The identity of one engine instance (`ids.Domain`), used as a pointer. All references carry their domain, so references from another engine never match. Roles have a separate `authorization.Domain`. |
| **OID** | The PostgreSQL-visible number of an object. A number alone never proves identity. |
| **address** | Class, object and sub-object numbers of a catalog object (`ObjectAddress`). |
| **ref** | An address plus provenance (`ObjectRef`). The only form of object identity that grants access to anything. |
| **principal** | A role as an identity with provenance (`PrincipalRef`), as opposed to a role name or a role OID. |
| **authority** | The right to perform an action, carried as an opaque value that only its issuer can interpret. Names, OIDs and equal-looking values never confer authority. |
| **authorization view** | An immutable paired capture of catalog and role state plus the current, session and authenticated role (`access.View`). All privilege checks consume one. |
| **object owner** | The role that owns a catalog object, in the PostgreSQL sense. |
| **holder** | The component responsible for a resource's lifetime, when "owner" would be confused with the object owner. |
| **detached** | Not connected to a live session or transaction: a detached token identifies something for display or bookkeeping and can authorize nothing. |

## Package seams

| Term | Meaning |
|---|---|
| **layer** | A package's position in the import order. A package imports only lower layers. |
| **family** | One group of related statements and its executor package (`tablecmds`, `viewcmds`, …). |
| **leaf** | A package that implements one focused feature and imports no sibling feature. |
| **Host** | An interface a lower package declares for the services it needs from the engine, implemented in a higher layer. A Host is declared by its consumer and lists only what that consumer calls. |
| **Runtime** | A Host for expression evaluation: the capability a function node recovers from the evaluation context at run time. |
| **capability** | One narrow group of related Host methods, obtained as a unit. |
| **host routine** | A SQL routine implemented by the embedding Go application. The word "host" alone never means this. |
| **hook** | A fixed extension point filled once by composition (`Engine.Statements`, `Engine.Factory`) or an entry in the ordered Restore table. Hooks are never installed per statement. |
| **payload** | A value a lower package stores without knowing its concrete type, such as a view's stored definition inside a catalog descriptor. Its owner package recovers it through one accessor. |
| **provider** (qualify) | See reserved senses: **kind provider**, **extension provider**, **selected provider**. |

## Analysis and expressions

| Term | Meaning |
|---|---|
| **binder** | The component that resolves names and types for one statement (`analyze.Binder`). |
| **binding** | The binder's output for one statement: typed trees plus what the statement depends on. Immutable once produced. |
| **describe** | Bind a statement without running anything, to learn parameter and result types. |
| **node** | One element of a bound expression or query tree. Nodes are immutable. |
| **kernel** | The pure computation behind a function or operator, free of engine state. |
| **selected provider** | The specific implementation that analysis chose for an operator, function or aggregate call. Stored with the node so that later name changes cannot change behavior. |
| **recipe** | The complete stored description of how to compute something: selected provider, conversions, operand order and dependencies. A recipe is stored and replayed; source text alone is not a recipe. |
| **occurrence** | One syntactic appearance of a table, column or expression in a statement. Two references to the same table are two occurrences. |
| **usage** | A type as used at one site (`sqltype.Usage`): the type plus modifiers and collation. |
| **demand** | What an expression needs from its environment in order to run (a snapshot, catalog text, an argument's value), reported without evaluating it. |
| **fold** | Replace an expression by its constant value before execution. An **accepted fold** has been checked and retained for a prepared statement. |
| **preflight** | Prepare an expression for execution without running it: materialize conversions, simplify, fold what may be folded. |
| **closed** | An expression is closed when it needs no row, parameter or statement state, so a DEFAULT built from it can be evaluated in isolation. |
| **intrinsic** | A property a node has by itself, excluding its children (intrinsic volatility). |
| **jumble** | Compute the statement fingerprint used by `pg_stat_statements`, as PostgreSQL does. |
| **retag** | Rewrite a stored expression to refer to new generations of the types it uses. |
| **remap** | Rewrite a stored expression or definition to refer to different objects or column slots, for example after LIKE, inheritance or Restore. |
| **reown** | Rebind stored values or definitions to equivalent identities in another catalog, during replacement. |
| **installed** | Provided by a created extension, as opposed to built in. An installed routine exists only while its extension does. |
| **stock** | Shipped with the engine and available to `CREATE EXTENSION` (a stock extension), as opposed to registered by the application. |

## Dump and Restore

| Term | Meaning |
|---|---|
| **dump capture** | The coherent copy of engine state that Dump takes under the lock and then renders without it. |
| **marker** | A versioned comment line in a dump script (`-- stuntgres-<kind>-vN <data>`) that carries state SQL cannot express. A marker grants nothing by itself; Restore validates it against real objects. |
| **restore stage** | The isolated private engine on which Restore replays a script before replacing the destination. |
| **replay** | Execute a dump script's statements on the restore stage. A statement runs "under replay" when Restore, not a client, issued it. |
| **directive** | A statement in a dump script that sets context for the statements after it (such as the owner of the next created object) instead of creating anything. |

## Words with a reserved sense

These words are easy to overload. Each has one sense here; use the qualified
form for anything else.

| Word | Reserved sense | Say instead for other senses |
|---|---|---|
| **host** | An adapter interface (see Host). | *host routine* for application-provided routines; *client address* for `pg_hba` hosts. |
| **publication** | The SQL `PUBLICATION` object of logical replication. | *publish* (verb and `Publish…` identifiers) for installing committed roots. |
| **prepared** | A SQL prepared statement (`PREPARE`, protocol Parse). | *two-phase* for `PREPARE TRANSACTION`; *candidate* for a change that is ready to publish. |
| **stage** | A statement's private unpublished changes. | *restore stage* for the Restore engine; *producer* for a step of query execution. |
| **view** | The SQL view object. | *authorization view*, *data view*. |
| **owner** | The role that owns a catalog object. | *holder* for lifetime responsibility; *transaction owner* is a fixed compound. |
| **provider** | Always qualified. | *kind provider* (validation for one catalog object kind), *extension provider* (one stock extension's implementation), *selected provider* (see above). |
| **capture** | An immutable record of facts (see above). | *dump capture* for Dump; do not use for "catch" or "record a call". |
| **admission** | The checked decision to proceed (see above). | Use *allowed* or *legal* for a purely syntactic rule about where a construct may appear. |
| **foreign** | Foreign data (FDW): foreign tables, servers, wrappers. | *foreign key* is always written out in full, never shortened to "foreign". |
| **checkpoint** | Saved statement state for a PL/pgSQL exception block. | *CHECKPOINT command* for the SQL statement. |
