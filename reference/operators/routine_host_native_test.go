package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type hostNativeRuntime struct {
	mu       sync.Mutex
	gate     func(context.Context) error
	retained stuntgres.HostHstoreMap
}

func hostMapObject(args []stuntgres.HostObject) (stuntgres.HostHstoreMap, error) {
	if len(args) != 1 || args[0].Null {
		return nil, errors.New("host: expected nonnull map argument")
	}
	m, ok := args[0].Value.(stuntgres.HostHstoreMap)
	if !ok || m == nil {
		return nil, errors.New("host: expected owned map object")
	}
	return m, nil
}
func hostIncrement(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
	m, err := hostMapObject(args)
	if err != nil {
		return stuntgres.HostObject{}, err
	}
	text := "0"
	if a, ok := m["a"]; ok {
		if a.Null {
			return stuntgres.HostObject{}, errors.New("host: null numeric entry")
		}
		text = a.Text
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return stuntgres.HostObject{}, err
	}
	m["a"] = stuntgres.HostHstoreValue{Text: strconv.Itoa(n + 1)}
	m["c"] = stuntgres.HostHstoreValue{Text: "3"}
	return stuntgres.HostObject{Value: m}, nil
}
func hostDouble(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
	m, err := hostMapObject(args)
	if err != nil {
		return stuntgres.HostObject{}, err
	}
	a, ok := m["a"]
	if !ok || a.Null {
		return stuntgres.HostObject{}, errors.New("host: missing numeric entry")
	}
	n, err := strconv.Atoi(a.Text)
	if err != nil {
		return stuntgres.HostObject{}, err
	}
	m["a"] = stuntgres.HostHstoreValue{Text: strconv.Itoa(2 * n)}
	m["d"] = stuntgres.HostHstoreValue{Text: "second"}
	return stuntgres.HostObject{Value: m}, nil
}
func hostFromMap(_ context.Context, v stuntgres.HostSQLValue) (stuntgres.HostObject, error) {
	if v.Type != stuntgres.HostHstore {
		return stuntgres.HostObject{}, errors.New("host: wrong input SQL type")
	}
	if v.Null {
		return stuntgres.HostObject{Null: true}, nil
	}
	for _, entry := range v.Hstore {
		switch entry.Key {
		case "from_wrong_object":
			return stuntgres.HostObject{Value: 17}, nil
		case "from_null_payload":
			return stuntgres.HostObject{Null: true, Value: stuntgres.HostHstoreMap{}}, nil
		}
	}
	m := make(stuntgres.HostHstoreMap, len(v.Hstore))
	for _, entry := range v.Hstore {
		if entry.Key == "from_error" {
			return stuntgres.HostObject{}, errors.New("host: FROM SQL failed")
		}
		m[entry.Key] = stuntgres.HostHstoreValue{Text: entry.Value, Null: entry.Null}
	}
	return stuntgres.HostObject{Value: m}, nil
}
func hostToMap(_ context.Context, v stuntgres.HostObject) (stuntgres.HostSQLValue, error) {
	if v.Null {
		return stuntgres.HostSQLValue{Type: stuntgres.HostHstore, Null: true}, nil
	}
	m, ok := v.Value.(stuntgres.HostHstoreMap)
	if !ok || m == nil {
		return stuntgres.HostSQLValue{}, errors.New("host: result is not a nonnil map")
	}
	if _, ok := m["to_error"]; ok {
		return stuntgres.HostSQLValue{}, errors.New("host: TO SQL failed")
	}
	if _, ok := m["wrong_result"]; ok {
		return stuntgres.HostSQLValue{Type: stuntgres.HostText, Text: "wrong SQL type"}, nil
	}
	if _, ok := m["sql_null_payload"]; ok {
		return stuntgres.HostSQLValue{Type: stuntgres.HostHstore, Null: true, Hstore: []stuntgres.HostHstoreEntry{}}, nil
	}
	if _, ok := m["sql_extra_payload"]; ok {
		return stuntgres.HostSQLValue{Type: stuntgres.HostHstore, Text: "contradiction", Hstore: []stuntgres.HostHstoreEntry{}}, nil
	}
	if _, ok := m["sql_nil_entries"]; ok {
		return stuntgres.HostSQLValue{Type: stuntgres.HostHstore}, nil
	}
	entries := make([]stuntgres.HostHstoreEntry, 0, len(m))
	for key, value := range m {
		entries = append(entries, stuntgres.HostHstoreEntry{Key: key, Value: value.Text, Null: value.Null})
	}
	return stuntgres.HostSQLValue{Type: stuntgres.HostHstore, Hstore: entries}, nil
}
func hostFromEntries(_ context.Context, v stuntgres.HostSQLValue) (stuntgres.HostObject, error) {
	if v.Null {
		return stuntgres.HostObject{Null: true}, nil
	}
	return stuntgres.HostObject{Value: slices.Clone(v.Hstore)}, nil
}
func hostToEntries(_ context.Context, v stuntgres.HostObject) (stuntgres.HostSQLValue, error) {
	if v.Null {
		return stuntgres.HostSQLValue{Type: stuntgres.HostHstore, Null: true}, nil
	}
	entries, ok := v.Value.([]stuntgres.HostHstoreEntry)
	if !ok || entries == nil {
		return stuntgres.HostSQLValue{}, errors.New("host: result is not an entry list")
	}
	return stuntgres.HostSQLValue{Type: stuntgres.HostHstore, Hstore: entries}, nil
}
func (runtime *hostNativeRuntime) definition() stuntgres.HostLanguageDefinition {
	program := func(id string, f stuntgres.HostProgramFunc) stuntgres.HostProgramDefinition {
		return stuntgres.HostProgramDefinition{ID: id, Version: "1", Inputs: []stuntgres.HostType{stuntgres.HostHstore}, Result: stuntgres.HostHstore, InputConversions: []string{"map"}, ResultConversion: "map", Execute: f}
	}
	copyMap := func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		m, err := hostMapObject(args)
		if err != nil {
			return stuntgres.HostObject{}, err
		}
		out := make(stuntgres.HostHstoreMap, len(m))
		for k, v := range m {
			out[k] = v
		}
		return stuntgres.HostObject{Value: out}, nil
	}
	nullProgram := func(ctx context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		if args[0].Null {
			return stuntgres.HostObject{Value: stuntgres.HostHstoreMap{"n": {Text: "handled"}}}, nil
		}
		return copyMap(ctx, args)
	}
	fail := func(context.Context, []stuntgres.HostObject) (stuntgres.HostObject, error) {
		return stuntgres.HostObject{}, errors.New("host: program failed")
	}
	wrong := func(context.Context, []stuntgres.HostObject) (stuntgres.HostObject, error) {
		return stuntgres.HostObject{Value: 7}, nil
	}
	wrongList := func(context.Context, []stuntgres.HostObject) (stuntgres.HostObject, error) {
		return stuntgres.HostObject{Value: []string{"not", "a map"}}, nil
	}
	wrongResult := func(context.Context, []stuntgres.HostObject) (stuntgres.HostObject, error) {
		return stuntgres.HostObject{Value: stuntgres.HostHstoreMap{"wrong_result": {Text: "yes"}}}, nil
	}
	nilMap := func(ctx context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		m, err := hostMapObject(args)
		if err != nil {
			return stuntgres.HostObject{}, err
		}
		if _, bad := m["bad"]; bad {
			return stuntgres.HostObject{Value: stuntgres.HostHstoreMap(nil)}, nil
		}
		return hostIncrement(ctx, args)
	}
	nullPayload := func(ctx context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		m, err := hostMapObject(args)
		if err != nil {
			return stuntgres.HostObject{}, err
		}
		if _, bad := m["bad"]; bad {
			return stuntgres.HostObject{Null: true, Value: m}, nil
		}
		return hostIncrement(ctx, args)
	}
	wait := func(ctx context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		result, err := hostIncrement(ctx, args)
		if err != nil {
			return result, err
		}
		runtime.mu.Lock()
		gate := runtime.gate
		runtime.mu.Unlock()
		if gate == nil {
			return stuntgres.HostObject{}, errors.New("host: wait gate not configured")
		}
		if err = gate(ctx); err != nil {
			return stuntgres.HostObject{}, err
		}
		return result, nil
	}
	retain := func(ctx context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		result, err := hostIncrement(ctx, args)
		if err == nil {
			runtime.mu.Lock()
			runtime.retained = result.Value.(stuntgres.HostHstoreMap)
			runtime.mu.Unlock()
		}
		return result, err
	}
	defaults := program("text-default-v1", func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		entries, ok := args[0].Value.([]stuntgres.HostHstoreEntry)
		if !ok {
			return stuntgres.HostObject{}, errors.New("host: expected actual default entry-list object")
		}
		return stuntgres.HostObject{Value: slices.Clone(entries)}, nil
	})
	defaults.InputConversions = []string{""}
	defaults.ResultConversion = ""
	definition := stuntgres.HostLanguageDefinition{Provider: "reference-go-host", ABIVersion: "map-v1", Name: "go_host", HandlerName: stuntgres.HostName{Schema: "public", Name: "go_host_call_handler"}, Handler: func(ctx context.Context, p stuntgres.HostProgramFunc, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		if len(args) == 1 {
			if m, ok := args[0].Value.(stuntgres.HostHstoreMap); ok {
				if _, fail := m["handler_error"]; fail {
					return stuntgres.HostObject{}, errors.New("host: handler failed")
				}
			}
		}
		return p(ctx, args)
	}, Programs: []stuntgres.HostProgramDefinition{program("increment-v1", hostIncrement), program("double-v1", hostDouble), program("copy-v1", copyMap), program("null-v1", nullProgram), program("fail-v1", fail), program("raise-v1", fail), program("wrong-object-v1", wrong), program("wrong-list-v1", wrongList), program("wrong-result-v1", wrongResult), program("wait-v1", wait), program("retain-v1", retain), defaults}, Conversions: []stuntgres.HostConversionDefinition{{ID: "map", Type: stuntgres.HostHstore, Bootstrap: true, FromName: stuntgres.HostName{Schema: "public", Name: "go_host_from_hstore"}, ToName: stuntgres.HostName{Schema: "public", Name: "go_host_to_hstore"}, FromSQL: hostFromMap, ToSQL: hostToMap}, {ID: "entries", Type: stuntgres.HostHstore, FromName: stuntgres.HostName{Schema: "public", Name: "go_host_default_from"}, ToName: stuntgres.HostName{Schema: "public", Name: "go_host_default_to"}, FromSQL: hostFromEntries, ToSQL: hostToEntries}}, Defaults: []stuntgres.HostDefaultDefinition{{Type: stuntgres.HostHstore, FromID: "entries", ToID: "entries"}}}
	definition.Programs = append(definition.Programs, program("nil-map-v1", nilMap), program("null-payload-v1", nullPayload))
	return definition
}
func hostNativeError(t *testing.T, ctx context.Context, c *sql.Conn, q, message string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		var value any
		var err error
		if prepared {
			stmt, failure := c.PrepareContext(ctx, q)
			if failure != nil {
				err = failure
			} else {
				err = stmt.QueryRowContext(ctx, args...).Scan(&value)
				stmt.Close()
			}
		} else {
			err = c.QueryRowContext(ctx, q, args...).Scan(&value)
		}
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Errorf("%s prepared=%v error=%v want %q", q, prepared, err, message)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Errorf("native ABI error fabricated SQLSTATE: %v", err)
		}
	}
}
func hostNativeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *hostNativeRuntime)) {
	t.Helper()
	runtime := new(hostNativeRuntime)
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error { return registerHostNative(d, runtime) })
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer routineLanguagesCleanup(t, c, "DROP EXTENSION IF EXISTS hstore_plpython3u CASCADE", "DROP EXTENSION IF EXISTS plpython3u CASCADE")()
			hostExec(t, p, b, c, "CREATE EXTENSION hstore")
			if b.PG {
				hostExec(t, p, b, c, "CREATE EXTENSION plpython3u")
				hostExec(t, p, b, c, "CREATE EXTENSION hstore_plpython3u")
			}
			body(t, p, b, c, runtime)
		})
	}
}
