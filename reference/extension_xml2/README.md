# XML2 scalar reference scenarios

This package owns public SQL and native-control scenarios for the installed
`xml_valid(text)`, `xpath_string(text,text)`, `xpath_number(text,text)` and
`xpath_bool(text,text)` declarations. Independently specified results distinguish
XPath root-element context, scalar conversions, FLOAT4 narrowing, strict NULLs,
validation modes, document/compile/evaluation precedence and namespace handling.
Raw-encoding scenarios distinguish decoded-Text validation from genuine
declaration-aware document decoding, including genuine ASCII, ISO-2022-JP and
ISO-2022-JP-2 G0/G2 transitions, conversion failures before XPath compilation,
full compile diagnostics and retained direct/prepared/held values. Source and
stylesheet-shaped documents are inspected through scalar XPath; these scenarios
do not claim native stylesheet compilation or transformation.
Catalog assertions select these four declarations rather than claiming parity
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

## Testing

Use the reusable family selector `^TestReferenceXML2` with `make check` under the
shared [testing policy](../../docs/TESTING.md). `TestMain` delegates oracle and
scenario lifecycle ownership to `fixture.Main`.

Client-byte witnesses distinguish typed-nil TEXT bytes (SQL NULL on both
backends) from nonnil empty input. Full compile diagnostics and healthy held
reuse remain independently asserted on both routes.
