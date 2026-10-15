package runstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
)

// errorCodePattern mirrors re.fullmatch(r'[A-Z0-9_]{1,80}', code). Go's $
// anchors at the end of the text (no multiline flag), so this is equivalent.
var errorCodePattern = regexp.MustCompile(`^[A-Z0-9_]{1,80}$`)

// resourceReviewBucketFields lists the aggregate counters of one bucket, in the
// same order the Python aggregate dict declares them.
var resourceReviewBucketFields = []string{
	"auto_selected_count",
	"auto_reselected_count",
	"manual_selected_count",
	"review_required_count",
	"generation_fallback_count",
}

// resourceReviewResourceTypes is the accepted resource_type set.
var resourceReviewResourceTypes = map[string]struct{}{
	"sprite":   {},
	"costume":  {},
	"backdrop": {},
	"sound":    {},
}

// metricRunRow is one ai_runs row read by MetricsForOwner.
type metricRunRow struct {
	status     string
	createdAt  int64
	updatedAt  int64
	terminalAt sql.NullInt64
}

// metricEventRow is one joined ai_run_events row read by MetricsForOwner.
type metricEventRow struct {
	runID       string
	phase       string
	eventType   string
	attempt     int64
	payloadJSON string
	createdAt   int64
}

// metricClientRow is one ai_run_commit_metric_receipts row.
type metricClientRow struct {
	runID                   string
	commitDurationMs        int64
	verifyDurationMs        int64
	behaviorTestCount       int64
	behaviorPassedCount     int64
	behaviorMustCount       int64
	behaviorMustPassedCount int64
	outcome                 string
	errorCode               sql.NullString
}

// metricResourceRow is one ai_run_resource_review_metric_receipts row.
type metricResourceRow struct {
	runID       string
	bucketsJSON string
}

