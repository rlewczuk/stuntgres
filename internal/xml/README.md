# XML parser, XPath evaluator and XSLT runtime

`Parse(context.Context, string, Options)` is the concrete, handwritten XML
DOCUMENT/CONTENT validator. `Content` permits empty values, text and multiple
roots; a leading DOCTYPE after legal declaration/misc selects document grammar
while retaining CONTENT error classification. `Document` requires one root with
legal prolog/epilog. The scanner uses fixed XML 1.0 fifth-edition names and
characters, not host Unicode categories. Version compatibility does not change
those character ranges.

`IsDocument` classifies accepted characters independently of their input mode;
`IsWellFormed` suppresses recoverable parse failures only. `XMLExists` and
`StartTable` share the operation-local DOCUMENT/options0 parser policy: namespace
and `xml:id` validity failures are fatal, ordinary DTD default attributes are absent,
default namespace bindings remain effective, and entity boundaries survive.
Validation and serialization keep their own existing policies; no operation
changes another call's accepted value, warnings or parser state.
The document-context memory-reader entry owns its initial UTF8 BOM handling and
single empty-document diagnostic; type input retains its distinct entry rules.

`XPathScalar` owns xml2's distinct raw-memory DOCUMENT operation. It expands
entity markup, omits DTD defaults and starts at the actual root element while
retaining its document parent for absolute paths. Namespace and validity events
are recorded without rejecting an otherwise usable LEGACY tree; grammar,
conversion and prior fatality remain authoritative. String conversion compiles
the literal `string(path)` wrapper. Structured document/evaluation failures
project to NULL/NULL/false; compile failures retain every ordered diagnostic,
including warnings, in `LegacyXPathError`. Number narrows once to float32 and
maps NaN to NULL. Ordinary cancellation, size and invariant errors propagate.
`ValidateText` instead returns ordinary Text/current-mode validation with its
genuine warnings. Neither bridge changes core validation or retains a tree.

`LegacyXPathTable` uses the same private RawMemory DOCUMENT policy but owns one
ordered diagnostic-text accumulator for all source documents. Parser events and
nonempty tolerated evaluation diagnostics retain actual order and repetitions,
including terminal evaluation; a later compile error joins that prior text.
Empty evaluation markers add no message or separator. Evaluation remains
nonfatal and notice-free; cancellation takes priority before retention/clearing.
Literal pipe splitting consumes only the declared path width. Documents parse
before compilation; nodesets zip by row index. Strings repeat without driving
rows; number/boolean
results emit ordered notices even on the exhausted iteration. NULL/unusable
documents emit one all-NULL marker. Only nullable owned strings cross the bridge;
the callback row is borrowed until return. Trees, path frames and mutable event
buffers never escape or cross concurrent invocations. Destination type input and
statement publication belong to the engine, not this parser.

`XPathSet` shares that invocation-private raw-document/compiler/evaluator pipeline.
Its explicit operation distinguishes raw node dumping from string-value lists,
including empty separators. Raw dumps reuse serializer primitives without subtree
namespace reconciliation; attributes keep names/quotes, CDATA keeps markup, and
documents keep their UTF-8 declaration and top-level/trailing newlines. Literal
root/item tags are output work, not XML-name input. String XPath results bypass
wrappers; Boolean/number results return their real unsupported classification for
the SQL notice owner. Checked accumulation and cancellation publish no partial
text, and no legacy tree or declaration metadata survives a call.

`ProcessXSLT` owns one raw source reader, one independent raw stylesheet reader,
the namespace-aware XSLT compiler, transformation and direct result-tree saver.
Its `XSLTError` preserves the actual source/style/compile/apply stage and one
ordered LEGACY diagnostic accumulator, including callbacks from earlier usable
readers. The private XSLT reader may finalize an actually completed grammar tree
after a converter fault; it does not publish a valid strict `Result`, erase
callbacks, reparse a prefix or change any other operation's byte acceptance.
Original BOM-only diagnostic windows remain attached to that reader.
Transform ERROR state remains distinct from stopped XPath/termination/ordinary
failure: a failed transformation cannot publish a result, but genuine later
structured callbacks are not suppressed by an earlier generic runtime report.

Compiled declarations, true match-pattern branches, mode/name dispatch indexes,
lexical namespace/base/line metadata, output properties and resolved attribute
sets belong to the invocation. Attribute-set resolution merges real attribute
instructions and releases dependency edges, including recursive edges; runtime
application never traverses an unresolved dependency graph. Explicit
template/sequence/result-target continuations own local scopes and parameters.
Built-in child continuations retain original text/CDATA boundaries and own an
invocation-local ambient XPath focus. Elements and matched ordinary text,
comments and PIs update that focus; CDATA and unmatched leaves inherit it.
Nested default traversal restores its actual caller's focus before the next
sibling. Explicit `apply-templates` keeps its separate selected-list positions.
Stylesheet bindings and static/dynamic constructors share lexical QName
validation. SQL parameter names retain their separate released splitting and
expansion rules; a leading-colon name does not alias an unqualified declaration.
Globals have separate dependency state: each eager select evaluation retains its
own ordered XPath callbacks, and a failed value remains uncomputed for later
global evaluations. Supplied parameter expressions see earlier supplied values,
not stylesheet globals; a declared variable ignores a supplied override before
its expression is compiled. Named decimal formats retain their first declaration.
The optional XPath environment supplies instruction-current nodes, expanded-name
bindings, distinct result tree fragments and XSLT functions.
Every projected node has a real document owner; document order and generated
identity are invocation-local. Transform document registrations own lazy key
indexes separately from XPath ownership, including the stylesheet document-info
fallback. The nil environment retains the ordinary XPath/scalar/table contract.
The compiler preprocesses the actual stylesheet document visible through
`document('')`, retaining foreign top-level data while stripping stylesheet-only
nodes and splicing real `xsl:text` children. Separate instructions retain their
owned language metadata; no alternate projection or reparsed source is used.
Source whitespace stripping uses compiled strip/preserve name rules and their
specificity/order, independently of source `xml:space`; stylesheet whitespace
preprocessing retains its separate `xml:space` policy. Match grammar and balanced
XPath predicate compilation are separate stages: generic pattern errors add no
structured Detail, while real predicate/select callbacks and earlier reader
diagnostics remain ordered and intact.

