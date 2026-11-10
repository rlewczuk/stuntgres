# internal/xml

A standard-library XML parser, XPath evaluator and XSLT runtime. SQL type
admission, SQLSTATE mapping, destination codecs, notices and statement publication
belong to the adapters, not this package.

## Entry points

- `Parse(ctx, text, Options)` validates DOCUMENT or CONTENT and returns an
  immutable `Result`. `IsDocument` classifies accepted characters independently
  of their original mode; `IsWellFormed` suppresses recoverable parser failures.
- `Serialize` converts accepted XML to characters with explicit mode/indent
  policy. `OutputText`, `OutputBinary` and `OutputBinaryCharacters` separately
  own typed declaration/transport output. Character serialization and typed
  output are not interchangeable.
- Construction and row/scope mapping publish generated results without retaining
  a parser, tree or SQL environment. Builders are invocation-owned and cannot
  publish an earlier prefix after failure.
- XPath operations share a compiler/evaluator with invocation-private document,
  namespace, focus and scratch state. Compiled expressions contain immutable
  syntax only. `Table` owns one relational row/column-path invocation; SQL target
  input, defaults and constraints remain adapter responsibilities.
- Legacy scalar/table operations have distinct parsing and diagnostic policies.
  They reuse the core machinery without changing strict XML consumers.
- `ProcessXSLT` owns source/style readers, compilation, transformation and saving
  for one invocation. Stage-specific errors preserve ordered diagnostics; only
  a completed transformation publishes immutable result text.

## Ownership and errors

`Result` retains original character spelling, mode/declaration metadata, ordered
warnings and validity, never a DOM, context, parser frame or conversion scratch.
Binary input is decoded rather than retained as transport bytes. A zero Result
is not accepted empty CONTENT. Diagnostic accessors copy their slice; immutable
strings may share backing.

Transient document collectors, entity/namespace environments, compiled transform
state and mutable buffers belong to one operation. Source/result nodes are never
shared mutably. Completed values remain independent of later calls and concurrent
invocations.

`ParseError` preserves requested/effective modes and ordered diagnostic facts.
Source-local byte offsets, character columns and entity-source identity are
separate from SQL query positions. Provider errors never invent SQLSTATE or C
source metadata. Warning delivery belongs to the caller; formatting or later
failure must not erase already produced diagnostics.

## Input, output and resource boundaries

`Options.InputKind` distinguishes already-decoded UTF8 `Text`, PostgreSQL-codec
`Binary`, and declaration-decoded `RawMemory` DOCUMENT bytes. Each entry owns its
framing and diagnostic precedence. A recognized encoding label is not executable
codec authority. Conversion reuses immutable mappings from `pgencoding18` or
independently licensed local inputs, never host handlers.

All external SYSTEM/PUBLIC subsets and entities are intrinsically empty. There is
no loader interface or filesystem/network/catalog lookup. XSLT can read only
owned in-memory document identities; external loading is denied before I/O.
There is no subprocess, plugin or native-library execution path.

State is per operation: no locks, pools or mutable global caches. Callers supply
nonnil contexts. Scanner, decoding, expansion, copying and emission loops poll
cancellation in bounded work, including final publication. Size arithmetic is
checked before growth. Failures return no partial successful Result/output and
leave accepted input unchanged. XML character/name rules use fixed release data,
not host Unicode categories.

## Generated data and provenance

[Source inputs](data/README.md) owns recognition/mapping provenance, licensing,
independent oracle corpora and reproduction. From the repository root,
`go run ./internal/xml/generate` validates vendored inputs and emits immutable
lookup data deterministically. Runtime never reads those source files or host
mapping data.

## Testing

Units call actual parser, output, serializer, XPath and transform functions without
SQL or native-library execution. Public SQL compatibility belongs in dual-mode
`reference/` tests; follow [docs/TESTING.md](../../docs/TESTING.md). Expected values
come from independent pinned captures or explicitly identified release-source
rules, not candidate-generated output.

Reusable selectors: `RUN='^TestXML(Core|Produce)'`, `RUN='^TestXMLDTD'`,
`RUN='^TestXMLSerialize'`, `RUN='^Test(XMLPredicate|XMLXPath|XPath)'`,
`RUN='^TestXMLRaw'`, `RUN='^(TestXML2|TestLegacyXPathTable)'` and `RUN='^TestXSLT'`.
Ownership/cancellation units admit actual private work and may also run with
`RACE=1`; immutable results must never retain execution scratch.
