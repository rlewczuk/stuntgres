# XML2 reference scenarios

This package owns public SQL and native-control scenarios for the selected
`xml_valid`, `xpath_string`, `xpath_number`, `xpath_bool`, `xpath_table`,
`xpath_nodeset` and `xpath_list` declarations (ten signatures). Independently
specified results distinguish XPath root-element context, scalar conversions,
FLOAT4 narrowing, typed table expansion, raw node serialization, literal
wrappers/separators, strict NULLs, validation modes,
document/compile/evaluation precedence and namespace handling.
Raw-encoding scenarios distinguish decoded-Text validation from genuine
declaration-aware document decoding, including genuine ASCII, ISO-2022-JP,
ISO-2022-JP-2 G0/G2 transitions, ISO-2022-KR/CN designation/SI/SO/SS2 and UTF-7
UTF16/shift state. Canonical KR/CN/UTF-7 labels and ASCII case variants use
independent source/style inputs, including CNS1 additions and CNS2 single shifts.
Conversion failures precede XPath compilation; scenarios assert full compile
diagnostics, strict typed NULLs and retained direct/prepared/held values.
Source and stylesheet-shaped documents are inspected through scalar XPath; these scenarios
do not claim native stylesheet compilation or transformation.
Catalog assertions select these ten declarations rather than claiming parity
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
nonrelocatable target coordinates; renamed/detached scalar and set consumers
also replay after the original header is removed. No private engine APIs or fixture
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

Set scenarios exercise the installed SQL-standard wrappers, their structural
body metadata and normal dependencies, current inner identities and independent
outer/inner EXECUTE checks. Notice-enabled sessions retain complete unsupported
result diagnostics and distinguish preparation, folding and actual demand.
Stored consumers use all five set signatures, including genuine row-dependent
CHECK writes and detached wrapper-to-kernel replay.
Controls also exercise populated mixed-wrapper index keys, predicates and both
nested operand directions before ordinary wrapper configuration. Each wrapper's
SET/RESET preserves exact TEXT/NULL results and its sibling's configuration,
with fresh INSERT/UPDATE/DELETE writes after every transition.
Long-lived set scenarios select custom planning explicitly; a separate bounded
matrix exercises forced custom/generic plans and the actual wrapper proconfig.
Complete attribution and NULL/ACL demand assertions do not depend on an
automatic cost transition or an execution count.
Configured wrappers observably suppress unsupported-result notices inside the
callee while restoring the caller's SHOW value after success and compile error;
RESET restores notice delivery on the same held statement. Controls and smoke
retain actual proconfig and its effect through same-/independent-driver replay.
Absent-header dumps must reconstruct and retire the genuine scratch cohort in
order, never reattach detached survivors or leave a final extension header.
Actual TEXT-state aggregates select the genuine wrapper support and combine
signatures. Their callback authority remains distinct from ordinary EXECUTE:
aggregate-owner support admission does not grant the caller inner-kernel access.
The same held aggregate exercises complete compile/ACL diagnostics, strict NULL
suppression, notice configuration and caller-setting restoration.

## Testing

Use the reusable family selector `^TestReferenceXML2` with `make check` under the
shared [testing policy](../../docs/TESTING.md). `TestMain` delegates oracle and
scenario lifecycle ownership to `fixture.Main`.

Client-byte witnesses distinguish genuine SQL NULLs and typed-nil TEXT bytes
(SQL NULL on both backends) from nonnil empty bytes (empty TEXT). Full compile
diagnostics and healthy held reuse remain independently asserted.
