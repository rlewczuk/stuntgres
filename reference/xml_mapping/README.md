# reference/xml_mapping

Public dual-mode scenarios for schema/database XML mapping: current namespace and
relation membership, real USAGE/read authority, ordinary SELECT/view/RLS execution,
XML result descriptors, exact nested characters, scope XSD and combined output.
Held statements cover current grants, roles and namespace generations. Table,
query and cursor mapping scenarios remain in `reference/strings`.

## Testing

Follow [the shared testing policy](../../docs/TESTING.md). `main_test.go` delegates
to `fixture.Main`; each scenario uses the shared fixture and public driver APIs.
Register roles before schemas and both before creating the scenario, so handles
close and schemas retire before role teardown.
The local scenario helper revokes PUBLIC USAGE on `public` in both backends before
setup. Cases that include `public` grant USAGE explicitly to their reader role;
database membership expectations never depend on bootstrap versus reset defaults.

Use `RUN='^TestReferenceXMLMapScope'`, adding `REFERENCE=1` for the pinned oracle.
Helpers independently transcribe released scope templates and INTEGER restrictions;
they never obtain expected XML from a mapper. Mixed scalar definitions are checked
once each with an exact remaining scope template, without imposing an unspecified
database type order. Direct and genuinely held executions assert independently
expected results, metadata and complete logical diagnostics before comparison.
