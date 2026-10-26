package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Each semantic cell is explicitly classified by a released direct edge or a
// named generic/no-path rule, in all three contexts. Syntax capability is a
// separate public route outcome: it never turns a forbidden edge into admitted.
type castMatrixCell struct {
	source, target castImplementedType
	contexts       [3]castReleasedDecision
}

func castPhysicalFact(v castImplementedType) castImplementedType {
	for v.base != "" {
		v = castInventoryType(v.base)
	}
	return v
}

func castInputFreeTarget(cell castMatrixCell) bool {
	return cell.contexts[2].admitted && (castInventoryIdentity(cell.source) == castInventoryIdentity(cell.target) || cell.contexts[2].operation == "named-to-record" || cell.contexts[2].operation == "named-array-to-record-array")
}

func castMatrixResultFact(cell castMatrixCell) castImplementedType {
	if cell.contexts[2].operation == "void-param-zero-callable" && cell.target.name == "earth" {
		return castInventoryType("float8")
	}
	if cell.contexts[2].operation == "named-to-record" || cell.contexts[2].operation == "named-array-to-record-array" {
		return cell.source
	}
	return cell.target
}

// Public annotation restrictions are explicit existing capabilities, not a
// global four-type cast policy and not permission to install new providers.
func castTargetBoundary(cell castMatrixCell, q string, pg bool) *fixture.Diagnostic {
	target := cell.target
	if pg || target.target || castInputFreeTarget(cell) {
		return nil
	}
	declaration := castInventoryIdentity(target)
	message := "unsupported column type"
	if target.element != "" && castInventoryType(target.element).kind == "e" {
		message = "unsupported array element type"
	}
	return &fixture.Diagnostic{Code: "0A000", Message: message, Position: fixture.Position(strings.LastIndex(q, declaration) + 1)}
}
func castMatrixForbidden(source, target castImplementedType, q string) fixture.Diagnostic {
	if source.name == "record[]" && target.name == "cast_pair[]" {
		// ArrayExpr converts the ROW element structurally; its deferred NULL
		// record element has no structural source and owns the refusal.
		return fixture.Diagnostic{Code: "42846", Message: "cannot cast type record to cast_pair", Position: fixture.Position(strings.Index(q, "NULL::record") + 1)}
	}
	position := strings.Index(q, "CAST(") + 1
	if strings.HasPrefix(q, "SELECT (") {
		position = strings.LastIndex(q, "::") + 1
	}
	return fixture.Diagnostic{Code: "42846", Message: "cannot cast type " + source.diagnostic + " to " + target.diagnostic, Position: fixture.Position(position)}
}

type castMatrixValue struct {
	input, output string
	diagnostic    *fixture.Diagnostic
	polygon       bool
}

func castScalarMatrixValue(cell castMatrixCell) castMatrixValue {
	source, target := cell.source, cell.target
	value := castMatrixValue{input: source.seed, output: source.output}
	decision := cell.contexts[2]
	if !decision.admitted {
		return value
	}
	if source.category == "S" {
		return castStringMatrixValue(cell)
	}
	physical := castPhysicalFact(target)
	if castPhysicalFact(source).element != "" {
		value = castArrayMatrixValue(cell)
	} else if decision.edge != nil {
		witness := castDirectWitness(*decision.edge)
		value.input, value.output = witness.input, witness.output
		value.polygon = decision.edge.source == "circle" && decision.edge.target == "polygon"
	} else if physical.category == "S" {
		// With no released override, output/input IO preserves the descriptor output.
		// In particular BOOLEAN->NAME is "t", unlike BOOLEAN->TEXT's "true".
		value.output = source.output
	} else if physical.name != castPhysicalFact(source).name && castInventoryIdentity(source) != castInventoryIdentity(target) && decision.operation != "named-to-record" && decision.operation != "row-to-composite" {
		panic("admitted scalar cell lacks independently specified value: " + source.name + " -> " + target.name)
	}
	if target.name == "earth" && castPhysicalFact(source).name == "cube" {
		value.input, value.output = target.seed, target.output
	}
	if target.name == "cast_short" {
		runes := []rune(value.output)
		if len(runes) > 3 {
			value.output = string(runes[:3])
		}
	}
	if (target.name == "cast_positive" || target.name == "cast_nested") && value.output == "0" {
		value.input, value.output = "1", "1"
	}
	if target.name == "information_schema.yes_or_no" && value.output != "YES" && value.output != "NO" {
		value.diagnostic = &fixture.Diagnostic{Code: "23514", Message: `value for domain information_schema.yes_or_no violates check constraint "yes_or_no_check"`, Schema: "information_schema", DataType: "yes_or_no", Constraint: "yes_or_no_check"}
	}
	return castConstrainedNumericValue(source, value)
}

