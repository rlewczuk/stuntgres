package reference_test

import "testing"

func TestReferenceObjectGrantRoutineBodyPolymorphicCasts(t *testing.T) {
	// REL_18_0 pg_cast.dat's cross-category TEXT function-method entries are
	// text(bool)2971 and text(inet)730; CIDR relabels to INET before730.
	// These are not the caller-formal bpchar/name/char conversions, nor
	// arbitrary output-function ACLs for ordinary CoerceViaIO instances.
	for _, tc := range []objectGrantBodyCase{
		{
			name: "quote_literal_bool", columns: "b BOOL", values: "TRUE",
			expression: "quote_literal(b)", nullExpression: "quote_literal(NULL::BOOL)", deadExpression: "CASE WHEN FALSE THEN quote_literal(b) ELSE '''true''' END",
			resultType: "TEXT", value: "'true'", target: "pg_catalog.text(boolean)", deniedName: "text",
			primitive: "b::TEXT", primitiveType: "TEXT", primitiveValue: "true",
			independent: "quote_literal(1::INTEGER)", independentType: "TEXT", independentValue: "'1'",
		},
		{
			name: "quote_nullable_bool", columns: "b BOOL", values: "TRUE",
			expression: "quote_nullable(b)", nullExpression: "quote_nullable(NULL::BOOL)", nullValue: "NULL", deadExpression: "CASE WHEN FALSE THEN quote_nullable(b) ELSE '''true''' END",
			resultType: "TEXT", value: "'true'", target: "pg_catalog.text(boolean)", deniedName: "text",
			primitive: "b::TEXT", primitiveType: "TEXT", primitiveValue: "true",
			independent: "quote_nullable(1::INTEGER)", independentType: "TEXT", independentValue: "'1'",
		},
		{
			name: "quote_literal_inet", columns: "ip INET", values: "INET '10.0.0.0/8'",
			expression: "quote_literal(ip)", nullExpression: "quote_literal(NULL::INET)", deadExpression: "CASE WHEN FALSE THEN quote_literal(ip) ELSE '''10.0.0.0/8''' END",
			resultType: "TEXT", value: "'10.0.0.0/8'", target: "pg_catalog.text(inet)", deniedName: "text",
			primitive: "ip::TEXT", primitiveType: "TEXT", primitiveValue: "10.0.0.0/8",
			independent: "quote_literal(TRUE)", independentType: "TEXT", independentValue: "'true'",
		},
		{
			name: "quote_nullable_cidr", columns: "ip CIDR", values: "CIDR '10.0.0.0/8'",
			expression: "quote_nullable(ip)", nullExpression: "quote_nullable(NULL::CIDR)", nullValue: "NULL", deadExpression: "CASE WHEN FALSE THEN quote_nullable(ip) ELSE '''10.0.0.0/8''' END",
			resultType: "TEXT", value: "'10.0.0.0/8'", target: "pg_catalog.text(inet)", deniedName: "text",
			primitive: "ip::TEXT", primitiveType: "TEXT", primitiveValue: "10.0.0.0/8",
			independent: "quote_nullable(TRUE)", independentType: "TEXT", independentValue: "'true'",
		},
		{
			name: "textanycat_bool_cast", columns: "b BOOL", values: "TRUE",
			expression: "'x'::TEXT || b", nullExpression: "'x'::TEXT || NULL::BOOL", deadExpression: "CASE WHEN FALSE THEN 'x'::TEXT || b ELSE 'xtrue' END",
			resultType: "TEXT", value: "xtrue", target: "pg_catalog.text(boolean)", deniedName: "text",
			primitive: "b::TEXT", primitiveType: "TEXT", primitiveValue: "true",
			independent: "'x'::TEXT || 1::INTEGER", independentType: "TEXT", independentValue: "x1",
		},
		{
			name: "anytextcat_inet_cast", columns: "ip INET", values: "INET '10.0.0.0/8'",
			expression: "ip || 'x'::TEXT", nullExpression: "NULL::INET || 'x'::TEXT", deadExpression: "CASE WHEN FALSE THEN ip || 'x'::TEXT ELSE '10.0.0.0/8x' END",
			resultType: "TEXT", value: "10.0.0.0/8x", target: "pg_catalog.text(inet)", deniedName: "text",
			primitive: "ip::TEXT", primitiveType: "TEXT", primitiveValue: "10.0.0.0/8",
			independent: "1::INTEGER || 'x'::TEXT", independentType: "TEXT", independentValue: "1x",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { objectGrantBodyConsumer(t, tc) })
	}
}

func TestReferenceObjectGrantRoutineBodyConcatenation(t *testing.T) {
	for _, tc := range []objectGrantBodyCase{
		{
			name: "textanycat_text_body", columns: "n INT,s TEXT", values: "1,'a'",
			expression: "'x'::TEXT || n", nullExpression: "'x'::TEXT || NULL::INTEGER", deadExpression: "CASE WHEN FALSE THEN 'x'::TEXT || n ELSE 'x1' END",
			resultType: "TEXT", value: "x1", target: "pg_catalog.textcat(text,text)", deniedName: "textcat",
			primitive: "s || 'b'::TEXT", primitiveType: "TEXT", primitiveValue: "ab",
			independent: "quote_literal(n)", independentType: "TEXT", independentValue: "'1'",
		},
		{
			name: "anytextcat_text_body", columns: "n INT,s TEXT", values: "1,'a'",
			expression: "n || 'x'::TEXT", nullExpression: "NULL::INTEGER || 'x'::TEXT", deadExpression: "CASE WHEN FALSE THEN n || 'x'::TEXT ELSE '1x' END",
			resultType: "TEXT", value: "1x", target: "pg_catalog.textcat(text,text)", deniedName: "textcat",
			primitive: "s || 'b'::TEXT", primitiveType: "TEXT", primitiveValue: "ab",
			independent: "quote_literal(n)", independentType: "TEXT", independentValue: "'1'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { objectGrantBodyConsumer(t, tc) })
	}
}
