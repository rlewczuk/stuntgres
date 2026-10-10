package reference_test

import "strings"

// Complete PostgreSQL 18.0 records observed in tests-1.md. Empty fields remain
// assertions, not normalization. Query routing supplies only independently
// observed source/portal fields; callers independently specify state/primary.
func regtypeErrorExpectation(state, primary, query, route string) (referenceDiagnostic, string) {
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: state, Message: primary}
	stage := "execute"
	switch query {
	case "SELECT $1::regtype AS r":
		want.Context = "unnamed portal parameter $1 = '...'"
		if primary == "" {
			if state == "42704" {
				want.Message = `type "regtype_a.sequence_subject" does not exist`
			}
			if state == "3F000" {
				want.Message = `schema "absent_regtype_schema" does not exist`
			}
		}
	case "SELECT $1::text::regtype AS r":
	case "SELECT $1::regtype AS a,$2::regtype AS b":
		want.Context = "unnamed portal parameter $2 = '...'"
	case "INSERT INTO regtype_a.values_subject(id,a) VALUES(7,$1)", "INSERT INTO regtype_a.recursive_subject(id,da) VALUES(4,$1)", "SELECT $1::regtype AS r FROM regtype_a.values_subject WHERE id=99", "SELECT NULL::text || $1::regtype::text AS r":
		want.Context = "unnamed portal parameter $1 = '...'"
	case "INSERT INTO regtype_reader.regtype_denied VALUES($1)":
		if state == "42704" {
			want.Context = "unnamed portal parameter $1 = '...'"
		}
	case "SELECT TRUE::regtype AS r":
		want.Position = "12"
		if strings.Contains(route, "prepared") {
			stage = "prepare"
		}
	case "SELECT '-1'::bigint::regtype AS r":
	case "INSERT INTO regtype_a.values_subject VALUES(9,'integer'::text)":
		want.Message = `column "r" is of type regtype but expression is of type text`
		want.Hint = "You will need to rewrite or cast the expression."
		want.Position = "47"
		if strings.Contains(route, "prepared") {
			stage = "prepare"
		}
	case "INSERT INTO regtype_a.values_subject VALUES(4,'integer'),(5,'missing_regtype_name')":
		want.Position = "61"
		if strings.Contains(route, "prepared") {
			stage = "prepare"
		}
	case "UPDATE regtype_a.values_subject SET r='missing_regtype_name'":
		want.Position = "39"
		if strings.Contains(route, "prepared") {
			stage = "prepare"
		}
	case "DROP DOMAIN regtype_a.same RESTRICT":
		want.Message = "cannot drop type same because other objects depend on it"
		want.Detail = "default value for column fallback of table values_subject depends on type same\nview reference_view depends on type same\ndefault value for column r of table expression_subject depends on type same\nconstraint expression_subject_r_check on table expression_subject depends on type same\ncolumn r of table generated_subject depends on type same\nindex regtype_reference_index depends on type same"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	case "DROP TABLE regtype_a.row_referent RESTRICT":
		want.Message = "cannot drop table row_referent because other objects depend on it"
		want.Detail = "view row_dependency depends on type row_referent"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	case "SELECT * FROM regtype_a.reference_view":
		want.Message = `relation "regtype_a.reference_view" does not exist`
		want.Position = "15"
	case "SELECT * FROM regtype_a.row_dependency":
		want.Message = `relation "regtype_a.row_dependency" does not exist`
		want.Position = "15"
	default:
		if route == "authorization-direct" || route == "authorization-prepared" {
			want.Position = "8"
			if route == "authorization-prepared" {
				stage = "prepare"
			}
		} else {
			panic("regtype diagnostic requires independently specified expectation: " + query)
		}
	}
	return want, stage
}

func regtypeInputDiagnostic(fact regtypeInvalidInput, route string) referenceDiagnostic {
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: fact.state, Message: fact.primary}
	if fact.parseContext {
		want.Context = `invalid type name "` + fact.input + `"`
	}
	switch route {
	case "literal-query", "literal-prepare":
		want.Position = "17"
	case "direct-frame", "held-frame":
		want.Position = fact.position
		if want.Context != "" {
			want.Context += "\n"
		}
		text := "..."
		if fact.input == "" {
			text = ""
		}
		want.Context += "unnamed portal parameter $1 = '" + text + "'"
	case "runtime-text", "held-runtime-text":
		want.Position = fact.position
	default:
		panic("unknown regtype input route: " + route)
	}
	return want
}
