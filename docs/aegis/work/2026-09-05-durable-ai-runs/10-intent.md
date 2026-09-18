# Durable AI Runs Intent

Requested outcome: implement the durable run boundary and browser recovery path in the AI Scratch integration plan.

Scope: server-side run states, SQLite run/event/idempotency storage, versioned SSE replay, feature-gated project-mode client integration, and IndexedDB checkpoint recovery/export.

Non-goals for this slice: fresh-VM validation, behavior oracle, benchmark calibration, or removal of legacy AI block streams.

Baseline read set: the implementation plan sections 5.1, 10.3, 10.4, 10.6, 11.5 and 17 Phase 4; existing AI blocks generation, project resource preparation, checkpoint store and Scratch bridge boundaries.

Impact: durable protocol state is isolated from the existing generator so legacy SSE behavior remains unchanged during migration. The browser sends only normalized resource summaries and never VM IDs, handles, binary resources, provider deltas or reasoning text.
