package stuntgres

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatisticExpressionValueDriverBoundary(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	rowType, arrayType := types[10029], types[10028]
	fields := make([]sqltype.Value, rowType.CompositeLen())
	for i := range fields {
		d := rowType.CompositeMember(i).Usage.Type
		switch d.ID() {
		case sqltype.Oid:
			fields[i], _ = sqltype.OIDValue(d, 0)
		case sqltype.Int2, sqltype.Int4:
			fields[i], _ = sqltype.Integer(d, 0)
		case sqltype.Float4:
			fields[i], _ = sqltype.Float(d, 0)
		case sqltype.Bool:
			fields[i] = sqltype.Boolean(d, false)
		default:
			fields[i] = sqltype.ResultNull(d.Usage())
		}
	}
	ctx := context.Background()
	concrete, err := types[1007].Input(ctx, "{2,3}", -1)
	if err != nil {
		t.Fatal(err)
	}
	slot, err := sqltype.StatisticArrayValue(types[2277], concrete, types[sqltype.Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	fields[26] = slot
	fields[6], _ = sqltype.Integer(types[sqltype.Int2], 2)
	fields[11], _ = sqltype.OIDValue(types[sqltype.Oid], 97)
	row, err := sqltype.StatisticRowValue(rowType, fields)
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.StatisticRowsValue(arrayType, []sqltype.Value{row})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := exportValue(ctx, array, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	text, ok := actual.(string)
	if !ok || !strings.HasPrefix(text, `{"(`) || !strings.HasSuffix(text, `)"}`) {
		t.Fatal("native row-array transport", actual)
	}
	if scanType(arrayType) != reflect.TypeFor[string]() || scanType(rowType) != reflect.TypeFor[string]() {
		t.Fatal("native statistic ScanType")
	}
	if arrayType.DatabaseName() != "_PG_STATISTIC" {
		t.Fatal(arrayType.DatabaseName())
	}
	actual, err = exportValue(ctx, slot, sqltype.OutputEnvironment{})
	if err != nil || actual != "{2,3}" {
		t.Fatal("typed slot transport", actual, err)
	}
	if _, err := importParameter(ctx, text, arrayType.Usage(), sqltype.InputEnvironment{}); err == nil {
		t.Fatal("external statistic input admitted")
	}
}
