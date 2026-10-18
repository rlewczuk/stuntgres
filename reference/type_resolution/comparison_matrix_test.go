package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var comparisonSpellings = []string{"=", "<>", "!=", "<", "<=", ">", ">="}
var comparisonOperatorSlots = []int{0, 1, 1, 2, 3, 4, 5}

func comparisonSignature(left, right int) comparisonReleasedSignature {
	if len(comparisonConcrete) != 87 || len(comparisonReleasedCells) != 87 || len(comparisonReleasedCells[left]) != 87 {
		panic("incomplete concrete comparison ledger")
	}
	index := strings.IndexByte(comparisonLedgerAlphabet, comparisonReleasedCells[left][right])
	if index < 0 || index >= len(comparisonReleasedSignatures) {
		panic("unclassified concrete comparison cell")
	}
	return comparisonReleasedSignatures[index]
}

// One serialized, bounded fixture per left identity, never per cell/container.
func comparisonFiniteScenario(t *testing.T, left int, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			comparisonFiniteSetup(t, p, b)
			body(t, p, b)
		})
	}
}

func comparisonFiniteSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	columns := []string{"id integer"}
	values := [3][]string{{"0"}, {"1"}, {"2"}}
	for i, typ := range comparisonConcrete {
		if typ.element >= 0 || typ.name == "char" || typ.name == "pg_node_tree" || typ.name == "void" {
			continue
		}
		columns = append(columns, fmt.Sprintf("c%d %s", i, typ.name))
		values[0] = append(values[0], "NULL")
		values[1] = append(values[1], comparisonSQLString(typ.low))
		values[2] = append(values[2], comparisonSQLString(typ.high))
	}
	columns = append(columns, "CONSTRAINT comparison_sample_check CHECK(id>=0)")
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_values("+strings.Join(columns, ",")+")", 0))
	var tuples []string
	for _, row := range values {
		tuples = append(tuples, "("+strings.Join(row, ",")+")")
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO comparison_values VALUES"+strings.Join(tuples, ","), 3))
}

func comparisonSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// Capture the actual catalog CHAR values once per bound query without changing
// scalar-subquery cardinality or multiplying the finite source rows.
const comparisonFiniteCharSource = "(SELECT (SELECT attgenerated FROM pg_attribute WHERE attrelid='comparison_values'::regclass AND attnum=1) AS low,(SELECT relkind FROM pg_class WHERE oid='comparison_values'::regclass) AS high) comparison_char_source"

func comparisonFiniteValue(index int, alias string, peer int) string {
	return comparisonFiniteValueFrom(index, alias, peer, "")
}

func comparisonFiniteValueFrom(index int, alias string, peer int, charSource string) string {
	typ := comparisonConcrete[index]
	if typ.element >= 0 {
		return "(CASE WHEN " + alias + ".id=0 THEN NULL ELSE ARRAY[" + comparisonFiniteValueFrom(typ.element, alias, -1, charSource) + "] END)"
	}
	var expr string
	switch typ.name {
	case "char":
		low := "(SELECT attgenerated FROM pg_attribute WHERE attrelid='comparison_values'::regclass AND attnum=1)"
		high := "(SELECT relkind FROM pg_class WHERE oid='comparison_values'::regclass)"
		if charSource != "" {
			low, high = charSource+".low", charSource+".high"
		}
		expr = "(CASE WHEN " + alias + ".id=1 THEN " + low + " ELSE " + high + " END)"
	case "pg_node_tree":
		expr = "(SELECT conbin FROM pg_constraint WHERE conname='comparison_sample_check')"
	case "void":
		expr = "pg_sleep(0)"
	default:
		expr = alias + ".c" + strconv.Itoa(index)
	}
	// Node-tree serialization is not shared with PostgreSQL's physical tree
	// format. A nonempty tree versus empty string, or itself, is predictable
	// independently on both representations; no copied physical tree golden.
	if peer >= 0 && comparisonConcrete[peer].name == "pg_node_tree" && comparisonStringType(typ.name) {
		if typ.name == "char" {
			expr = "(SELECT attgenerated FROM pg_attribute WHERE attrelid='comparison_values'::regclass AND attnum=1)"
			if charSource != "" {
				expr = charSource + ".low"
			}
		} else {
			expr = "(SELECT c" + strconv.Itoa(index) + " FROM comparison_values WHERE id=1)"
		}
	} else if typ.name != "char" && typ.name != "pg_node_tree" && typ.name != "void" && typ.name != "bpchar" && typ.name != "bit" {
		// Ordinary columns already contain NULL at id0. Keep CASE for
		// modifier-sensitive BPCHAR/BIT and NULL-gated special sources.
		return expr
	}
	return "(CASE WHEN " + alias + ".id=0 THEN NULL ELSE " + expr + " END)"
}

func comparisonStringType(name string) bool {
	return name == "char" || name == "name" || name == "text" || name == "varchar" || name == "bpchar"
}

func comparisonFiniteSign(left, right, l, r int) int {
	a, b := comparisonConcrete[left], comparisonConcrete[right]
	if a.name == "pg_node_tree" || b.name == "pg_node_tree" {
		if a.name == b.name {
			return 0
		}
		if a.name == "pg_node_tree" {
			return 1
		}
		return -1
	}
	if a.element >= 0 && b.element >= 0 {
		return comparisonFiniteSign(a.element, b.element, l, r)
	}
	if comparisonStringType(a.name) && comparisonStringType(b.name) {
		x, y := "", ""
		if l == 2 {
			x = a.high
		}
		if r == 2 {
			y = b.high
		}
		return strings.Compare(x, y)
	}
	if a.name == "bool" && b.name == "bool" {
		l, r = 3-l, 3-r
	}
	if l < r {
		return -1
	}
	if l > r {
		return 1
	}
	return 0
}

