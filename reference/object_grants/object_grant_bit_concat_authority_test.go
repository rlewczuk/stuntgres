package reference_test

import "testing"

// Both physical input families select the released VARBIT,VARBIT declaration;
// neither may accidentally borrow the BYTEA concatenation identity.
func TestReferenceObjectGrantBitConcatAuthority(t *testing.T) {
	objectGrantBodyConsumer(t, objectGrantBodyCase{
		name: "bit_concat", columns: "f BIT(5),g BIT(5),v VARBIT,w VARBIT,z VARBIT", values: "B'10001',B'01101',B'10001',B'01101',B'1000101101'",
		expression: "f || g", nullExpression: "B'' OPERATOR(pg_catalog.||) NULL", deadExpression: "CASE WHEN FALSE THEN f || g ELSE z END",
		resultType: "VARBIT", value: "1000101101", target: "pg_catalog.bitcat(bit varying,bit varying)", deniedName: "bitcat",
		primitive: "v OPERATOR(pg_catalog.||) w", primitiveType: "VARBIT", primitiveValue: "1000101101",
		independent: "f & g", independentType: "BIT", independentValue: "00001",
	})
}