func TestReferenceCastImplementedMatrixNumeric(t *testing.T)    { castScalarMatrix(t, "Numeric", false) }
func TestReferenceCastImplementedFunctionsNumeric(t *testing.T) { castScalarMatrix(t, "Numeric", true) }
func TestReferenceCastImplementedMatrixIdentifiers(t *testing.T) {
	castScalarMatrix(t, "Identifiers", false)
	t.Run("oidvector_codec", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				for _, input := range []any{"1 2 3", "", nil} {
					step := fixture.Select("SELECT CAST($1 AS oidvector) AS v", []string{"v"}, []string{"OIDVECTOR"}, [][]any{{input}}, input)
					// Assert this nominal carrier's actual public codec before
					// Normalize can equate native text and lib/pq byte payloads.
					step.CheckRaw = func(t *testing.T, column, row int, value any) bool {
						if input == nil {
							if value != nil {
								t.Fatalf("oidvector SQL NULL became %T(%v)", value, value)
							}
							return true
						}
						literal := input.(string)
						if b.PG {
							raw, ok := value.([]byte)
							if !ok || raw == nil || string(raw) != literal {
								t.Fatalf("raw PostgreSQL oidvector: %T(%v); want bytes %q", value, value, literal)
							}
						} else if raw, ok := value.(string); !ok || raw != literal {
							t.Fatalf("raw native oidvector: %T(%v); want string %q", value, value, literal)
						}
						return true
					}
					fixture.PgDescriptionRows(t, p, b, step)
				}
			})
		}
	})
}
func TestReferenceCastImplementedFunctionsIdentifiers(t *testing.T) {
	castScalarMatrix(t, "Identifiers", true)
}
func TestReferenceCastImplementedMatrixTemporal(t *testing.T) { castScalarMatrix(t, "Temporal", false) }
func TestReferenceCastImplementedFunctionsTemporal(t *testing.T) {
	castScalarMatrix(t, "Temporal", true)
}
func TestReferenceCastImplementedMatrixJSONFTS(t *testing.T)    { castScalarMatrix(t, "JSONFTS", false) }
func TestReferenceCastImplementedFunctionsJSONFTS(t *testing.T) { castScalarMatrix(t, "JSONFTS", true) }
func TestReferenceCastImplementedMatrixBinaryNetwork(t *testing.T) {
	castScalarMatrix(t, "BinaryNetwork", false)
}
func TestReferenceCastImplementedFunctionsBinaryNetwork(t *testing.T) {
	castScalarMatrix(t, "BinaryNetwork", true)
}
func TestReferenceCastImplementedMatrixGeometry(t *testing.T) { castScalarMatrix(t, "Geometry", false) }
func TestReferenceCastImplementedFunctionsGeometry(t *testing.T) {
	castScalarMatrix(t, "Geometry", true)
}
func TestReferenceCastImplementedMatrixRanges(t *testing.T)    { castScalarMatrix(t, "Ranges", false) }
func TestReferenceCastImplementedFunctionsRanges(t *testing.T) { castScalarMatrix(t, "Ranges", true) }
func TestReferenceCastImplementedMatrixInstalled(t *testing.T) {
	castScalarMatrix(t, "Installed", false)
}
func TestReferenceCastImplementedFunctionsInstalled(t *testing.T) {
	castScalarMatrix(t, "Installed", true)
}
func TestReferenceCastImplementedMatrixStrings(t *testing.T)    { castScalarMatrix(t, "Strings", false) }
func TestReferenceCastImplementedFunctionsStrings(t *testing.T) { castScalarMatrix(t, "Strings", true) }
func TestReferenceCastImplementedMatrixArrays(t *testing.T)     { castScalarMatrix(t, "Arrays", false) }
func TestReferenceCastImplementedFunctionsArrays(t *testing.T)  { castScalarMatrix(t, "Arrays", true) }
func TestReferenceCastImplementedMatrixStructural(t *testing.T) {
	castScalarMatrix(t, "Structural", false)
}
func TestReferenceCastImplementedFunctionsStructural(t *testing.T) {
	castScalarMatrix(t, "Structural", true)
}
func TestReferenceCastImplementedMatrixDomains(t *testing.T)    { castScalarMatrix(t, "Domains", false) }
func TestReferenceCastImplementedFunctionsDomains(t *testing.T) { castScalarMatrix(t, "Domains", true) }