Only genuine in-memory document identities are readable during transformation.
External includes/imports parse the entity loader's actual empty input; runtime
file/network loading is denied before I/O. There is no filesystem, network,
subprocess, plugin or native-library transform path. Source/result nodes are
never shared mutably. The ordered result builder fixes namespaces, replaces
attributes by expanded name and retains text-only disable-output-escaping facts.
Namespace preprocessing relocates locally excluded prefixed declarations to the
stylesheet root before inherited result namespaces are collected. Root exclusions
filter that inherited set; local exclusions are not blanket result-time URI
filters. Namespace aliases replace namespace URIs while preserving literal
prefixes, with subsequent result-name fixup owning any necessary bindings.
The saver consumes that tree directly, with merged XML/HTML/text properties and
intrinsic encoders; it never reparses through typed XML serialization.
`XSLTResult.Text` owns the complete immutable datum, including embedded NUL.
Empty success is non-NULL; ordinary cancellation, growth and invariant failures
are errors, not a partial result or a fabricated NULL. No compiled program,
context, document, key index, encoder state or mutable buffer escapes the call.

XPath compilation retains immutable syntax only. Invocation-private frames
project seven node kinds with stable owner/namespace identity, document order,
all axes and axis-ordered predicates. Namespace axes retain explicit empty default
bindings as owner-specific nodes: the nearest declaration masks ancestor bindings,
and descendants inherit the empty binding with their own namespace-node identity.
Entity replacement contributes to element string-values without entering navigation
axes. Typed node-set, string, number
and boolean results share the XPath 1.0 operators, conversions and core function
library. Numeric scanning and formatting reproduce the release provider's bounded
fraction/exponent scanner and fifteen-digit formatting rules. Prefixes are query
bindings, not document declarations; `xml` is predefined. Evaluation checks
cancellation and checked buffer growth and retains
no tree, context or scratch in `Result` or compiled expressions. `XPathError`
distinguishes empty, compilation and evaluation failures; `XPathContextError`
retains the strict XML parser diagnostic separately from XML input failures.
The common scanner distinguishes EOF inside either quoted literal as
`Unfinished literal`, rather than a generic grammar failure; ordinary context
cancellation during scanning keeps priority over compile diagnostics.

`XPath` uses that same complete evaluator for scalar `xpath`/`xpath_exists`
invocations. Namespace pairs register only after strict document projection;
duplicate aliases replace earlier bindings and `xml` remains predefined.
Qualified names never match an empty namespace URI. Cardinality-only evaluation
does not serialize nodes. Result envelopes own XML-content spellings or a raw
numeric scalar; SQL float presentation belongs to the caller. Text/attribute
escaping and namespace cast spelling are distinct from subtree saving.
Root-node conversion prepends `<?xml version="1.0" encoding="UTF-8"?>` and LF,
then uses the subtree saver’s document-child separators and trailing LF, whether
or not the input had a declaration. This is XPath-only; XMLTABLE, character
serialization and typed-output declaration policies remain independent.

`Table` owns one relational invocation's tree, namespace bindings, compiled row
and column paths, and current row. Column filters compile before the first row
evaluation. `Column` distinguishes a missing node-set from an empty scalar and
serializes requested node sequences using the common cancellation-aware saver,
reconciling inherited namespaces without mutating source nodes. SQL target input,
DEFAULT demand and column constraints remain in the SQL adapter/source owner.

`Options.InputKind` has three distinct contracts. `Text` is already-decoded
UTF8: declarations never reinterpret its characters. PostgreSQL-codec `Binary`
preflight captures an encoding before later declaration errors, resolves its
PostgreSQL identity and uses only direct `pgencoding18` kernels, verifying the
whole source before conversion. DOCUMENT text handler recognition is separate
immutable metadata and adds no Binary codecs.

`RawMemory` accepts byte strings only with `Mode: Document`. It starts in UTF8,
consumes one initial UTF8 BOM, and selects a decoder at the completed valid quoted
encoding field in the same DOCUMENT declaration production. Selection can
override UTF8 BOM detection, preserves earlier declaration events, and precedes
later standalone/close errors. Only unread bytes are decoded; the same scanner
continues over the final character spelling. Malformed declaration recovery
retains its pointer-only skip and independently tracked character column.
ASCII validates every unread physical byte as genuine sevenbit, including after
NUL or a completed root. Character references remain XML spelling, so `&#233;`
can collect é while literal UTF8 bytes under ASCII fail atomically. The validated
identity spelling shares immutable backing. High bytes return `RawInputError`
with only actual prefix grammar diagnostics, never fabricated iconv callbacks.

Physical EOF genuine partial ISO-2022-JP designation prefixes remain buffered
without producing a character: ESC alone or followed by `(` or `$`, and a lone
JIS lead in the selected double-byte state. Illegal bytes do not become buffered
merely because they follow ESC near EOF. Grammar must still accept the entire
decoded document. No reset, replacement character, valid-prefix publication or
MIME line restriction is introduced. Unknown escapes fall through as literal
ESC for XML validation; true conversion faults remain distinct failures.
Sizing and emission retain the same full physical input for decoder lookahead.
Emission stops at the sized consumption boundary without treating that boundary
as EOF: a literal ESC produced before a later fault remains in the decoded
prefix for grammar diagnostics. Genuine incomplete physical tails still emit
no character. Replay does not retry the known conversion fault or add decoder
events; parser continuation and failure-atomic publication remain separate.

