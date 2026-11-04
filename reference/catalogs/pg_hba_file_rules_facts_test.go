package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
)

func hbaLocalFact(file, method string, options ...string) stuntgres.HBARule {
	return stuntgres.HBARule{
		RuleNumber: new(int32(1)), FileName: new(file), LineNumber: new(int32(1)),
		Type: new("local"), Database: []string{"all"}, UserName: []string{"all"},
		AuthMethod: new(method), Options: options,
	}
}

// Each declaration explicitly supplies PostgreSQL bytes and native facts.
// These constructors never inspect bytes or turn SQL expectations into input.
func hbaLocalSet(contents, method string, options ...string) fixture.HBARuleSet {
	return fixture.HBARuleSet{Contents: contents, Rules: []stuntgres.HBARule{hbaLocalFact("{MAIN}", method, options...)}}
}

func hbaCanonicalSet() fixture.HBARuleSet {
	return fixture.HBARuleSet{
		Contents: hbaCanonical,
		Manifest: map[string]string{"child.conf": "host db1,db2 +staff,.user .example.test trust\n"},
		Rules: []stuntgres.HBARule{
			{RuleNumber: new(int32(1)), FileName: new("{MAIN}"), LineNumber: new(int32(2)), Type: new("local"), Database: []string{"all"}, UserName: []string{"all"}, AuthMethod: new("peer"), Options: []string{"map=new"}},
			{RuleNumber: new(int32(2)), FileName: new("{ROOT}/child.conf"), LineNumber: new(int32(1)), Type: new("host"), Database: []string{"db1", "db2"}, UserName: []string{"+staff", ".user"}, Address: new(".example.test"), AuthMethod: new("trust")},
			{RuleNumber: new(int32(3)), FileName: new("{MAIN}"), LineNumber: new(int32(4)), Type: new("host"), Database: []string{"all"}, UserName: []string{"all"}, Address: new("192.0.2.129"), Netmask: new("255.255.255.0"), AuthMethod: new("scram-sha-256")},
			{FileName: new("{MAIN}"), LineNumber: new(int32(5)), Error: new(`invalid connection type "bad"`)},
		},
	}
}
