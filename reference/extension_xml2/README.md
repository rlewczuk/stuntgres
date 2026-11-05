# XML2 scalar and table reference scenarios

This package owns public SQL and native-control scenarios for the installed
`xml_valid(text)`, `xpath_string(text,text)`, `xpath_number(text,text)`,
`xpath_bool(text,text)` and five-TEXT `xpath_table` declarations. Independently specified results distinguish
XPath root-element context, scalar conversions, FLOAT4 narrowing, strict NULLs,
validation modes, document/compile/evaluation precedence and namespace handling.
Raw-encoding scenarios distinguish decoded-Text validation from genuine
declaration-aware document decoding, including genuine ASCII, ISO-2022-JP,
ISO-2022-JP-2 G0/G2 transitions, ISO-2022-KR/CN designation/SI/SO/SS2 and UTF-7
UTF16/shift state. Canonical KR/CN/UTF-7 labels and ASCII case variants use
independent source/style inputs, including CNS1 additions and CNS2 single shifts.
Conversion failures precede XPath compilation; scenarios assert full compile
diagnostics, strict typed NULLs and retained direct/prepared/held values.
Source and stylesheet-shaped documents are inspected through scalar XPath; these scenarios
do not claim native stylesheet compilation or transformation.
Catalog assertions select these five declarations rather than claiming parity
with the entire upstream extension inventory.

Scenarios share `reference/fixture` setup, recording, metadata and diagnostic
APIs. Prepared and direct routes use the same expectations; held statements
exercise changing inputs and session settings. Stored defaults/views, a named
row-bound XML2 CHECK and the explicit native-only control phase exercise
provider-before-consumer replay, retained rows, statement rebinding,
independent-driver destinations and rejected replacement atomicity. CHECK
probes demand fresh direct/prepared/held writes, genuine NULL acceptance,
full constraint/compile diagnostics and later-row statement atomicity before
and after current-name, detached-provider and native replacement transitions.
Public and quoted custom-schema headers keep genuine
nonrelocatable target coordinates; renamed/detached scalar consumers also replay
after the original header is removed. No private engine APIs or fixture
implementations live here.

Installation scenarios assert exact early/late signature-collision diagnostics
and preserve the original loose routine's identity, metadata and behavior without
publishing an extension or partial members.

XPath table scenarios preserve key/document/relation/path-list/criteria order,
typed anonymous-record metadata and positional nodeset expansion. Independent
direct/prepared expectations cover literal pipe splitting, unmatched versus
empty nodes, malformed-document key retention, RawMemory decoding, exact ordered
notices and complete XML, type, record-context, SPI and authority diagnostics.
Combined parser/evaluation/compile boundaries preserve full diagnostic order and
duplicates; fresh and held calls prove isolation, nonfatal NULL projection and
healthy reuse without evaluation notices.
The genuine STABLE routine executes read/write SPI; injected commands prove
eager source materialization, final-SELECT validation and statement rollback
after later conversion, XPath or cancellation failure. Typed FROM views retain
current routine/type names and invoker authority through rename, detach,
headerless replay and native same/independent-driver replacement; retained Rows
and failed replacement checks use the shared control APIs.
Held continuity projects the domain member through its genuine builtin codec.
A separate raw-domain held query preserves the catalog-lifetime signature
fence: replacement rejects the old domain result, while fresh domain queries
succeed. Same-lifetime domain rename, retained raw-domain Rows and rejected
replacement checks independently preserve current/accepted ownership.

## Testing

Use the reusable family selector `^TestReferenceXML2` with `make check` under the
shared [testing policy](../../docs/TESTING.md). `TestMain` delegates oracle and
scenario lifecycle ownership to `fixture.Main`.

Client-byte witnesses distinguish typed-nil TEXT bytes (SQL NULL on both
backends) from nonnil empty input. Full compile diagnostics and healthy held
reuse remain independently asserted on both routes.
