package stuntgres

import (
	"context"

	"stuntgres/internal/workadmission"
)

// WorkClass names one semantic quantum of cooperative provider work.
// Classes are not uniform CPU, memory, time, or complete query accounting units.
type WorkClass = workadmission.Class

// WorkAdmission grants one quantum or refuses it with an unchanged error.
// A callback may wait for application workload credit, must honor the supplied
// effective operation context, and must be concurrency-safe. Credits are consumed
// per quantum, not held as lifetime permits across nested provider calls. Do not
// reenter the busy sql.Conn; use a different connection for independent work.
type WorkAdmission = workadmission.Admission

const (
	WorkPatternNormalize    = workadmission.WorkPatternNormalize
	WorkPatternMatch        = workadmission.WorkPatternMatch
	WorkJSONArrayCount      = workadmission.WorkJSONArrayCount
	WorkJSONObjectExpand    = workadmission.WorkJSONObjectExpand
	WorkJSONStripNulls      = workadmission.WorkJSONStripNulls
	WorkJSONBContainment    = workadmission.WorkJSONBContainment
	WorkJSONBObjectExpand   = workadmission.WorkJSONBObjectExpand
	WorkJSONBArrayExpand    = workadmission.WorkJSONBArrayExpand
	WorkJSONBExistence      = workadmission.WorkJSONBExistence
	WorkJSONBPathDelete     = workadmission.WorkJSONBPathDelete
	WorkJSONBPretty         = workadmission.WorkJSONBPretty
	WorkArrayTransform      = workadmission.WorkArrayTransform
	WorkJSONRecordExpand    = workadmission.WorkJSONRecordExpand
	WorkJSONBRecordPopulate = workadmission.WorkJSONBRecordPopulate
	WorkCalendarArithmetic  = workadmission.WorkCalendarArithmetic
	WorkEncodingConvert     = workadmission.WorkEncodingConvert
	WorkPasswordBcrypt      = workadmission.WorkPasswordBcrypt
	WorkPasswordSHA         = workadmission.WorkPasswordSHA
	WorkAggregateInput      = workadmission.WorkAggregateInput
	WorkRecursiveUnion      = workadmission.WorkRecursiveUnion
	WorkTSVectorConvert     = workadmission.WorkTSVectorConvert
)

// WithWorkAdmission attaches a request-scoped workload-credit policy. A nil
// policy disables admission. Attachment preserves cancellation, deadlines,
// causes, and unrelated values. Policies apply only to selected actual runtime
// provider work, never binding or locked publication, and are not retained by
// prepared descriptions or immutable values. Cached work need not consume credit.
func WithWorkAdmission(ctx context.Context, admission WorkAdmission) context.Context {
	return workadmission.With(ctx, admission)
}
