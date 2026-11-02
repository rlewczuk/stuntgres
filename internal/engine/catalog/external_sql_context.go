package catalog

import "context"

// The key is engine-private. Only external-bound execution gateways tag their
// callback context; ordinary SQL and planning allocate nothing for this feature.
// The concrete capability contains its exact operation and physical token.
type externalSQLContextKey struct{}

func ExternalSQLExecutionContext(ctx context.Context, capability ExternalSQLSession) context.Context {
	if capability == nil {
		return ctx
	}
	return context.WithValue(ctx, externalSQLContextKey{}, capability)
}

func ExternalSQLContextMatches(ctx context.Context, capability ExternalSQLSession) bool {
	return ctx.Value(externalSQLContextKey{}) == capability
}

func ExternalSQLContextCapability(ctx context.Context) ExternalSQLSession {
	capability, _ := ctx.Value(externalSQLContextKey{}).(ExternalSQLSession)
	return capability
}

func WithoutExternalSQLContext(ctx context.Context) context.Context {
	if ExternalSQLContextCapability(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, externalSQLContextKey{}, struct{}{})
}
