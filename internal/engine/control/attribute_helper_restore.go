package control

import (
	"slices"
	"stuntgres/internal/parser"
)

type AttributeDetachPair struct{ Child, Helper AttributeDropTarget }

func retiredAttributeHelper(c *attributeConstructor) bool {
	if c == nil || c.closed || !c.staged || len(c.parents) != 0 || len(c.columns) == 0 {
		return false
	}
	for _, a := range c.columns {
		if !a.Dropped || !a.Placeholder || !a.Local {
			return false
		}
	}
	return true
}
func pairAttributeHelperDetach(s *parser.AlterTable, constructors map[AttributeDropTarget]*attributeConstructor, required map[AttributeDropTarget]bool) (AttributeDetachPair, bool) {
	if s.IfExists || s.Only || len(s.Actions) != 1 || s.Actions[0].Action != parser.NoInheritTable {
		return AttributeDetachPair{}, false
	}
	child, ok := attributeTarget(s.Name)
	if !ok {
		return AttributeDetachPair{}, false
	}
	helper, ok := attributeTarget(s.Actions[0].Parent)
	if !ok || !AttributeHelperName(helper.Table) || !required[helper] || !retiredAttributeHelper(constructors[helper]) {
		return AttributeDetachPair{}, false
	}
	c := constructors[child]
	if c == nil || c.closed || !slices.Contains(c.parents, helper) {
		return AttributeDetachPair{}, false
	}
	return AttributeDetachPair{child, helper}, true
}
func pairAttributeHelperTeardown(s *parser.DropTable, constructors map[AttributeDropTarget]*attributeConstructor, required map[AttributeDropTarget]bool) (AttributeDropTarget, bool) {
	if s.IfExists || s.Mode != parser.DropRestrict || len(s.Names) != 1 {
		return AttributeDropTarget{}, false
	}
	helper, ok := attributeTarget(s.Names[0])
	if !ok || !AttributeHelperName(helper.Table) || !required[helper] || !retiredAttributeHelper(constructors[helper]) {
		return AttributeDropTarget{}, false
	}
	for _, c := range constructors {
		if slices.Contains(c.parents, helper) {
			return AttributeDropTarget{}, false
		}
	}
	return helper, true
}
