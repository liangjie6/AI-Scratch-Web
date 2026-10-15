package runstore

// This file declares the parameter structs for the RunStore methods. Python
// declares every store method with keyword-only arguments, several of them
// optional; Go has no keyword arguments or defaults, so each method takes one
// struct. Optional arguments become pointer fields (nil = Python's None) and
// the two attempt parameters treat the zero value as Python's default of 1.

// CreateRunParams are the arguments of RunStore.CreateRun.
type CreateRunParams struct {
	OwnerScopeHash  string
	ProjectEpoch    int64
	ArtifactSummary map[string]any
}

// TransitionParams are the arguments of RunStore.Transition.
type TransitionParams struct {
	RunID          string
	OwnerScopeHash string
	Target         RunStatus
	Phase          string
	EventType      string
	Payload        map[string]any
	// Attempt is the attempt number; the zero value means the Python default 1.
	Attempt int64
}

// AppendEventParams are the arguments of RunStore.AppendEvent.
type AppendEventParams struct {
	RunID          string
	OwnerScopeHash string
	Phase          string
	EventType      string
	Payload        map[string]any
	// Attempt is the attempt number; the zero value means the Python default 1.
	Attempt int64
}

// MergeArtifactSummaryParams are the arguments of RunStore.MergeArtifactSummary.
type MergeArtifactSummaryParams struct {
	RunID          string
	OwnerScopeHash string
	Values         map[string]any
}

// AcceptedToolCallIdsParams are the arguments of RunStore.AcceptedToolCallIds.
type AcceptedToolCallIdsParams struct {
	RunID          string
	OwnerScopeHash string
}

// EventsAfterParams are the arguments of RunStore.EventsAfter. Sequence is the
// exclusive cursor and its zero value matches the Python default of 0.
type EventsAfterParams struct {
	RunID          string
	OwnerScopeHash string
	Sequence       int64
}

// SequenceForEventParams are the arguments of RunStore.SequenceForEvent.
type SequenceForEventParams struct {
	RunID          string
	OwnerScopeHash string
	EventID        string
}

// AcceptInteractionParams are the arguments of RunStore.AcceptInteraction.
// ToolCallID is optional; nil means the Python default None.
type AcceptInteractionParams struct {
	RunID                string
	OwnerScopeHash       string
	Operation            string
	IdempotencyKey       string
	ExpectedProjectEpoch int64
	RequiredStatus       RunStatus
	Phase                string
	EventType            string
	Payload              map[string]any
	ToolCallID           *string
}

// CancelIdempotentParams are the arguments of RunStore.CancelIdempotent.
type CancelIdempotentParams struct {
	RunID                string
	OwnerScopeHash       string
	ExpectedProjectEpoch int64
	IdempotencyKey       string
}

// RecordClientCommitMetricParams are the arguments of
// RunStore.RecordClientCommitMetric. ErrorCode is optional; nil means the
// Python default None and is hashed and stored as JSON null.
type RecordClientCommitMetricParams struct {
	RunID                   string
	OwnerScopeHash          string
	ExpectedProjectEpoch    int64
	IdempotencyKey          string
	CommitDurationMs        int64
	VerifyDurationMs        int64
	BehaviorTestCount       int64
	BehaviorPassedCount     int64
	BehaviorMustCount       int64
	BehaviorMustPassedCount int64
	Outcome                 string
	ErrorCode               *string
}

// RecordResourceReviewMetricParams are the arguments of
// RunStore.RecordResourceReviewMetric. A nil Buckets slice is stored as an
// empty JSON array, like a Python empty list.
type RecordResourceReviewMetricParams struct {
	RunID                string
	OwnerScopeHash       string
	ExpectedProjectEpoch int64
	IdempotencyKey       string
	Buckets              []map[string]any
}

// MetricsForOwnerParams are the arguments of RunStore.MetricsForOwner.
// StuckAfterMs is optional; nil means the Python default None.
type MetricsForOwnerParams struct {
	OwnerScopeHash string
	WindowHours    int
	StuckAfterMs   *int64
}
