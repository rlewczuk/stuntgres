# reference/xml_mapping

Dual-mode scenarios for schema/database XML mapping. Table/query/cursor mapping
belongs to [strings](../strings/README.md#testing).

## Testing

Follow [the shared testing policy](../../docs/TESTING.md). `main_test.go` delegates
to `fixture.Main`; each scenario uses the shared fixture and public driver APIs.
Register roles before schemas and both before creating the scenario, so handles
close and schemas retire before role teardown.
The local scenario helper revokes PUBLIC USAGE on `public` in both backends before
setup. Cases that include `public` grant USAGE explicitly to their reader role;
database membership expectations never depend on bootstrap versus reset defaults.

Use `RUN='^TestReferenceXMLMapScope'`, adding `REFERENCE=1` for the pinned oracle.
Helpers own independent released-template expectations for direct and genuinely
held routes; expected XML is never learned from a mapper.
