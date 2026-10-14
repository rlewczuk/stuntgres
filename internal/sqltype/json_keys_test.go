package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func jsonKeysUnitValues(ctx context.Context, value Value, textType *Descriptor) ([]Value, error) {
	plan, err := BindJSONKeys("json_object_keys", value.Type().Usage(), textType.Usage())
	if err != nil {
		return nil, err
	}
	cursor, err := plan.Open(ctx, value)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()
	var keys []Value
	for {
		key, present, err := cursor.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !present {
			return keys, nil
		}
		keys = append(keys, key)
	}
}

func TestJSONObjectKeysValues(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		input string
		want []string
	}{
		{`{}`, nil},
		{`{"a":1,"a":2,"\u0061":3,"nested":{"ignored":4}}`, []string{"a", "a", "a", "nested"}},
		{`{"":0,"a\"b":1,"x\\y":2,"\u00e9":3,"\uD83D\uDE04":4}`, []string{"", `a"b`, `x\y`, "é", "😄"}},
		{`{"huge":1e1000000}`, []string{"huge"}},
		{`{"deep":` + strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000) + "}", []string{"deep"}},
	} {
		value, err := types[JSON].Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		keys, err := jsonKeysUnitValues(context.Background(), value, types[Text])
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, key := range keys {
			if key.Type() != types[Text] || key.IsNull() {
				t.Fatal("key lost generation-owned TEXT identity", key)
			}
			text, err := key.Text()
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, text)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("keys=%q want=%q", got, tc.want)
		}
		stored, err := value.JSONText()
		if err != nil || stored != tc.input {
			t.Fatal("processing changed storage", stored, err)
		}
	}
}

func TestJSONObjectKeysCompleteProcessing(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct{ input, code string }{
		{`{"ok":1,"late":"\u0000"}`, "22P05"},
		{`{"ok":1,"nested":{"\u0000":1}}`, "22P05"},
		{`{"ok":1,"nested":["\uD800"]}`, "22P02"},
		{`{"ok":1,"nested":{"\uDC00":1}}`, "22P02"},
		{`[]`, "22023"},
		{`null`, "22023"},
	} {
		value, err := types[JSON].Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal("JSON storage must still accept the document", err)
		}
		keys, err := jsonKeysUnitValues(context.Background(), value, types[Text])
		var diagnostic *sqlstate.Error
		if keys != nil || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatalf("partial keys=%v error=%v want=%s", keys, err, tc.code)
		}
	}
	for _, value := range []Value{String(types[Text], "{}")} {
		if _, err := jsonKeysUnitValues(context.Background(), value, types[Text]); err == nil {
			t.Fatal("invalid provider input admitted")
		}
	}
	value, err := types[JSON].Input(context.Background(), `{}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jsonKeysUnitValues(context.Background(), value, types[Int4]); err == nil {
		t.Fatal("non-TEXT result admitted")
	}
}

func TestJSONObjectKeysCancellation(t *testing.T) {
	types := testTypes()
	for _, input := range []string{
		`{"a":"` + strings.Repeat(`\u0041`, 10000) + `"}`,
		`{"a":` + strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000) + "}",
	} {
		value, err := types[JSON].Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if keys, err := jsonKeysUnitValues(ctx, value, types[Text]); keys != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("precanceled processing", keys, err)
		}
		ctxDuring := &groupingCancelContext{Context: context.Background(), at: 5}
		if keys, err := jsonKeysUnitValues(ctxDuring, value, types[Text]); keys != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("processing cancellation", keys, err)
		}
	}
}
