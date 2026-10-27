# XML character parser

`Parse(context.Context, string, Options)` is the concrete, handwritten XML
DOCUMENT/CONTENT validator. `Content` permits empty values, text and multiple
roots; `Document` requires one root with legal prolog/epilog. The scanner uses
fixed XML 1.0 fifth-edition names and characters, not host Unicode categories.
Version compatibility does not change those character ranges.

`Options.InputKind` distinguishes already-decoded `Text` from raw-byte `Binary`
strings. Text declarations never reinterpret the characters. Binary preflight
captures a declared PostgreSQL encoding before later declaration errors, resolves
its identity and uses only direct `pgencoding18` kernels. The entire source is
verified before conversion steps, with missing-pair precedence owned here rather
than inherited from SQL's conversion adapter. DOCUMENT text handler recognition
uses separate immutable recognition metadata; it adds no binary codecs.

## Representation and errors

`Result` contains the immutable original **character spelling**, requested and
effective modes, declaration metadata, ordered warnings and a validity bit. The
original binary transport is decoded, not retained. No DOM, parser, open-element
stack, context, attribute map or conversion scratch survives a call. Immutable
input strings and no-op outputs safely share backing storage. `Diagnostics()`
returns an independent slice; its strings are immutable. A zero Result is not
accepted empty CONTENT and cannot be output.

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
line/character-column and original byte offset are separate from the byte-based
80-byte context/caret; contexts can contain split or malformed UTF8 bytes.
`Detail()` joins only error/fatal blocks without a terminal newline; general
warnings remain separately observable. Namespace-domain failures do not establish
ill-formedness. An ordinary lexical duplicate does only when it is the first prior
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
`UnsupportedCapabilityError{Capability: DTD}` is the explicit DOCTYPE boundary,
including effective DOCUMENT promotion for leading CONTENT DOCTYPE. No external
entity loader or I/O exists. DTD semantics, pretty serialization and SQL adapters
belong to their own owners, not this package.

## Typed output and lifecycle

`OutputText(ctx, value)` normalizes a declaration for UTF8 typed XML output;
`OutputBinary(ctx, value, targetID)` uses an explicit canonical PostgreSQL target
and direct codec. Both return `(characters, OutputWarning, error)`. A character
cast uses `Original()` instead and produces no output warning.

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
context. Exact conversion/output sizes are checked before allocation; validation
counts normalized construct lengths without retaining expanded payloads, except
the transient namespace/value and eligible catalog-PI captures needed by grammar
diagnostics and duplicate admission.

## Recognition inputs

`data/recognition.tsv` contains compact pinned handler names, compiled ICU
preferred resolution and independent finite recognition observations, not
conversion mappings. Its provenance, reproduction and licensing are in
[data/README.md](data/README.md). From the repository root, the offline stdlib-only
`go run ./internal/xml/generate` validates these inputs and deterministically emits
`declaration_names_generated.go`. Runtime uses static switches only.

## Testing

Package tests call actual parser/output functions and never issue SQL or invoke a
native parser. Expected grammar/output literals and ordered diagnostics precede
implementation and use release sources plus the pinned PostgreSQL18.6 UTF8/C
captures described with the metadata. Marked LF/empty-version output,
forward-compatible version/QName grammar and structured-column cases are
source-derived, not fresh observations. Typed declaration eligibility, output
warnings and DOCUMENT/CONTENT BOM boundaries have independent actual
typed-text/binary-send observations, distinct from raw character casts.
Production-specific invalid-character cascades, decoder range and failure-only
continuation, CDATA committed previews, short terminal sequences, ASCII comment
previews, name/construct limit recovery and complex-comment transition/lookahead
literals come from independent pinned PostgreSQL18.6 captures; condition identities
and structured columns also follow the matching Debian libxml productions.
Complex-comment cancellation uses the same admitted production polling checks as
other grammar; uncommitted segment scans and subsequent recovery remain cancellable.
The generator family validates immutable input and whole generated output;
recognition tests separately assert literal labels and all finite observations.
Cancellation tests admit real private production/conversion phases; ownership
cases also support selected race execution.

```sh
make check RUN='^TestXMLCore'
```
