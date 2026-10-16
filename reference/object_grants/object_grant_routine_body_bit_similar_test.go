package reference_test

import "testing"

func TestReferenceObjectGrantRoutineBodyBitAndSimilar(t *testing.T) {
	for _, tc := range []objectGrantBodyCase{
		{
			// Unlike TEXT/BYTEA, the released BIT wrapper delegates directly
			// to length(bit); it has no octet_length or multiplication edge.
			name: "bit_length_bit", columns: "bits BIT(3)", values: "B'101'",
			expression: "bit_length(bits)", nullExpression: "bit_length(B'' OPERATOR(pg_catalog.&) NULL)", deadExpression: "CASE WHEN FALSE THEN bit_length(bits) ELSE 3 END",
			resultType: "INT4", value: int64(3), target: "pg_catalog.length(bit)", deniedName: "length",
			primitive: "length(bits)", primitiveType: "INT4", primitiveValue: int64(3),
			independent: "bit_length('é'::TEXT)", independentType: "INT4", independentValue: int64(16),
		},
		{
			// The escape-quoted middle pattern is the independently expected
			// returned substring; the outer sections match a and c.
			name: "similar_pattern_body", columns: "s TEXT,p TEXT,e TEXT", values: `'abc','a#"b#"c','#'`,
			expression: "substring(s,p,e)", nullExpression: "substring(NULL::TEXT,'a#\"b#\"c','#')", deadExpression: "CASE WHEN FALSE THEN substring(s,p,e) ELSE 'b' END",
			resultType: "TEXT", value: "b", target: "pg_catalog.similar_to_escape(text,text)", deniedName: "similar_to_escape",
			primitive: "s SIMILAR TO '%' ESCAPE e", primitiveType: "BOOL", primitiveValue: true,
			independent: "substring(s,'b')", independentType: "TEXT", independentValue: "b",
		},
		{
			name: "similar_regex_body", columns: "s TEXT,p TEXT,e TEXT", values: `'abc','a#"b#"c','#'`,
			expression: "substring(s,p,e)", nullExpression: "substring(NULL::TEXT,'a#\"b#\"c','#')", deadExpression: "CASE WHEN FALSE THEN substring(s,p,e) ELSE 'b' END",
			resultType: "TEXT", value: "b", target: "pg_catalog.substring(text,text)", deniedName: "substring",
			primitive: "substring(s,'b')", primitiveType: "TEXT", primitiveValue: "b",
			independent: "upper(s)", independentType: "TEXT", independentValue: "ABC",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { objectGrantBodyConsumer(t, tc) })
	}
}
