package sepgsql

import (
	"reflect"
	"slices"
	"testing"
)

func policyFixture() SimulationConfig {
	return SimulationConfig{Policy: Policy{ID: "unit-policy", Version: "1", Contexts: []string{"admin", "reader", "trusted", "public", "secret", "unlabeled"}, InitialContext: "admin", UnlabeledContext: "unlabeled", Rules: []Rule{{Source: "admin", Target: "*", Class: "*", Allow: []string{"*"}}, {Source: "reader", Target: "public", Class: "db_table", Allow: []string{"select", "lock"}}, {Source: "reader", Target: "public", Class: "db_column", Allow: []string{"select"}}, {Source: "reader", Target: "secret", Class: "db_table", Deny: []string{"select"}}}, CreateTransitions: []CreateTransition{{Source: "*", Parent: "public", Class: "db_table", Result: "public"}, {Source: "admin", Parent: "public", Class: "db_table", Name: "private", Result: "secret"}}, TrustedTransitions: []TrustedTransition{{Source: "reader", Procedure: "secret", Result: "trusted"}}, DefaultSpec: "db_table db.* public\ndb_table * secret\ndb_column db.*.secret secret\n", NamedSpecs: map[string]string{"quoted": "db_table db.\"A.B\".t public\n"}, Translations: []Translation{{Raw: "secret", Translated: "Secret display"}}}}
}
func TestSepgsqlSimulationPolicy(t *testing.T) {
	config := policyFixture()
	policy, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		source, target, class string
		required              Mask
		allow                 bool
		denied                []string
	}{
		{"admin", "secret", "db_column", Permission("db_column", "select"), true, nil},
		{"reader", "public", "db_table", Permission("db_table", "select") | Permission("db_table", "lock"), true, nil},
		{"reader", "public", "db_table", Permission("db_table", "update"), false, []string{"update"}},
		{"reader", "secret", "db_table", Permission("db_table", "select"), false, []string{"select"}},
		{"trusted", "public", "db_table", Permission("db_table", "select"), false, []string{"select"}},
	} {
		allowed, event := policy.Decide(test.source, test.target, test.class, "object", test.required, true)
		if allowed != test.allow || event == nil || !reflect.DeepEqual(event.Denied, test.denied) {
			t.Fatalf("decision %#v: %v %#v", test, allowed, event)
		}
	}
	// Union grants never defeat explicit denials, including wildcard grants.
	config.Policy.Rules = append(config.Policy.Rules, Rule{Source: "reader", Target: "*", Class: "*", Allow: []string{"*"}})
	first, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(config.Policy.Rules)
	second, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Runtime{first, second} {
		allowed, event := p.Decide("reader", "secret", "db_table", "", Permission("db_table", "select"), false)
		if allowed || event == nil || !reflect.DeepEqual(event.Denied, []string{"select"}) {
			t.Fatal("deny precedence", allowed, event)
		}
	}
	config = policyFixture()
	captured, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Policy.Contexts[0] = "corrupt"
	config.Policy.Rules[0].Allow[0] = "drop"
	config.Policy.CreateTransitions[1].Result = "public"
	config.Policy.NamedSpecs["quoted"] = "db_table * secret"
	config.Policy.Translations[0].Translated = "changed"
	if !captured.Valid("admin") || captured.Valid("corrupt") || captured.CreateContext("admin", "public", "db_table", "private") != "secret" || captured.CreateContext("reader", "secret", "db_schema", "x") != "secret" || captured.Translate("secret", false) != "Secret display" || captured.Translate("Secret display", true) != "secret" || captured.Translate("unknown", false) != "unknown" {
		t.Fatal("compiled input ownership or transitions")
	}
	if context, transition := captured.TrustedContext("reader", "secret"); !transition || context != "trusted" {
		t.Fatal(context, transition)
	}
	if context, transition := captured.TrustedContext("reader", "public"); transition || context != "reader" {
		t.Fatal(context, transition)
	}
	if label, ok := captured.InitialLabel(nil, "db_table", "db.a/b.t"); !ok || label != "public" {
		t.Fatal("fnmatch spans separators/first match", label, ok)
	}
	if label, ok := captured.InitialLabel(nil, "db_table", "other.t"); !ok || label != "secret" {
		t.Fatal(label, ok)
	}
	if _, ok := captured.InitialLabel(nil, "db_view", "db.t"); ok {
		t.Fatal("spec crossed class")
	}
	profile := "quoted"
	if label, ok := captured.InitialLabel(&profile, "db_table", `db."A.B".t`); !ok || label != "public" {
		t.Fatal("named spec ownership", label, ok)
	}
	for _, bad := range []func(*SimulationConfig){
		func(c *SimulationConfig) { c.Policy.ID = "" }, func(c *SimulationConfig) { c.Policy.Version = "" },
		func(c *SimulationConfig) { c.Policy.Contexts = append(c.Policy.Contexts, "admin") }, func(c *SimulationConfig) { c.Policy.Contexts = append(c.Policy.Contexts, "*") }, func(c *SimulationConfig) { c.Policy.InitialContext = "missing" }, func(c *SimulationConfig) { c.Policy.UnlabeledContext = "" },
		func(c *SimulationConfig) { c.Policy.Rules[0].Source = "unknown" }, func(c *SimulationConfig) { c.Policy.Rules[0].Class = "db_tuple" }, func(c *SimulationConfig) { c.Policy.Rules[0].Allow = []string{"select"} }, func(c *SimulationConfig) { c.Policy.Rules[1].Allow = []string{"select", "select"} },
		func(c *SimulationConfig) {
			c.Policy.CreateTransitions = append(c.Policy.CreateTransitions, CreateTransition{Source: "admin", Parent: "public", Class: "db_table", Name: "private", Result: "public"})
		},
		func(c *SimulationConfig) {
			c.Policy.TrustedTransitions = append(c.Policy.TrustedTransitions, TrustedTransition{Source: "reader", Procedure: "secret", Result: "public"})
		},
		func(c *SimulationConfig) { c.Policy.DefaultSpec = "db_table [bad public" }, func(c *SimulationConfig) { c.Policy.DefaultSpec = "db_table * invalid" }, func(c *SimulationConfig) { c.Policy.DefaultSpec = "db_language * public" },
		func(c *SimulationConfig) {
			c.Policy.Translations = append(c.Policy.Translations, Translation{Raw: "secret", Translated: "other"})
		}, func(c *SimulationConfig) {
			c.Policy.Translations = append(c.Policy.Translations, Translation{Raw: "public", Translated: "Secret display"})
		},
	} {
		invalid := policyFixture()
		bad(&invalid)
		if compiled, err := Compile(invalid); err == nil || compiled != nil {
			t.Fatalf("accepted invalid policy: %#v", invalid)
		}
	}
	empty := policyFixture()
	empty.Policy.Rules = nil
	deny, err := Compile(empty)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, _ := deny.Decide("admin", "public", "db_table", "", Permission("db_table", "select"), false); allowed {
		t.Fatal("empty rules granted superuser-like authority")
	}
	for _, test := range []struct {
		pattern, input string
		match          bool
	}{{"a?c", "abc", true}, {"a[!x-z]c", "abc", true}, {"a[x-z]c", "abc", false}, {`a\*c`, "a*c", true}, {"*", "a/b.c", true}, {"db.*.t", "db.a.b.t", true}} {
		pattern, err := compilePattern(test.pattern)
		if err != nil || pattern.MatchString(test.input) != test.match {
			t.Fatal(test, err)
		}
	}
}
func TestSepgsqlSimulationPatternEscapedBrackets(t *testing.T) {
	for _, test := range []struct {
		pattern, input string
		match          bool
	}{
		{`[a\-z]`, "a", true},
		{`[a\-z]`, "-", true},
		{`[a\-z]`, "z", true},
		{`[a\-z]`, "b", false},
		{`[!a\-z]`, "a", false},
		{`[!a\-z]`, "-", false},
		{`[!a\-z]`, "z", false},
		{`[!a\-z]`, "b", true},
		{`[^a\-z]`, "-", false},
		{`[^a\-z]`, "b", true},
		{`[a-z]`, "b", true},
		{`[a-z]`, "-", false},
		{`[!a-z]`, "b", false},
		{`[!a-z]`, "-", true},
		{`[\]]`, "]", true},
		{`[\[]`, "[", true},
		{`[\^]`, "^", true},
		{`[\\]`, `\`, true},
	} {
		t.Run(test.pattern+"/"+test.input, func(t *testing.T) {
			pattern, err := compilePattern(test.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if match := pattern.MatchString(test.input); match != test.match {
				t.Fatalf("pattern %q on %q: got %t, want %t", test.pattern, test.input, match, test.match)
			}
		})
	}
	config := policyFixture()
	spec := "db_table db.public.[a\\-z] secret\ndb_table db.public.[a-z] public\ndb_table * unlabeled\n"
	config.Policy.DefaultSpec = spec
	config.Policy.NamedSpecs["escaped"] = spec
	policy, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	profile := "escaped"
	for _, name := range []*string{nil, &profile} {
		for _, test := range []struct {
			key, label string
		}{
			{"db.public.a", "secret"},
			{"db.public.-", "secret"},
			{"db.public.z", "secret"},
			{"db.public.b", "public"},
			{"db.public.other", "unlabeled"},
		} {
			if label, ok := policy.InitialLabel(name, "db_table", test.key); !ok || label != test.label {
				t.Fatalf("first-match label for %q: got %q, %t; want %q", test.key, label, ok, test.label)
			}
		}
	}
}

func TestSepgsqlSimulationAudit(t *testing.T) {
	config := policyFixture()
	config.Permissive = true
	var delivered AuditEvent
	config.Audit = func(event AuditEvent) { delivered = event; event.Denied[0] = "tampered" }
	policy, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	allowed, event := policy.Decide("reader", "secret", "db_table", "db.public.t", Permission("db_table", "select"), false)
	want := "SELinux: denied { select } scontext=reader tcontext=secret tclass=db_table name=\"db.public.t\" permissive=1"
	if !allowed || event == nil || event.Message != want || !event.Permissive || !reflect.DeepEqual(event.Required, []string{"select"}) || !reflect.DeepEqual(event.Denied, []string{"select"}) || !reflect.DeepEqual(event.Audited, []string{"select"}) {
		t.Fatal(allowed, event)
	}
	policy.Deliver(*event)
	if delivered.Message != want || event.Denied[0] != "select" {
		t.Fatal("audit borrowed decision storage", delivered, event)
	}
	suppressed := policyFixture()
	suppressed.SuppressDeniedAudit = true
	p, err := Compile(suppressed)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, event := p.Decide("reader", "secret", "db_table", "", Permission("db_table", "select"), false); allowed || event != nil {
		t.Fatal("suppression changed permission", allowed, event)
	}
	if allowed, event := p.Decide("reader", "secret", "db_table", "", Permission("db_table", "select"), true); allowed || event == nil || !reflect.DeepEqual(event.Audited, []string{"select"}) {
		t.Fatal("debug changed permission or lost audit", allowed, event)
	}
}