ISO-2022-JP-2 has invocation-private independent G0 and G2 designations. G0 owns
ASCII, Roman, Kana, JIS0208, JIS0212, GB2312 and KSC5601; G2 owns Latin1 or Greek.
SS2 emits exactly one scalar and retains both sets. LF changes neither set; no
MIME line-width or final-ASCII restriction applies. Any physical ESC suffix with
fewer than three bytes is buffered, including ESC-X; ESC-$-( needs four bytes.
Pair EOF ordering is set-specific: JIS0212/GB reject an invalid first lead before
checking availability, whereas KSC checks availability first. Complete unknown
escapes still emit literal ESC for grammar validation. Every emission replay
keeps full original lookahead, and a real failed-buffer retry retains both sets.

ISO-2022-KR selects ASCII or KSC5601 with SI/SO; its designation is
nonselecting and SO works without a prior announcement. ISO-2022-CN keeps the
active set separate from the latest GB2312/CNS1 SO announcement; designating a
new set changes the next SO, not the current set. Unannounced SO selects GB.
SS2 reads one CNS2 pair without changing either state, and the H designation
changes neither. Neither family resets at LF. Framing and physical-EOF
availability ordering belong to these raw handlers, not PostgreSQL verifiers.
Their immutable GB/KSC/CNS mappings are exhaustively qualified; CNS1 adds only
three independently licensed source facts documented in `data/README.md`.

UTF-7 owns direct characters, shifted Base64 and big-endian UTF16, including
supplementary pairs. Invalid units are never replaced. Ordinary malformed-bit
or high-surrogate errors retain the current sextet boundary; chained lone-low
rejection instead restores the last emitted internal-character cursor and full
pending state. That checkpoint can precede a previous zero-output terminator:
`+AOk-+3AA-` faults at byte4, not at the second plus. Real failed-buffer retry
retains this state. Physical EOF does not terminate or flush a shift: a lone
plus is one buffered byte, whereas `+A`, `+AA` and `+2AA` consume all bytes
without emitting a character. Actual following markup remains real lookahead.


Executable RawMemory labels use ASCII case-insensitivity, without trimming,
punctuation removal, PostgreSQL aliases or ICU fuzzy matching:

| Labels | Effective codec |
| --- | --- |
| UTF8, UTF-8 | UTF8 |
| ASCII, US-ASCII | ASCII |
| ISO-2022-JP-2, ISO2022JP2, CSISO2022JP2 | ISO-2022-JP-2 |
| ISO-2022-JP, ISO2022JP, CSISO2022JP, ISO_2022_JP | ISO-2022-JP |
| ISO-2022-KR | ISO-2022-KR |
| ISO-2022-CN | ISO-2022-CN |
| UTF-7 | UTF-7 |
| EUC_JP, EUC-JP; EUC_CN; EUC_KR; EUC_TW | corresponding PostgreSQL direct codec |
| EUC-JISX0213; SHIFT_JISX0213 | EUC_JIS_2004; SHIFT_JIS_2004 |
| LATIN1–LATIN10 | corresponding PostgreSQL direct codec |
| ISO-8859-1/2/3/4/9/10/13/14/15/16 | LATIN1/2/3/4/5/6/7/8/9/10 |
| WINDOWS-1250–WINDOWS-1258 | corresponding WIN1250–WIN1258 codec |
| CP866; CP874 | WIN866; WIN874 |
| ISO_8859_5/6/7/8, ISO-8859-5/6/7/8 | corresponding ISO_8859 codec |
| KOI8R, KOI8-R; KOI8U | KOI8R; KOI8U |
| SJIS, SHIFT_JIS; BIG5; GBK; UHC; GB18030; JOHAB | corresponding direct codec |

Ranges above enumerate finite decimal suffixes, not wildcard aliases. The
39 nonUTF8 direct identities reuse their existing mappings, including both words
of combined JIS2004 mappings. Genuine ISO-2022-JP owns ASCII, Roman yen/overline
and JIS0208 designations and its complete independently licensed forward table.
JP2 reuses that JIS0208 table, an independently licensed complete JIS0212 forward
table, and exhaustively qualified existing GB2312/KSC5601/Greek mapping-only
lookups. JIS0212's sole qualified source correction is documented in
[`data/README.md`](data/README.md); no duplicate GB/KSC/Greek tables or runtime
external decoder are introduced.
Raw JOHAB admits its two-byte Hangul/symbol framing before consulting the
existing immutable PostgreSQL Unicode mappings via `pgencoding18.LookupUTF8`.
This is separate from the PostgreSQL byte-string verifier: raw `88 61` decodes
as U+AC00 and raw `8F 41` as U+B028, each consuming two bytes. Valid framing does
not admit an unmapped cell. Symbol subranges retain the D9/DA/DE restrictions,
and invalid/truncated frames fail without repair. PostgreSQL `Verify`, `Length`,
Binary input and all old callers retain their own contracts.
Recognition alone never authorizes decoding: unknown/rejected labels fail,
recognized labels outside this admitted set report unavailable capability.
Canonical KR/CN/UTF-7 labels and their ASCII case variants are executable.
Other same-family aliases require whole-handler qualification before admission;
CN-EXT and IMAP UTF-7 are distinct handlers, not fallback spellings.
RawMemory introduces no SQL function, XPath policy, stylesheet transformation,
transport or output-encoding change; existing Text/Binary consumers are unchanged.

## Representation and errors

`Result` contains the immutable original **character spelling**, requested and
effective modes, declaration metadata, ordered warnings and a validity bit. The
original binary transport is decoded, not retained. No DOM, parser, open-element
stack, context, declaration table or conversion scratch survives in Result.
The private `parseExpanded` entry uses the same parser with a nullable transient
collector. Its operation-owned document preserves ordered element/text/CDATA/
comment/PI/DOCTYPE nodes, resolved names, namespace declarations, normalized
explicit/default attributes and unresolved entity-reference identity. It is not
retained by Result. Expanded entity occurrences own their nodes. Preserved
references have distinct boundaries and may share immutable replacement
contributions within that operation; navigation never enters those contributions.
Immutable input strings and unchanged node values safely share backing storage.
Adjacent text is coalesced once; temporary text-part slices are cleared before
publication rather than retained as mutable collector scratch.
Literal source collection normalizes CR/CRLF line endings; already-decoded
character references append their scalar unchanged, including U+000D. Numeric
references decoded eagerly in an EntityValue instead become replacement input
and follow literal-source normalization when that replacement is parsed.
PI completion retains the grammar's target and data-start endpoint, never
rescans separator whitespace, and skips collector-only work in validation mode.
`Diagnostics()` returns an independent slice; its strings are immutable. A zero
Result is not accepted empty CONTENT and cannot be output. All failures return
zero Result and no partial transient document.

`RawInput()` returns immutable `RawInputInfo`: effective codec, default selection,
initial UTF8 BOM presence and the exact unconsumed physical EOF byte count.
The written label stays in `Declaration.Encoding`; `Original()` is decoded,
BOM-less, unexpanded character spelling, never the superseded transport.
`RawInputError` distinguishes unknown label, unavailable codec, conversion fault
and empty input. It owns declaration/ordered diagnostics and, on conversion
fault, the cause, zero-based original raw-byte offset (including BOM) and at most
four actual offending bytes. Empty input has no fabricated provider callback.
Grammar errors remain `ParseError`. Encoding and I/O conversion diagnostics have
no XML source coordinate. Parser `Offset` addresses the full decoded spelling;
after a converter switch `SourceBaseOffset` is the completed quote's exclusive
index, the lower bound of the replacement buffer's context window, not an offset
subtraction. Default/UTF8 input keeps its ordinary source window. Entity frames
start at their own zero and restore the parent's switched window on return.
Contexts retain literal control bytes; visible terminal rendering is not a
byte-exact context record. Failed-buffer retry events stay ordered, without
deduplication. Source windows/carets and offsets are source-derived provenance,
separate from callback domain/condition/severity/line/column/text observations.
All failures publish zero Result and no tree, even when a converted prefix was
consumed for the reader's premature-end continuation.
At physical EOF, conversion previews contain at most four actual remaining
bytes and, if shorter, one terminating `0x00`. They never read allocator residue
past that terminator or invent padding; `RawInputError.Bytes` contains actual
input only. A genuine conversion fault remains an error even after a complete
root. The pinned reader can return a nonnil document pointer alongside fatal
conversion callbacks; pointer existence is not this package's acceptance rule.
Incomplete buffered ISO input with no conversion fault is a separate case and
can still follow a completed document successfully.

`ConstructText`, `ConstructComment`, `ConstructConcat`, `ConstructElement`,
`ConstructForest`, `ConstructPI` and `ConstructRoot` publish genuine generated
values without input revalidation. Validity is value provenance, not a promise
of well-formed characters: document qualification still reparses independently.
Construction retains no tree, recipe or environment. Escape profiles and legacy
identifier ranges are distinct from input grammar and serializer escaping.
`UnmapName` is a presentation-only, single-pass inverse for exactly four hex
digits after lowercase `_x`; it supplies neither input nor construction authority.
Concat/root consume original declaration/body characters; typed output owns its
separate normalization and warnings. All construction failures publish zero.
Both join passes poll outer fragments, including empty fragments; emission cannot
skip cancellation merely because a fragment contributes no characters.

`RowMapping` is a single-invocation checked row assembler: it maps raw SQL
identifiers once, appends already typed-output field characters into one bounded
buffer, and publishes immutable CONTENT only on successful Finish. NULL skips,
zero-width rows, growth copies and final publication remain cancellable.
Namespace spelling is literal generated data, not parser admission.
`NewScopeMapping` extends that same buffer with checked schema/table nesting;
only its outer root owns namespace declarations and combined-schema links.
Scope roots remain present for table forests, including empty members.
`ConstructMappingSchema` and `ConstructScopeMappingSchema` own the released XSD
templates and return opaque `MappingSchemaResult` authority. Scope templates
emit their own complex type without recursively defining referenced members.
Only that token may supply an embedded schema; exposing its immutable Result for
typed adoption cannot mint another token. Schema facts contain ordered SQL type
identities, never SQL callbacks or a DOM.


`Declaration` distinguishes absent and present-empty encoding/version properties,
retains written labels and has an exclusive UTF8-original byte `End`. CONTENT's
custom declaration is stripped only from transient scanner input. Its diagnostic
`SourceBaseOffset` identifies that origin; no textual fragment wrapper is made.
DOCUMENT's explicit UTF8 parser entry does not auto-detect or strip an initial
UTF8 BOM: its required-root check rejects it at the original byte zero. CONTENT
can retain a BOM as text; a later declaration is still not a byte0 declaration.
DOCUMENT's post-encoding separator follows the parser's input-encoding assignment,
not the next declaration keyword or the mere presence of an encoding field.
A complete recognized non-special handler label requires whitespace before any
following token, except the immediate `?>` close. Case-insensitive `UTF8`, `UTF-8`,
`UTF16` and `UTF-16` set separate declaration state and do not enable that check;
malformed or incomplete labels do not assign input encoding, and unsupported
handlers terminate before it. Written metadata remains separate from this
production state; CONTENT custom declarations and binary preflight retain their
own recognition and error precedence.
An incomplete DOCUMENT declaration at physical input end still reaches the
required-root diagnostic. Embedded NUL and ordinary operation errors retain their
distinct termination behavior.

`ParseError` distinguishes custom declaration failures from grammar errors and
retains requested/effective modes, declaration and ordered `Diagnostic` values.
Conditions are libxml symbolic identities with domain and severity. XML
line/character-column and source-local byte offset are separate from the byte-based
80-byte context/caret; contexts can contain split or malformed UTF8 bytes.
`Diagnostic.Source` distinguishes original character input from entity replacement
input; `EntityName` identifies a replacement. Offset and SourceBaseOffset belong
to that source, not an invented original-document position. Invoking failures are
separate later events in the parent's source.
`Detail()` joins only error/fatal blocks without a terminal newline; general
warnings remain separately observable. Type-input and serializer policies filter
namespace-domain failures; XPath's strict policy records them at declaration and
name-resolution sites and treats them as errors.
Strict-context `xml:id` validation emits its DTD-domain error at start-tag
completion after all attributes and namespaces, independently of XPath demand.
Its release NCName character classes
remain separate from parser Name rules. Surrounding XML blanks are valid without
being removed from the collected attribute or ID index; no extra ID normalization
is performed. Type-input, well-formedness and serializer policies retain their
existing treatment of validity-domain diagnostics.
An ordinary lexical duplicate establishes ill-formedness only when it is the first prior
local-name match (before any equal-URI match); namespace declaration duplicates
do only when both declarations qualify for namespace collection.
No SQLSTATE, SQL query position or C source metadata is fabricated by this provider.
Reserved PI prefixes beginning with ASCII case-insensitive `xml` warn, except the
exact lowercase W3C targets `xml-stylesheet` and `xml-model`; exact `xml` variants
remain fatal. Actual `xml:space` attributes warn unless their constructed value is
`default` or `preserve`. Those warnings follow normalized/reference contributions,
including CONTENT's deferred ampersand spelling and partial or NULL value returns,
not raw source matching. Actual `xml:space` values reuse the existing lazy capture;
unchanged spans share source storage and other ordinary values remain unbuffered.
DOCUMENT prolog catalog PIs preserve concrete syntax warnings after their closing
delimiter; CONTENT, element content and document epilog do not admit this branch.
Catalog syntax validation performs no external loading. Warning severity never
establishes ill-formedness or enters error Detail, but warnings retain their order
among errors if another production fails. PostgreSQL forwards warnings as notice
primary messages; that transport is distinct from this provider's Diagnostic API.
Character decoding is shared. Multibyte sequences with valid continuation bytes
and shortest form retain their computed value and full encoded width, including
encoded surrogates and values beyond Unicode. XML-invalid values emit a decoder
range diagnostic and remain invalid; no replacement character or successful
alternate decoding is admitted. Productions separately own their diagnostics and
recovery. An unconsumed value can produce another decoder event on reread:
CONTENT can report subsequent CharData events, while a failed DOCUMENT start tag
reaches epilog diagnostics. CharData reports and skips invalid values.
Malformed UTF8 reports once and enables single-byte continuation only for that
already-failed parse, so subsequent grammar errors remain observable. This state
belongs to the parser, survives production boundaries and cannot publish a Result.
A short incomplete terminal sequence instead leaves the cursor and decoder state
unchanged; production termination and content no-progress diagnostics distinguish
it from both malformed-byte continuation and NUL termination.
At a fragment production boundary, raw NUL ends continuation of an already
ill-formed parse without becoming another CharData scalar. Warnings alone do not
qualify, and clean input still cannot publish a successful prefix before NUL.
Comments, complex attributes, PI data and CDATA share a normalized reporting
read: CR returns LF, and CRLF moves the raw cursor onto LF without advancing
tracked line/column. Cached consumption then tracks that raw LF exactly once;
lone CR retains source-byte column tracking. The same rule applies after
failed-parser single-byte decoding, without changing Original or raw contexts.
PI reads its next character before checking constructed length, so the read's
cursor mutation and decoder events precede the length diagnostic.
CDATA retains two consumed lookahead characters. Initial lookahead failure and
invalid current/rolling input return unconsumed to CONTENT with distinct production
messages. Its next normalized read also precedes the committed-length check.
Later failure previews at most 50 bytes of committed, line-normalized payload,
excluding both retained characters. Only failure reconstructs that bounded
preview; successful validation does not allocate a payload buffer. Overlength
retains the separate release CDATA recovery state: stale ASCII CharData cannot
commit its ordinary text cursor, while an initial lone CR or non-ASCII character
selects complex CharData and consumes normally, including stray-terminator recovery.
Name/NCName length errors use the completed production: only a wholly ASCII name
with a positive ASCII terminator reports at its uncommitted start. Non-ASCII
boundaries and terminal input select the complex consumed cursor. QName component
limits remain independent; NmToken has its own incremental length check.
Complex Name/NCName and NmToken use the same normalized reporting read, including
at a CRLF terminator before a length diagnostic. Their consumed spelling endpoint
remains separate from that reporting cursor, so skipped CR never becomes part of
a returned name or QName component. Raw Name/NCName acceleration requires an
actual initial ASCII NameStart (excluding colon for NCName); invalid ASCII starts
perform the complex initial normalized read too. Eligible wholly ASCII names
retain their raw terminator and rewind behavior.
QName recovery follows each component's null return: an initial failed NCName
at a colon retries ordinary Name from that colon. A failed local-name/NmToken
retains the prefix through its colon; a failed Name after a second colon retains
both successful components through that colon. Immutable source endpoints exclude
consumed failed payload while preserving the actual recovery cursor. Discontinuous
recovery assembles only surviving prefix/token pieces with checked, cancellable
copies; contiguous names share source storage. Only parser
termination or ordinary errors prevent these recoveries, not physical input end.
Lexical names remain the identity for element matching. Ordinary attribute
identity combines actual prefix/local spelling with ordered namespace resolution.
Missing-value, unfinished-start-tag, mismatched-end-tag and premature-end
diagnostics use each production's source-local spelling. A recovered name at
physical input end still requires a tag terminator. Closing tags use the concrete
NameAndCompare production for unprefixed openings and QNameAndCompare for prefixed
openings: successful delimiter-bounded comparisons advance tracking by matched
bytes, while fallbacks retain ordinary Name/QName scanning and null names render
as `unparsable`. Missing end delimiters still permit the mismatch diagnostic and
return to surrounding content/epilog recovery. Comparisons poll in bounded chunks,
including when their speculative cursor has not yet been committed.
The first attribute attempt follows optional whitespace. Every outcome shares the
same delimiter-before-separator continuation; missing values do not fabricate an
unfinished tag when `>` or `/>` is already present. Complex attribute character or
termination errors can retain a constructed-value status for duplicate checking
without allocating ordinary value payloads. Ordinary attributes and namespace
declarations have separate per-start-tag collectors. Ordinary checks follow the
complete attribute loop, including any final separator error, before start-tag
termination. The earliest prior equal prefix/local or equal local/non-NULL URI
terminates each check: only the former emits a parser-domain duplicate. Earliest
indices keyed by lexical and expanded identity preserve this order without
repeated pairwise scans. Admitted namespace duplicates are diagnosed inline and
leave the first binding unchanged.
Declarations of `xmlns:xml` or `xmlns:xmlns`, reserved XML/XMLNS namespace URIs,
and empty prefixed namespace URIs never enter that collection. Empty default
declarations do; relative and invalid other URIs also remain collected. Admission
streams emptiness and reserved-URI equality facts from normalized attribute
characters: literal whitespace becomes spaces, while numeric references retain
their scalar values. Operation-only prefix bindings support inherited scope,
shadowing and unwind on every element return. All declarations precede ordinary
resolution, even when written after attributes. Unprefixed attributes have no URI
regardless of the default namespace; undefined prefixes are not equal-URI matches.
The implicit `xml` binding cannot be overridden. Normalized URI interning hashes
during scanning and confirms equality in cancellable bounded chunks; numeric IDs
avoid rehashing long URI payloads in attribute keys. URI values share source
storage only while every retained contribution is unchanged and contiguous.
References contributing no scalar never extend the retained source endpoint;
a later contribution across that gap starts the existing lazy normalized buffer.
Scalar ampersand comparison preserves equality because the release's deferred
ampersand escaping is injective. Attribute construction counts that escape
separately: CONTENT ampersand references contribute five bytes (`&#38;`), while
DOCUMENT replacement contributes one, for both numeric and predefined references.
Canonical URI bytes never substitute for this construction-length counter.
No URI syntax normalization, trimming or whitespace collapse occurs.
Result retains no namespace environment, tree or URI buffers.
Duplicate wording retains lexical prefixes.
The common attribute boundary owns the post-value input-growth check; committing
a closing quote does not add a second check before the tag delimiter. Fragment
input-window failure therefore retains the surrounding production's actual cursor.
Complex attribute iterations commit a contribution, check input growth, read the
next character through the reporting decoder, and only then check constructed
length. The next iteration reuses that read, including its original contribution
start and the shared read-time normalization. URI capture ends at the committed
contribution before the next read, so its cursor normalization cannot insert the
following CR into the preceding contribution.
Decoder events can precede an overlength diagnostic.
Complex overlength retains the subsequent memory-error
event and ordinary start-tag recovery; the ASCII accelerator retains its distinct
opening-quote reporting. A complex value's input-growth halt exposes empty input
to the common attribute continuation while preserving original offsets and
line/column. Its subsequent recovery contexts are empty, not stale source bytes.
Comments retain the ASCII accelerator's transition into a separate complex
production at non-ASCII characters, lone CR or controls. Its consumed `q`/`r`
lookahead and current reporting cursor determine diagnostics, including the
second-lookahead invalid-character branch's retained value. ASCII double-hyphen
events distinguish no buffered payload from a buffered preview whose displayed
newlines trim to empty. Buffered previews bound 50 payload bytes separately from
the printed opener; complex double-hyphen events use the fixed production message.
NUL termination distinguishes initial lookahead from the later committed preview.
ASCII comment segments scan completely before checking their accumulated payload
length. The reporting byte cursor retains the last committed segment origin while
line/column reflect the complete local scan. Hyphens join the following segment;
CRLF restarts at LF, excluding CR from the constructed count. Comment and PI length
failures return to their surrounding grammar, preserving subsequent diagnostics;
they do not acquire CDATA's distinct stale-state recovery.
In comments, attributes and PI data, NUL terminates the production's source
without a PCDATA-invalid-zero event. An already-retained complex-comment double
hyphen is still reported after a malformed-byte decoding event.

`EncodingError` distinguishes invalid label, missing direct pair, invalid sequence
and untranslatable character, exposing canonical IDs/names, bytes and English
message/detail. `SizeError` distinguishes conversion from append overflow.
DOCTYPE and the complete internal ELEMENT/ATTLIST/ENTITY/NOTATION grammar are
nonvalidating: unused declarations are syntax-checked, but root/content-model,
ID, required-attribute, enumeration-membership and FIXED-equality validity are
not enforced. First entity/attribute definitions bind; later declarations are
still parsed. The transient DOCTYPE retains admitted declaration order/identity,
canonical grammar models, attribute default kinds and original EntityValues.
Processed replacement values remain distinct from original declaration spelling:
collector projections consume the former, while declaration serialization uses
the latter.
External general entities require whitespace before `NDATA` after their SYSTEM
or PUBLIC identifier, even when unused. A missing separator reports a syntax
error and continues parsing the NDATA clause and declaration terminator; an
undeclared notation remains a deliberately unenforced validity constraint.
Unparsed identity is explicit, independent of the notation string. A properly
separated NDATA clause may retain an absent name as empty and succeed unused;
content and attribute references still fail as unparsed, not empty external data.
ENTITY's initial, percent and successful-name separator errors continue through
the remaining declaration syntax; its absent-Name branch still returns.
ELEMENT/NOTATION retain their distinct initial-error returns. The pinned percent
separator diagnostic contains two literal percent signs (`'%%'`).
In contrast, a missing final ENTITY terminator reports at the original failure
cursor and stops parsing. Subset/DOCTYPE/document callers do not append recovery
errors after that halt; earlier genuine separator diagnostics remain ordered.

General entities parse balanced replacement content with the same element/text
grammar and isolated open-element/source frames. Parameter entities are resumable
DTD inputs, with semantic boundary whitespace, placement/input-identity checks
and eager declaration-value timing. Defaults expand at declaration time; literal
attribute whitespace becomes spaces, direct numeric references retain their scalar,
and tokenized attributes collapse U+0020 only. Defaults bind namespaces before
ordinary attribute resolution and follow explicit attributes in declaration order.
Missing REQUIRED/IMPLIED attributes remain absent; explicit values override FIXED.
String-based PE lookup honors `standalone="yes"` even after earlier PE use.
Missing lookups retain the release's reference count, suppressed-warning/error
bookkeeping, density checks and PE-seen update; a fatal lookup alone does not
short-circuit the remaining declaration-value string.
Fatal missing string-GE lookup likewise consumes the valid reference, preserves
lookup/count and both nil checks, then continues through later references unless
accounting or an ordinary error stops it; it is not a generic decoder halt.
GE/PE string references and raw EntityValue reference prechecking share a
50000-byte XML Name scanner. Its pinned overlength label is `NCName`, without
tightening the Name grammar. A failed scan leaves the local pointer at Name
begin and retains the active input's source context. String-reference callers
report their own missing-Name diagnostic, check nil and decode residual bytes;
malformed tokens perform no lookup/count/PE-seen update. Missing semicolons leave
the failed delimiter unconsumed. Raw EntityValue prechecking instead reports its
reference-character error and returns. Input-based Name/PE scanning stays distinct.

Recursive GE/PE result copies check density only at semantic buffer growth, not
at completed replacement size. Each decoder starts at capacity300; ordinary,
numeric/predefined scalar and reference contributions all track byte capacity,
growing to twice the previous capacity plus100. Recursive-copy checkpoints from
an empty buffer are201,601,1401,3001, not additive100-byte intervals. These checks
still run after syntax failures. The outer attribute/default copy itself adds no
recursive gate. Validation tracks the same capacity history without disposable
expansion buffers; physical Go builder capacity need not mirror libxml's buffer.
The string decoder derives content presence separately from entity type: an
internal empty entity has nonNULL empty content, a named unparsed entity has its
notation text as nonNULL content, and parsed external or notation-absent unparsed
entities have NULL content. Eligible nonNULL content is inspected and recursively
counted even after an unparsed-reference diagnostic; raw declaration identity
and the fatal unparsed restriction are unchanged.

For NULL GE content, the local decoder retains immutable `&`, Name and `;`
pieces without loading anything. It reserves after the ampersand with increment
Name byte length plus100, with no ordinary growth before or after that reserve.
Strict reserve equality can leave the current size one byte past the usual
threshold: the next nonempty recursive copy checks its next actual byte, never
a past checkpoint; empty copies perform no growth/check. These private strings
after failed lookups are not successful external-resource values or published
XML. Ordinary external content remains empty and failures publish neither Result
nor transient tree.

All external SYSTEM/PUBLIC subsets and general/parameter entities are intrinsically
empty. There is no loader interface, fetch option, filesystem/network/catalog
lookup or host executable dispatch. External entities in attributes and unparsed
entity uses remain errors. Suppressible direct undeclared references retain an
entity-reference node; declared external general entities contribute no nodes.
Nested general-entity contexts do not inherit external-subset/PE/standalone
leniency. Entity counts, local density, copy volume, input/decode depth, names,
identifier and construct lengths use the pinned release accounting, independently
of whether collection is enabled. SQL adapters remain separate owners.
Replacement-copy volume charges source byte length plus five on first and cached
nonempty GE publication, in document and balanced replacement contexts and with
or without collection. Single-text ownership does not exempt the first use;
childless/empty replacement lists return before this charge. The pinned
million-byte entity accepts ten references and rejects at the eleventh.
The no-size/nil-entity accounting branch applies above 10000 references to
both fatal undeclared errors and suppressed undeclared warnings. Replacing
a prior fatal with that warning does not exempt the count limit; at exactly
10000 this branch still admits either code.
Input-based PE lookups record the suppressed warning before this check too;
outward suppression never discards its accounting identity. Real-entry external
subset tests cover acceptance at 10000 and the sole loop diagnostic at 10001.
Entity-string chunks align valid UTF8 within at most three boundary bytes and
always advance. Already-failed single-byte input progresses without clearing or
suppressing its original malformed-byte diagnostic.

## Character serialization

`Serialize(ctx, value, SerializeOptions{Mode, Indent}) (string, []Diagnostic, error)` converts
an accepted value to characters independently of its original parse mode. Zero
options mean CONTENT, NO INDENT: this returns the original immutable backing
without a parse or copy. DOCUMENT NO INDENT first qualifies using document grammar.
Diagnostics are detached events from this call's single qualification parse,
including events committed before a later failure. They never replay the value's
input diagnostics; CONTENT NO INDENT returns no events.
INDENT reparses the character spelling once with a transient collector and the
parse-time ignorable-blank policy, including inherited/defaulted `xml:space`,
DTD model classification and literal versus scalar-reference callbacks. ASCII
and complex character callbacks retain their parse-time lookahead and space-state
transitions; complex callbacks flush at 300 normalized bytes before coalescing.
Model lookup uses the actual bound node’s local identity, not its lexical QName.
DTD enumeration/NOTATION metadata retains first-admitted distinct members in order.
Collection-only per-attribute hash indexing admits members without rescanning the
ordered list. Token hashing and collision equality poll in bounded chunks; the
index is discarded when that attribute type finishes and never enters Result.

The operation-local saver emits ordinary XML, canonical DTD declarations and
automatic DOCTYPE-selected XHTML. It retains no nodes or formatting state in
Result. Two-space indentation is capped at 60 bytes; mixed subtrees suppress
layout. Requested/effective modes independently govern declaration eligibility,
Unicode escaping, top-level separators and terminal newlines. DOCUMENT strips
terminal CR/LF; DOCTYPE-promoted CONTENT retains the document saver’s final LF.
Notation records precede ordinary declarations in deterministic binding order.

`*NotDocumentError` has exactly `not an XML document` as its message, without
SQLSTATE, detail, hint or a fabricated parser diagnostic. Every structured
`*ParseError` from this call's qualification parse becomes `*NotDocumentError`,
for both DOCUMENT qualification and CONTENT INDENT, regardless of parse category.
Explicit `Parse` retains its structured `*ParseError`; CONTENT NO INDENT bypasses
qualification. Context, size, encoding and invalid-argument failures remain
ordinary errors, and qualification events are preserved independently.
Every failure returns empty output and leaves the accepted value unchanged.

Typed output is separate: `OutputText`/`OutputBinary` normalize declaration
transport and return a warning independently of character serialization.

`ConcatAccumulator` is execution-local accepted-value concatenation, independent
of input and output normalization. It preserves singleton identity, merges
declarations after each binary transition and appends bodies verbatim without
reparsing, including DTD-bearing bodies. The custom byte-zero reader's partial
metadata and ignored syntax-failure body boundary are local to this operation.
Constructed results use CONTENT metadata and retain no input diagnostics.
Body capacity grows geometrically with checked sizes: only an empty replacement
builder is grown, then the immutable existing prefix is copied with cancellation
polls at intervals of at most 4096 bytes before new bodies are appended. No write
can trigger an implicit unbounded prefix copy. Failed append states are poisoned
and cannot publish or accept later inputs. Headerless finalization shares the
existing body string without another allocation or copy; declaration-emitting
finalization alone constructs a prefixed string with the same bounded polling.
Both paths retain a final cancellation fence. Finalized strings and operand
backing remain immutable across later appends, including capacity expansion.


## Typed output and lifecycle

`OutputText(ctx, value)` normalizes a declaration for UTF8 typed XML output;
`OutputBinary(ctx, value, targetID)` uses an explicit canonical PostgreSQL target
and direct codec. `OutputBinaryCharacters(ctx, value, targetID)` uses the same
declaration formatting but leaves Unicode characters for the SQL adapter's
admitted client transport authority. All return `(characters, OutputWarning,
error)`; only `OutputBinary` encodes target bytes. A character conversion uses
`Serialize`, not typed-output normalization.

Each output operation reuses the custom byte0 declaration reader, scanning only
the prefix, never the body. Successful DOCUMENT declaration metadata does not
establish eligibility for normalization: DOCUMENT accepts some adjacent
encoding/standalone fields that the custom reader rejects. On custom-reader
failure, output preserves the entire original spelling and returns
`OutputWarning{Message: "could not parse XML declaration in stored value",
Detail: "Space required."}` for that missing separator. Other custom failures
retain their corresponding reader detail. This warning is not a libxml
`Diagnostic`, parse error or change to the retained Result. Its empty Message
denotes absence. Once formed, it remains observable even if later target
conversion or cancellation fails; such failures return empty characters and the
ordinary error. Cancellation before a completed custom failure returns no warning.
The caller owns delivery of each operation's warning; no callback or sink is retained.

Eligible declaration output retains a nondefault version or explicit standalone
and adds canonical encoding only for non-UTF8/non-SQL_ASCII targets. When no
declaration is emitted, one leading LF is removed, including when input had no
declaration. A failed custom declaration is not repaired or stripped, and its
preserved original still undergoes target conversion. Body markup, references and
spacing are never reparsed or pretty-printed.

All state is per operation. No locks, pools, mutable caches, native libraries,
external modules or runtime metadata files are used. Long scanner, validation,
counting, emission and copy loops poll cancellation with at most 4096 bytes of
progress plus one character/packed word between polls; phase ends and publication
also check context. Cancellation is an ordinary context error with zero result or
empty output, never a partial successful value. The caller supplies a nonnil
context. Construct/counter/copy sizes are checked before allocation or arithmetic;
noncursor entity decoding, token normalization, default insertion and collector
work poll independently of the source cursor. Validation does not construct
ordinary nodes or text/attribute payloads. DTD declaration/default tables and
namespace/value/catalog-PI captures are transient grammar state.

## Recognition and mapping inputs

`data/recognition.tsv` contains compact pinned handler names, compiled ICU
preferred resolution and independent finite recognition observations, not
conversion mappings. Its provenance, reproduction and licensing are in
[data/README.md](data/README.md). From the repository root, the offline stdlib-only
`go run ./internal/xml/generate` validates these inputs and deterministically emits
`declaration_names_generated.go` and the immutable `jis0208_generated.go` mapping.
The original Unicode JIS0208 input, Unicode license and independently qualified
single-cell raw-reader correction are documented in [data/README.md](data/README.md).
Runtime uses static switches and the immutable forward table, never source files,
native codecs, network or host mapping data.

## Testing

Package tests call actual parser/output/serialization functions and never issue
SQL or invoke a native parser. Expected grammar/output literals and ordered diagnostics precede
implementation and use release sources plus the pinned PostgreSQL18.6 UTF8/C
captures described with the metadata. Marked LF/empty-version output,
forward-compatible version/QName grammar and structured-column cases are
source-derived, not fresh observations. Typed declaration eligibility, output
warnings and DOCUMENT/CONTENT BOM boundaries have independent actual
typed-text/binary-send observations, distinct from raw character casts.

Legacy scalar/table policy, stage projection and invocation isolation use the
reusable `RUN='^(TestXML2|TestLegacyXPathTable)'` family, including real
parser/decoder/compiler/evaluator cancellation and strict-core contrasts.

XSLT compiler/pattern/template/XPath-environment/parameter/result/diagnostic,
no-I/O and admitted-phase cancellation/ownership units use
`RUN='^TestXSLT'`. They assert independently specified native values and real
private program/tree/scope state; public SQL compatibility belongs to the
dual-mode XML2 reference family. Concurrent ownership units are also suitable
for selected `RACE=1` execution. Collation and number-format helpers retain
their independent release-source/data provenance beside their implementations.

XMLTABLE invocation, namespace, node-output and cancellation units use
`RUN='^TestXMLTableProvider$'`.
Row/scope schema construction, poisoned failure and cancellation proof use
`RUN='^TestXMLMap(Rows|Scope)(Construction|Cancellation)$'`.
Projected XPath text nodes retain their invocation-local source kind: adjacent
CDATA still coalesces for XPath string values and cardinality, while XMLTABLE
XML output serializes CDATA through the common node serializer, including
delimiter splitting and cancellation. A local copy carries the coalesced value;
original source nodes, sibling invocations and retained XML stay unchanged.
Ordinary text remains escaped, and scalar/string XPath results do not acquire
CDATA markup. Mixed text/CDATA boundaries remain distinct XPath text nodes.

Production-specific invalid-character cascades, decoder range and failure-only
continuation, CDATA committed previews, short terminal sequences, ASCII comment
previews, name/construct limit recovery and complex-comment transition/lookahead
literals come from independent pinned PostgreSQL18.6 captures; condition identities
and structured columns also follow the matching Debian libxml productions.
Complex-comment cancellation uses the same admitted production polling checks as
other grammar; uncommitted segment scans and subsequent recovery remain cancellable.
The generator family validates immutable input and whole generated output;
recognition tests separately assert literal labels and all finite observations.
Complete KR/CN mapping/state and UTF-7 direct/UTF16, replay, diagnostics and
immutable lifecycle contracts use the reusable
`RUN='^TestXMLRaw(KRCN|UTF7)'` family. Its expected mappings come from original
licensed release inputs plus the three modern CNS facts, not candidate tables.
Raw-memory decoding, complete JIS0208 membership/values and holes, finite aliases,
ordered conversion/declaration errors, EOF provenance, DTD/no-I/O and real-phase
cancellation/ownership use the reusable `RUN='^TestXMLRaw'` family. Generator units
in that family compare all 8836 cells to the original licensed input plus the
literal independently observed correction, and reject corrupt/incomplete inputs.
Cancellation tests admit real private production/conversion phases; ownership
cases also support selected race execution.
The retained Result schema is checked explicitly; nested declaration, raw-input
metadata and diagnostic values are recursively restricted to immutable scalar
fields. The accessor-copied diagnostics slice is the sole admitted slice, not
permission to retain decoder scratch, pointers, maps, contexts or parser frames.
DTD units use independent pinned PostgreSQL18.6 UTF8/C diagnostics (the literal
`data/dtd-oracle.json` corpus) and shared-parser parsed-serialization projections.
The distinct XPath parse and unsuccessful `xmlsend` discovery query are not
expansion/default expectations. Internal DTD units never issue SQL. DTD source
frame identities, strict density comparisons, arithmetic boundaries, actual-phase
cancellation, immutable Result ownership and concurrent declaration isolation are
covered by the same family.
The captured mutual PE cycle generated with numeric percent references has a
permanent full-Detail/source-identity witness in both requested modes and both
collection settings. It is distinct from illegal raw-percent placement and the
accepted eager-declaration `q`-empty timing case. Literal reference line-ending
projections and long-separator PI completion/cancellation are also covered.

The 68-case runtime corpus is distinct from additional source-derived URI,
attribute-type recovery, default-value, storage and accounting witnesses; those
witnesses do not claim an oracle execution that was not performed.
Serialization’s independent literal corpus and exact diagnostic details live in
`data/serialize-oracle.json`, with reproduction/runtime provenance in its README.
Notation comparisons permit only the independently observed section permutations;
every declaration byte, count and section position remains asserted. Source-derived
extensions are explicitly labeled. Provider smoke calls real Parse/Serialize
operations, and lifecycle cases admit real parse, model, formatting, escaping and
copy phases without production hooks.


Follow-up pinned captures cover the string-PE standalone override and malformed
EntityValue bytes. The latter were delivered to genuine PostgreSQL binary XML
receive using binary COPY, not invalid UTF8 SQL text. The package's ordinary
Text-input regression exercises the shared parser's existing failed-byte contract;
it does not bypass Binary encoding preflight. A captured two-byte boundary scalar
projection is extended with source-derived three/four-byte alignments, tokenized
attributes and cancellation during valid and already-failed string decoding.

```sh
make check RUN='^TestXML(Core|Produce)'
make check RUN='^TestXMLDTD'
make check RUN='^TestXMLSerialize'
make check RUN='^Test(XMLPredicate|XMLXPath|XPath)'
```
