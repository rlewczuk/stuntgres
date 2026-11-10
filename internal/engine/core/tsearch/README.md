# internal/engine/core/tsearch

Text-search runtime below core: configured conversion, dictionary acquisition,
inspection and nested SQL. It never imports core; pure immutable FTS algebra
belongs to `fts`, and core supplies statement/session adapters.

## Contract

- `Convert` (to_tsvector, to_tsquery and its plain, phrase and websearch
  forms), `ConvertJSON`, `Headline`, `CurrentConfig`, `Debug`, `Stat`, `Rewrite`,
  `Lexize`, `Unaccent` and `ResolveParser` are the bodies of `exec.TextSearchRuntime`.
- `CaptureConfiguration` captures one configuration and its parser from an
  immutable catalog read; `Capture.Walk` tokenizes and lexizes a text,
  acquiring each consulted dictionary on demand; `Capture.Accept` is the final
  currentness and cancellation fence that publishes new dictionary receipts.
  `Captured` reports whether a configuration is held.
- `DictionaryMemos` maps a dictionary to one successful physical-session
  initialization; its entries stay opaque to core. Active hookable ordinary
  initializers reuse the actual compiler, not a callback notification around
  an already compiled pointer.

## Host and authority

- `Host[A]` is the statement a call runs in; core implements it on
  `statementHost` (the shared leaf method set). It gives the operation
  (context, settings, notices), the I/O environments (the captured regconfig
  input), work admission, the Engine.Mu pair, the pending catalog journal and
  the private catalog over it, the operation's authorization, the namespace
  authorization's catalog and regconfig/regdictionary resolver, the
  thesaurus compiler's dictionary host, the session's `DictionaryMemos`, and
  the `exec.NestedSQL` capability.
- `Authority[A]` is that authorization, held by value: a `ddl.Authority` for
  `extension.CaptureThesaurusInput`, `Access` for the current role, and
  `Recaptured` over another catalog read. The package is generic over it, as
  `analyze.PathInput` is, so capturing and recapturing it never allocates.
  Core's `AuthorizationView` implements it.

## Locks

- Calls are entered with no engine lock held. `Host.ReacquireEngine` takes
  `Engine.Mu` and `Host.ReleaseEngineLocked` releases it; the host methods
  named …Locked, `tsAcquisition.currentLocked` and the thesaurus checks run
  inside those windows. `DictionaryMemos` belongs to `Engine.Mu`.
- Parser, dictionary and resource compilation, lexization, notices and nested
  SQL run with nothing held. Engine.Mu precedes the effective settings mutex.

## Behaviour

Capture immutable catalog/configuration identity under `Engine.Mu`, then release
it before parser/resource compilation, lexization, notices or nested SQL.
Acquisition and final `Accept` fence current descriptors and cancellation before
publishing successful session dictionary memos. Failed work publishes no memo
prefix. Nested SQL borrows the caller's operation and restores lexical state on
all exits; raw values retain no catalog or invocation authority.

## Testing

Pure runtime helpers are tested here; core and `core/enginetests/textsearch`
exercise actual hosts and lifecycle. Public SQL belongs to the owning text-search
reference packages.