func comparisonTruth(op int, sign int) bool {
	switch op {
	case 0:
		return sign == 0
	case 1, 2:
		return sign != 0
	case 3:
		return sign < 0
	case 4:
		return sign <= 0
	case 5:
		return sign > 0
	case 6:
		return sign >= 0
	default:
		panic("unclassified operator spelling")
	}
}

func comparisonDemand(index, op int) string {
	typ := comparisonConcrete[index]
	if typ.element < 0 {
		return ""
	}
	leaf := comparisonConcrete[typ.element].name
	if leaf == "json" || leaf == "jsonpath" {
		if op <= 2 {
			return "could not identify an equality operator for type " + leaf
		}
		return "could not identify a comparison function for type " + leaf
	}
	if leaf == "aclitem" && op >= 3 {
		return "could not identify a comparison function for type aclitem"
	}
	return ""
}

func comparisonNoMatch(q string, left, right, op int, marker string) fixture.Diagnostic {
	canonical := comparisonSpellings[op]
	if canonical == "!=" {
		canonical = "<>"
	}
	return fixture.Diagnostic{Code: "42883", Message: "operator does not exist: " + comparisonConcrete[left].diagnostic + " " + canonical + " " + comparisonConcrete[right].diagnostic,
		Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: comparisonCheckpointPosition(q, strings.TrimPrefix(marker, " "))}
}

func comparisonDemandRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, q, message string) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: message})
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func comparisonMatrix(t *testing.T, family string) {
	for left, typ := range comparisonConcrete {
		if typ.family != family {
			continue
		}
		t.Run(typ.name, func(t *testing.T) {
			comparisonFiniteScenario(t, left, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				visited := 0
				for right, rt := range comparisonConcrete {
					for op, spelling := range comparisonSpellings {
						visited++
						t.Run(rt.name+spelling, func(t *testing.T) {
							x, y := comparisonFiniteValue(left, "l", right), comparisonFiniteValue(right, "r", left)
							expr := x + " " + spelling + " " + y
							from := " FROM comparison_values l CROSS JOIN comparison_values r"
							if comparisonSignature(left, right).operators[comparisonOperatorSlots[op]] == 0 {
								for _, filter := range []string{"l.id=1 AND r.id=1", "l.id=0 AND r.id=0", "FALSE"} {
									q := "SELECT " + expr + " AS v" + from + " WHERE " + filter
									comparisonCheckpointAnalysis(t, p, b, q, comparisonNoMatch(q, left, right, op, " "+spelling+" "))
								}
								return
							}
							demand := comparisonDemand(left, op)
							filter := ""
							if demand != "" {
								comparisonDemandRoutes(t, p, b, "SELECT "+expr+" AS v"+from+" WHERE l.id=1 AND r.id=1", demand)
								filter = " WHERE l.id=0 OR r.id=0"
							}
							var expected, retained [][]any
							for l := range 3 {
								for r := range 3 {
									if demand != "" && l != 0 && r != 0 {
										continue
									}
									var want any
									if l != 0 && r != 0 {
										want = comparisonTruth(op, comparisonFiniteSign(left, right, l, r))
									}
									expected = append(expected, []any{int64(l), int64(r), want})
									if want == true {
										retained = append(retained, []any{int64(l), int64(r)})
									}
								}
							}
							q := "SELECT l.id AS l,r.id AS r," + expr + " AS v" + from + filter + " ORDER BY l.id,r.id"
							comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"l", "r", "v"}, []string{"INT4", "INT4", "BOOL"}, expected))
							comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT "+expr+" AS v"+from+" WHERE FALSE", []string{"v"}, []string{"BOOL"}, nil))
							if demand == "" {
								q = "SELECT l.id AS l,r.id AS r" + from + " WHERE " + expr + " ORDER BY l.id,r.id"
								comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"l", "r"}, []string{"INT4", "INT4"}, retained))
							}
						})
					}
				}
				if visited != 609 {
					t.Fatalf("visited %d cells, want 609", visited)
				}
			})
		})
	}
}

func TestReferenceComparisonMatrixNumeric(t *testing.T)       { comparisonMatrix(t, "Numeric") }
func TestReferenceComparisonMatrixStrings(t *testing.T)       { comparisonMatrix(t, "Strings") }
func TestReferenceComparisonMatrixTemporal(t *testing.T)      { comparisonMatrix(t, "Temporal") }
func TestReferenceComparisonMatrixIdentifiers(t *testing.T)   { comparisonMatrix(t, "Identifiers") }
func TestReferenceComparisonMatrixBinaryNetwork(t *testing.T) { comparisonMatrix(t, "BinaryNetwork") }
func TestReferenceComparisonMatrixNegativeSpace(t *testing.T) { comparisonMatrix(t, "NegativeSpace") }
func TestReferenceComparisonMatrixRanges(t *testing.T)        { comparisonMatrix(t, "Ranges") }
func TestReferenceComparisonMatrixArrays(t *testing.T)        { comparisonMatrix(t, "Arrays") }
