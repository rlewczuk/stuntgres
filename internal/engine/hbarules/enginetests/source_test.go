package enginetests_test

import (
	"reflect"
	"stuntgres/internal/engine/hbarules"
	"sync"
	"testing"
)

func TestHBARulesSnapshotOwnership(t *testing.T) {
	var absent *hbarules.Source
	if absent.Capture() != nil || absent.Capture().Rules() != nil {
		t.Fatal("nil source is not empty")
	}
	source := new(hbarules.Source)
	if source.Capture() != nil {
		t.Fatal("zero source is not empty")
	}
	makeRules := func() []hbarules.Rule {
		rule := hbarules.Rule{RuleNumber: new(int32(0)), FileName: new(""), LineNumber: new(int32(-7)), Type: new("arbitrary"), Database: []string{}, UserName: []string{"", "second"}, Address: new("address"), Netmask: new("netmask"), AuthMethod: new("method"), Options: []string{"option"}, Error: new("warning")}
		return []hbarules.Rule{rule, {}, rule}
	}
	input, want := makeRules(), makeRules()
	source.Set(input)
	first := source.Capture()
	if first == nil || first != source.Capture() || !reflect.DeepEqual(first.Rules(), want) {
		t.Fatal("not one immutable capture with verbatim order and duplicates")
	}
	owned := first.Rules()
	if &owned[0] == &input[0] || owned[0].RuleNumber == input[0].RuleNumber || owned[0].FileName == input[0].FileName || owned[0].LineNumber == input[0].LineNumber || owned[0].Type == input[0].Type || owned[0].Address == input[0].Address || owned[0].Netmask == input[0].Netmask || owned[0].AuthMethod == input[0].AuthMethod || owned[0].Error == input[0].Error || &owned[0].UserName[0] == &input[0].UserName[0] || &owned[0].Options[0] == &input[0].Options[0] {
		t.Fatal("publication borrowed caller backing")
	}
	if &source.Capture().Rules()[0] != &owned[0] || source.Capture().Rules()[0].FileName != owned[0].FileName || &source.Capture().Rules()[0].UserName[0] != &owned[0].UserName[0] {
		t.Fatal("capture repeatedly cloned immutable facts")
	}
	*input[0].RuleNumber, *input[0].LineNumber = 99, 99
	for _, scalar := range []*string{input[0].FileName, input[0].Type, input[0].Address, input[0].Netmask, input[0].AuthMethod, input[0].Error} {
		*scalar = "caller mutation"
	}
	input[0].Database = append(input[0].Database, "new")
	input[0].UserName[0], input[0].Options[0] = "changed", "changed"
	input[0], input[1], input[2] = hbarules.Rule{}, hbarules.Rule{}, hbarules.Rule{}
	if !reflect.DeepEqual(first.Rules(), want) || owned[0].Database == nil || owned[1].Database != nil {
		t.Fatal("caller mutation or NULL/empty collapse")
	}
	source.Set(makeRules())
	second := source.Capture()
	if second == first || &second.Rules()[0] == &owned[0] || second.Rules()[0].FileName == owned[0].FileName {
		t.Fatal("new publication reused old backing")
	}
	for _, clear := range [][]hbarules.Rule{nil, {}} {
		source.Set(clear)
		if len(source.Capture().Rules()) != 0 || !reflect.DeepEqual(first.Rules(), want) || !reflect.DeepEqual(second.Rules(), want) {
			t.Fatal("clear mutated retained generations")
		}
	}
}

func TestHBARulesSnapshotConcurrentPublication(t *testing.T) {
	source := new(hbarules.Source)
	batch := func(n int32, label string) []hbarules.Rule {
		return []hbarules.Rule{
			{RuleNumber: &n, FileName: &label, LineNumber: &n, Type: &label, Database: []string{label, ""}, UserName: []string{}, Address: &label, Netmask: &label, AuthMethod: &label, Options: []string{label}, Error: &label},
			{RuleNumber: &n, FileName: &label, Database: []string{label}},
		}
	}
	a, b := batch(1, "A"), batch(-2, "B")
	source.Set(a)
	retained := source.Capture()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, input := range [][]hbarules.Rule{a, b} {
		workers.Go(func() {
			<-start
			for range 200 {
				source.Set(input)
				source.Set(nil)
			}
		})
	}
	for range 4 {
		workers.Go(func() {
			<-start
			for range 500 {
				snapshot := source.Capture()
				rules := snapshot.Rules()
				if len(rules) != 0 && !reflect.DeepEqual(rules, a) && !reflect.DeepEqual(rules, b) {
					t.Error("reader observed incomplete publication", rules)
					return
				}
			}
		})
	}
	close(start)
	workers.Wait()
	if !reflect.DeepEqual(retained.Rules(), a) {
		t.Fatal("concurrent publication mutated retained generation")
	}
}
