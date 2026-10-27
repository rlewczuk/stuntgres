package tablecmds

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type capturedExtendedInput struct {
	storage.ExtendedInput
	simpleSlots [8]int
	layouts     [][]int
	references  [][]ids.ObjectRef
	frames      [][]sqltype.Value
	dimensions  [8]sqltype.Value
}

func captureExtendedStatistics(s *core.StatementScope, read catalog.Read, relation *catalog.Relation, columns []storage.StatisticsColumn) ([]capturedExtendedInput, error) {
	typ, ok := read.LookupType(sqltype.PgNDistinct)
	if !ok {
		return nil, fmt.Errorf("missing pg_ndistinct type")
	}
	available := make(map[int32]storage.StatisticsColumn, len(columns))
	for _, column := range columns {
		available[column.Key.Attribute] = column
	}
	var inputs []capturedExtendedInput
	for _, raw := range read.Descriptors() {
		d, ok := raw.(*catalog.Statistics)
		if !ok || d.Relation.Address != relation.Address() || !strings.ContainsAny(d.Selection.Kinds(), "de") || d.TargetSet && d.Target == 0 {
			continue
		}
		if err := (catalog.StatisticsProvider{}).Validate(read, d); err != nil {
			return nil, err
		}
		input := capturedExtendedInput{ExtendedInput: storage.ExtendedInput{Key: storage.ExtendedStatisticsKey{Definition: read.Domain().Ref(read.Epoch(), d.Address())}, Definition: d, Type: typ}}
		input.Providers = storage.CaptureStatisticsProviders(read, d)
		eligible := true
		required := make(map[int32]bool)
		for i := range d.Selection.Len() {
			column, ok := available[d.Selection.Key(i)]
			if !ok {
				eligible = false
				break
			}
			input.simpleSlots[i] = column.Slot
			column.Slot = i
			input.Columns = append(input.Columns, column)
		}
		input.layouts = make([][]int, d.Expressions.Len())
		input.frames = make([][]sqltype.Value, d.Expressions.Len())
		input.references = make([][]ids.ObjectRef, d.Expressions.Len())
		for i := range d.Expressions.Len() {
			recipe := d.Expressions.At(i)
			input.references[i] = make([]ids.ObjectRef, recipe.DependencyLen())
			for j := range recipe.DependencyLen() {
				input.references[i][j] = recipe.Dependency(j)
			}
			for j := range recipe.InputLen() {
				attribute := recipe.Input(j).Address.SubID
				if _, ok := available[attribute]; !ok {
					eligible = false
				}
				required[attribute] = true
			}
			layout := make([]int, recipe.AttributeLen())
			identity := len(layout) == len(relation.Attributes)
			for j := range layout {
				layout[j] = relation.IndexForAttribute(recipe.Attribute(j))
				if layout[j] != j {
					identity = false
				}
			}
			var frame []sqltype.Value
			if identity {
				layout = nil
			} else {
				for j := range i {
					if slices.Equal(layout, input.layouts[j]) {
						frame = input.frames[j]
						break
					}
				}
				if frame == nil {
					frame = make([]sqltype.Value, len(layout))
				}
			}
			input.layouts[i], input.frames[i] = layout, frame
			input.Columns = append(input.Columns, storage.StatisticsColumn{Key: storage.StatisticsKey{Attribute: -int32(i + 1)}, Usage: recipe.Usage(), Slot: d.Selection.Len() + i})
		}
		for _, column := range columns {
			if required[column.Key.Attribute] {
				input.Inputs = append(input.Inputs, column)
			}
		}
		if !eligible {
			ns, _ := read.Namespace(d.Namespace())
			relns, _ := read.Namespace(relation.Namespace())
			s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: fmt.Sprintf(`statistics object "%s.%s" could not be computed for relation "%s.%s"`, ns.Name(), d.Name(), relns.Name(), relation.Name()), Schema: relns.Name(), Table: relation.Name()})
			continue
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func (input *capturedExtendedInput) add(evaluation *exec.Context, collector *storage.ExtendedCollector, row []sqltype.Value) error {
	d := input.Definition
	for i := range d.Selection.Len() {
		input.dimensions[i] = row[input.simpleSlots[i]]
	}
	for i := range d.Expressions.Len() {
		frame := row
		if input.layouts[i] != nil {
			frame = input.frames[i]
			shared := false
			for j := range i {
				if slices.Equal(input.layouts[i], input.layouts[j]) {
					shared = true
					break
				}
			}
			if !shared {
				for j, slot := range input.layouts[i] {
					if slot >= 0 {
						frame[j] = row[slot]
					}
				}
			}
		}
		evaluation.Row = frame
		evaluation.RoutineRecipe = true
		evaluation.RoutineReferences = input.references[i]
		value, err := d.Expressions.At(i).Execution().Eval(evaluation)
		if err != nil {
			return err
		}
		input.dimensions[d.Selection.Len()+i] = value
	}
	return collector.Add(evaluation.Context, input.dimensions[:d.Selection.DimensionLen()])
}