// MetricsForOwner returns bounded, payload-free telemetry for one opaque owner
// scope.
//
// Python: metrics_for_owner().
func (s *RunStore) MetricsForOwner(params MetricsForOwnerParams) (map[string]any, error) {
	now := nowMillis()
	windowStart := now - int64(params.WindowHours)*60*60*1000

	var (
		runs               []metricRunRow
		eventRows          []metricEventRow
		clientMetricRows   []metricClientRow
		resourceReviewRows []metricResourceRow
	)
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		db, err := s.handle()
		if err != nil {
			return err
		}
		ctx := context.Background()

		rows, err := db.QueryContext(ctx, `
			SELECT status, created_at, updated_at, terminal_at
			FROM ai_runs
			WHERE owner_scope_hash = ? AND created_at >= ?`,
			params.OwnerScopeHash, windowStart,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var row metricRunRow
			if err := rows.Scan(&row.status, &row.createdAt, &row.updatedAt, &row.terminalAt); err != nil {
				rows.Close()
				return err
			}
			runs = append(runs, row)
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			return rowsErr
		}

		rows, err = db.QueryContext(ctx, `
			SELECT event.run_id, event.phase, event.type, event.attempt, event.payload_json, event.created_at
			FROM ai_run_events AS event
			INNER JOIN ai_runs AS run ON run.run_id = event.run_id
			WHERE run.owner_scope_hash = ? AND run.created_at >= ? AND event.created_at >= ?
			ORDER BY event.run_id ASC, event.sequence ASC`,
			params.OwnerScopeHash, windowStart, windowStart,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var row metricEventRow
			if err := rows.Scan(&row.runID, &row.phase, &row.eventType, &row.attempt, &row.payloadJSON, &row.createdAt); err != nil {
				rows.Close()
				return err
			}
			eventRows = append(eventRows, row)
		}
		rowsErr = rows.Err()
		rows.Close()
		if rowsErr != nil {
			return rowsErr
		}

		rows, err = db.QueryContext(ctx, `
			SELECT metric.run_id, metric.commit_duration_ms, metric.verify_duration_ms,
			       metric.behavior_test_count, metric.behavior_passed_count,
			       metric.behavior_must_count, metric.behavior_must_passed_count,
			       metric.outcome, metric.error_code
			FROM ai_run_commit_metric_receipts AS metric
			INNER JOIN ai_runs AS run ON run.run_id = metric.run_id
			WHERE run.owner_scope_hash = ? AND metric.created_at >= ?`,
			params.OwnerScopeHash, windowStart,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var row metricClientRow
			if err := rows.Scan(
				&row.runID,
				&row.commitDurationMs,
				&row.verifyDurationMs,
				&row.behaviorTestCount,
				&row.behaviorPassedCount,
				&row.behaviorMustCount,
				&row.behaviorMustPassedCount,
				&row.outcome,
				&row.errorCode,
			); err != nil {
				rows.Close()
				return err
			}
			clientMetricRows = append(clientMetricRows, row)
		}
		rowsErr = rows.Err()
		rows.Close()
		if rowsErr != nil {
			return rowsErr
		}

		rows, err = db.QueryContext(ctx, `
			SELECT metric.run_id, metric.buckets_json
			FROM ai_run_resource_review_metric_receipts AS metric
			INNER JOIN ai_runs AS run ON run.run_id = metric.run_id
			WHERE run.owner_scope_hash = ? AND metric.created_at >= ?`,
			params.OwnerScopeHash, windowStart,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var row metricResourceRow
			if err := rows.Scan(&row.runID, &row.bucketsJSON); err != nil {
				rows.Close()
				return err
			}
			resourceReviewRows = append(resourceReviewRows, row)
		}
		rowsErr = rows.Err()
		rows.Close()
		if rowsErr != nil {
			return rowsErr
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}

	statuses := map[string]int{}
	durations := []int64{}
	stuckRunCount := 0
	for _, row := range runs {
		statuses[row.status]++
		if row.terminalAt.Valid {
			durations = append(durations, maxInt64(0, row.terminalAt.Int64-row.createdAt))
		} else if params.StuckAfterMs != nil &&
			row.status != string(RunStatusAwaitingToolResult) &&
			now-row.updatedAt > *params.StuckAfterMs {
			stuckRunCount++
		}
	}
	sortInt64s(durations)

	eventTypeCounts := map[string]int{}
	errorCodeCounts := map[string]int{}
	validationFailedRuns := map[string]struct{}{}
	validationCompletedRuns := map[string]struct{}{}
	repairAttempts := map[string]int64{}

	type activePhase struct {
		phase     string
		startedAt int64
	}
	activePhases := map[string]activePhase{}
	phaseDurations := map[string][]int64{}
	closeActivePhase := func(runID string, endedAt int64) {
		active, ok := activePhases[runID]
		if !ok {
			return
		}
		delete(activePhases, runID)
		phaseDurations[active.phase] = append(phaseDurations[active.phase], maxInt64(0, endedAt-active.startedAt))
	}

	for _, row := range eventRows {
		eventTypeCounts[row.eventType]++
		switch row.eventType {
		case "phase_started":
			active, ok := activePhases[row.runID]
			if !ok {
				activePhases[row.runID] = activePhase{phase: row.phase, startedAt: row.createdAt}
			} else if active.phase != row.phase {
				closeActivePhase(row.runID, row.createdAt)
				activePhases[row.runID] = activePhase{phase: row.phase, startedAt: row.createdAt}
			}
		case "asset_resolution_required":
			closeActivePhase(row.runID, row.createdAt)
			activePhases[row.runID] = activePhase{phase: row.phase, startedAt: row.createdAt}
		case "phase_completed":
			if active, ok := activePhases[row.runID]; ok && active.phase == row.phase {
				closeActivePhase(row.runID, row.createdAt)
			}
		case "repair_started":
			if row.attempt > repairAttempts[row.runID] {
				repairAttempts[row.runID] = row.attempt
			}
		case "validation_failed":
			validationFailedRuns[row.runID] = struct{}{}
		case "generation_complete", "generation_error":
			closeActivePhase(row.runID, row.createdAt)
			if row.eventType == "generation_complete" {
				validationCompletedRuns[row.runID] = struct{}{}
			}
		}
		if row.eventType == "generation_error" {
			payload := map[string]any{}
			if err := json.Unmarshal([]byte(row.payloadJSON), &payload); err == nil {
				if code, ok := payload["code"].(string); ok && errorCodePattern.MatchString(code) {
					errorCodeCounts[code]++
				}
			}
		}
	}

	phaseDurationMs := map[string]map[string]any{}
	for phase, values := range phaseDurations {
		sortInt64s(values)
		phaseDurationMs[phase] = map[string]any{
			"count": len(values),
			"p50":   percentileInt64(values, 0.5),
			"p95":   percentileInt64(values, 0.95),
		}
	}
	repairCounts := make([]int64, 0, len(repairAttempts))
	for _, attempts := range repairAttempts {
		repairCounts = append(repairCounts, attempts)
	}
	sortInt64s(repairCounts)

	// A cancelled or provider-failed run which never reached ProgramIR
	// validation is not evidence of a first-pass schema result.
	validationEligibleRuns := map[string]struct{}{}
	for runID := range validationCompletedRuns {
		validationEligibleRuns[runID] = struct{}{}
	}
	for runID := range validationFailedRuns {
		validationEligibleRuns[runID] = struct{}{}
	}
	firstPassRuns := map[string]struct{}{}
	for runID := range validationCompletedRuns {
		if _, failed := validationFailedRuns[runID]; !failed {
			firstPassRuns[runID] = struct{}{}
		}
	}

	commitDurations := make([]int64, 0, len(clientMetricRows))
	verifyDurations := make([]int64, 0, len(clientMetricRows))
	commitVerifyDurations := make([]int64, 0, len(clientMetricRows))
	var behaviorTestCount, behaviorPassedCount, behaviorMustCount, behaviorMustPassedCount int64
	commitOutcomeCounts := map[string]int{}
	commitErrorCodeCounts := map[string]int{}
	for _, row := range clientMetricRows {
		commit := maxInt64(0, row.commitDurationMs)
		verify := maxInt64(0, row.verifyDurationMs)
		commitDurations = append(commitDurations, commit)
		verifyDurations = append(verifyDurations, verify)
		commitVerifyDurations = append(commitVerifyDurations, commit+verify)
		behaviorTestCount += maxInt64(0, row.behaviorTestCount)
		behaviorPassedCount += maxInt64(0, row.behaviorPassedCount)
		behaviorMustCount += maxInt64(0, row.behaviorMustCount)
		behaviorMustPassedCount += maxInt64(0, row.behaviorMustPassedCount)
		commitOutcomeCounts[row.outcome]++
		if row.errorCode.Valid && errorCodePattern.MatchString(row.errorCode.String) {
			commitErrorCodeCounts[row.errorCode.String]++
		}
	}
	sortInt64s(commitDurations)
	sortInt64s(verifyDurations)
	sortInt64s(commitVerifyDurations)

	revisionConflictCount := commitErrorCodeCounts["COMMIT_CONFLICT"]
	var revisionConflictRate *float64
	if len(clientMetricRows) > 0 {
		rate := float64(revisionConflictCount) / float64(len(clientMetricRows))
		revisionConflictRate = &rate
	}

	resourceReviewByType := map[string]map[string]int64{}
	for _, row := range resourceReviewRows {
		var decoded any
		if err := json.Unmarshal([]byte(row.bucketsJSON), &decoded); err != nil {
			continue
		}
		buckets, ok := decoded.([]any)
		if !ok {
			continue
		}
		for _, bucketValue := range buckets {
			bucket, ok := bucketValue.(map[string]any)
			if !ok {
				continue
			}
			resourceType, ok := bucket["resource_type"].(string)
			if !ok {
				continue
			}
			if _, allowed := resourceReviewResourceTypes[resourceType]; !allowed {
				continue
			}
			aggregate, ok := resourceReviewByType[resourceType]
			if !ok {
				aggregate = map[string]int64{}
				for _, field := range resourceReviewBucketFields {
					aggregate[field] = 0
				}
				resourceReviewByType[resourceType] = aggregate
			}
			for _, field := range resourceReviewBucketFields {
				value, ok := jsonInt64(bucket[field])
				if ok && value >= 0 && value <= 100 {
					aggregate[field] += value
				}
			}
		}
	}

	alerts := []map[string]any{}
	failedRecoveryCount := commitOutcomeCounts["FAILED_NEEDS_RECOVERY"]
	if failedRecoveryCount > 0 {
		alerts = append(alerts, map[string]any{
			"code":     "ATOMIC_RECOVERY_FAILURE",
			"severity": "critical",
			"action":   "DISABLE_AI_COMMIT",
			"count":    failedRecoveryCount,
		})
	}
	totalP95 := percentileInt64(commitVerifyDurations, 0.95)
	if len(clientMetricRows) >= 50 && totalP95 != nil && *totalP95 > 5000 {
		alerts = append(alerts, map[string]any{
			"code":            "COMMIT_VERIFY_P95_EXCEEDED",
			"severity":        "warning",
			"action":          "STOP_ROLLOUT",
			"attempt_count":   len(clientMetricRows),
			"p95_duration_ms": *totalP95,
		})
	}
	sb3ResourceMissingCount := commitErrorCodeCounts["SB3_RESOURCE_MISSING"]
	var sb3ResourceMissingRate *float64
	if len(clientMetricRows) > 0 {
		rate := float64(sb3ResourceMissingCount) / float64(len(clientMetricRows))
		sb3ResourceMissingRate = &rate
	}
	if len(clientMetricRows) >= 1000 && sb3ResourceMissingRate != nil && *sb3ResourceMissingRate > 0.001 {
		alerts = append(alerts, map[string]any{
			"code":                   "SB3_RESOURCE_MISSING_RATE_EXCEEDED",
			"severity":               "warning",
			"action":                 "STOP_ROLLOUT",
			"attempt_count":          len(clientMetricRows),
			"missing_resource_count": sb3ResourceMissingCount,
		})
	}
	var stuckRate *float64
	if len(runs) > 0 {
		rate := float64(stuckRunCount) / float64(len(runs))
		stuckRate = &rate
	}
	if stuckRate != nil && *stuckRate > 0.005 {
		alerts = append(alerts, map[string]any{
			"code":            "STUCK_RUN_RATE_EXCEEDED",
			"severity":        "warning",
			"action":          "STOP_ROLLOUT",
			"run_count":       len(runs),
			"stuck_run_count": stuckRunCount,
		})
	}
	var firstPassRate *float64
	if len(validationEligibleRuns) > 0 {
		rate := float64(len(firstPassRuns)) / float64(len(validationEligibleRuns))
		firstPassRate = &rate
	}
	if len(validationEligibleRuns) >= 100 && firstPassRate != nil && *firstPassRate < 0.9 {
		alerts = append(alerts, map[string]any{
			"code":               "VALIDATION_FIRST_PASS_BELOW_TARGET",
			"severity":           "warning",
			"action":             "STOP_ROLLOUT",
			"eligible_run_count": len(validationEligibleRuns),
			"first_pass_rate":    *firstPassRate,
		})
	}
	var behaviorMustPassRate *float64
	if behaviorMustCount > 0 {
		rate := float64(behaviorMustPassedCount) / float64(behaviorMustCount)
		behaviorMustPassRate = &rate
	}
	if behaviorMustCount >= 100 && behaviorMustPassRate != nil && *behaviorMustPassRate < 0.9 {
		alerts = append(alerts, map[string]any{
			"code":           "BEHAVIOR_MUST_PASS_BELOW_TARGET",
			"severity":       "warning",
			"action":         "STOP_ROLLOUT",
			"must_count":     behaviorMustCount,
			"must_pass_rate": *behaviorMustPassRate,
		})
	}
	var behaviorPassRate *float64
	if behaviorTestCount > 0 {
		rate := float64(behaviorPassedCount) / float64(behaviorTestCount)
		behaviorPassRate = &rate
	}

	commitVerifyRunIDs := map[string]struct{}{}
	for _, row := range clientMetricRows {
		commitVerifyRunIDs[row.runID] = struct{}{}
	}
	resourceReviewRunIDs := map[string]struct{}{}
	for _, row := range resourceReviewRows {
		resourceReviewRunIDs[row.runID] = struct{}{}
	}

	return map[string]any{
		"window_start":       windowStart,
		"window_end":         now,
		"run_count":          len(runs),
		"terminal_run_count": len(durations),
		"status_counts":      statuses,
		"event_type_counts":  eventTypeCounts,
		"error_code_counts":  errorCodeCounts,
		"terminal_duration_ms": map[string]any{
			"p50": percentileInt64(durations, 0.5),
			"p95": percentileInt64(durations, 0.95),
		},
		"failed_run_count":    statuses[string(RunStatusFailed)],
		"cancelled_run_count": statuses[string(RunStatusCancelled)],
		"stuck_run_count":     stuckRunCount,
		"stuck_after_ms":      params.StuckAfterMs,
		"alerts":              alerts,
		"phase_duration_ms":   phaseDurationMs,
		"repair": map[string]any{
			"run_count":      len(repairCounts),
			"total_attempts": sumInt64s(repairCounts),
			"p95_attempts":   percentileInt64(repairCounts, 0.95),
		},
		"validation": map[string]any{
			"eligible_run_count":   len(validationEligibleRuns),
			"failed_run_count":     len(validationFailedRuns),
			"first_pass_run_count": len(firstPassRuns),
			"first_pass_rate":      firstPassRate,
		},
		"commit_verify": map[string]any{
			"attempt_count":        len(clientMetricRows),
			"run_count":            len(commitVerifyRunIDs),
			"outcome_counts":       commitOutcomeCounts,
			"rollback_kind_counts": commitOutcomeCounts,
			"error_code_counts":    commitErrorCodeCounts,
			"revision_conflict": map[string]any{
				"count": revisionConflictCount,
				"rate":  revisionConflictRate,
			},
			"commit_duration_ms": map[string]any{
				"p50": percentileInt64(commitDurations, 0.5),
				"p95": percentileInt64(commitDurations, 0.95),
			},
			"verify_duration_ms": map[string]any{
				"p50": percentileInt64(verifyDurations, 0.5),
				"p95": percentileInt64(verifyDurations, 0.95),
			},
			"total_duration_ms": map[string]any{
				"p50": percentileInt64(commitVerifyDurations, 0.5),
				"p95": percentileInt64(commitVerifyDurations, 0.95),
			},
		},
		"behavior": map[string]any{
			"test_count":        behaviorTestCount,
			"passed_count":      behaviorPassedCount,
			"pass_rate":         behaviorPassRate,
			"must_count":        behaviorMustCount,
			"must_passed_count": behaviorMustPassedCount,
			"must_pass_rate":    behaviorMustPassRate,
		},
		"resource_review": map[string]any{
			"attempt_count":    len(resourceReviewRows),
			"run_count":        len(resourceReviewRunIDs),
			"by_resource_type": resourceReviewByType,
		},
	}, nil
}

// percentileInt64 mirrors the Python percentile() helper:
// values[min(len(values) - 1, int((len(values) - 1) * fraction))], with None
// for an empty list.
func percentileInt64(values []int64, fraction float64) *int64 {
	if len(values) == 0 {
		return nil
	}
	index := int(float64(len(values)-1) * fraction)
	if index > len(values)-1 {
		index = len(values) - 1
	}
	if index < 0 {
		index = 0
	}
	value := values[index]
	return &value
}

// maxInt64 mirrors Python's max(0, a - b) and max(0, value) clamps.
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// sortInt64s sorts in place, mirroring list.sort().
func sortInt64s(values []int64) {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
}

// sumInt64s mirrors Python's sum() over a list of ints.
func sumInt64s(values []int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
}
