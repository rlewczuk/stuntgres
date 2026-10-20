package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func (c *consumerContext) partitionContextPlural() string {
	if c.PartitionConsumer == "partition key expression" {
		return "partition key expressions"
	}
	return c.PartitionConsumer
}

func (c *consumerContext) partitionExpressionError(code, message string, pos parser.Position) error {
	if c.PartitionConsumer == "partition key expression" {
		return sqlError(code, message)
	}
	return expr.ErrorAt(code, message, pos)
}
