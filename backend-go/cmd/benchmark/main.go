// Command benchmark is the Go port of
// backend/tools/evaluate_ai_blocks_benchmark.py: it aggregates sanitized
// AI-block benchmark results without reading model content.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

const (
	corpusSchemaVersion = 2
	resultSchemaVersion = 2
)

var verificationKinds = []string{"executable", "structural", "manual", "unsupported"}

var passingFieldByVerification = map[string]string{
	"executable":  "passed",
	"structural":  "passed",
	"manual":      "approved",
	"unsupported": "accepted",
}

// pyFloat renders a float the way Python's json.dumps does.
type pyFloat float64

// MarshalJSON emits the Python repr of the value.
func (f pyFloat) MarshalJSON() ([]byte, error) {
	if math.IsInf(float64(f), 0) || math.IsNaN(float64(f)) {
		return []byte("null"), nil
	}
	return []byte(jsonx.FormatFloat(float64(f))), nil
}

// orderedMap preserves Python dict insertion order in the JSON report.
type orderedMap struct {
	keys   []string
	values []any
}

func (m *orderedMap) set(key string, value any) *orderedMap {
	m.keys = append(m.keys, key)
	m.values = append(m.values, value)
	return m
}

// MarshalJSON emits the object with its keys in insertion order.
func (m *orderedMap) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range m.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		encodedValue, err := json.Marshal(m.values[index])
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedValue)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read JSON: %s", path)
	}
	value, err := jsonx.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("cannot read JSON: %s", path)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON root must be an object: %s", path)
	}
	return object, nil
}

func asBool(value any, field string) (bool, error) {
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be boolean", field)
	}
	return flag, nil
}

func asNonNegativeNumber(value any, field string) (float64, error) {
	number, ok := jsonx.Float(value)
	if !ok || math.IsInf(number, 0) || math.IsNaN(number) || number < 0 {
		return 0, fmt.Errorf("%s must be a non-negative finite number", field)
	}
	return number, nil
}

func asNonNegativeInt(value any, field string) (int64, error) {
	if _, isBool := value.(bool); isBool {
		return 0, fmt.Errorf("%s must be a non-negative integer", field)
	}
	number, ok := jsonx.Int(value)
	if !ok || number < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", field)
	}
	return number, nil
}

func asString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

type counter map[string]int64

func (c counter) add(key string, value int64) { c[key] += value }

func percentile(values []float64, fraction float64) *pyFloat {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	index := int(float64(len(ordered)-1) * fraction)
	if index > len(ordered)-1 {
		index = len(ordered) - 1
	}
	result := pyFloat(ordered[index])
	return &result
}

func rate(numerator, denominator int64) *pyFloat {
	if denominator == 0 {
		return nil
	}
	result := pyFloat(float64(numerator) / float64(denominator))
	return &result
}

func criterionExpectations(item map[string]any, caseID string) (map[string]counter, error) {
	expect, ok := item["expect"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("corpus case %s must contain an expect object", caseID)
	}
	rawCriteria, ok := expect["success_criteria"].([]any)
	if !ok || len(rawCriteria) == 0 {
		return nil, fmt.Errorf("corpus case %s must declare one or more success_criteria", caseID)
	}
	seen := map[string]bool{}
	counts := map[string]counter{}
	for _, kind := range verificationKinds {
		counts[kind] = counter{}
	}
	for index, raw := range rawCriteria {
		path := fmt.Sprintf("corpus case %s.expect.success_criteria[%d]", caseID, index)
		criterion, ok := raw.(map[string]any)
		if !ok || len(criterion) != 3 {
			return nil, fmt.Errorf("%s must contain only id, priority and verification", path)
		}
		for _, key := range []string{"id", "priority", "verification"} {
			if _, present := criterion[key]; !present {
				return nil, fmt.Errorf("%s must contain only id, priority and verification", path)
			}
		}
		criterionID, _ := asString(criterion["id"])
		priority, _ := asString(criterion["priority"])
		verification, _ := asString(criterion["verification"])
		if criterionID == "" || seen[criterionID] {
			return nil, fmt.Errorf("%s.id must be a unique non-empty string", path)
		}
		if priority != "must" && priority != "should" {
			return nil, fmt.Errorf("%s.priority must be must or should", path)
		}
		if _, ok := passingFieldByVerification[verification]; !ok {
			return nil, fmt.Errorf("%s.verification is invalid", path)
		}
		seen[criterionID] = true
		counts[verification].add("total", 1)
		if priority == "must" {
			counts[verification].add("must_total", 1)
		}
	}
	return counts, nil
}

func criterionOutcomes(raw any, expected map[string]counter, path string) (map[string]counter, error) {
	object, ok := raw.(map[string]any)
	if !ok || len(object) != len(verificationKinds) {
		return nil, fmt.Errorf("%s must contain exactly executable, structural, manual and unsupported", path)
	}
	outcomes := map[string]counter{}
	for _, kind := range verificationKinds {
		entry, ok := object[kind].(map[string]any)
		entryPath := path + "." + kind
		if !ok {
			return nil, fmt.Errorf("%s must be an object", entryPath)
		}
		passingField := passingFieldByVerification[kind]
		expectedFields := map[string]bool{"total": true, "must_total": true, passingField: true, "must_" + passingField: true}
		if kind == "manual" {
			expectedFields["reviewed"] = true
			expectedFields["must_reviewed"] = true
		}
		if len(entry) != len(expectedFields) {
			return nil, fmt.Errorf("%s has invalid fields", entryPath)
		}
		values := counter{}
		for field := range expectedFields {
			raw, present := entry[field]
			if !present {
				return nil, fmt.Errorf("%s has invalid fields", entryPath)
			}
			parsed, err := asNonNegativeInt(raw, entryPath+"."+field)
			if err != nil {
				return nil, err
			}
			values[field] = parsed
		}
		if values["total"] != expected[kind]["total"] || values["must_total"] != expected[kind]["must_total"] {
			return nil, fmt.Errorf("%s totals must match the corpus success_criteria", entryPath)
		}
		if values[passingField] > values["total"] || values["must_"+passingField] > values["must_total"] {
			return nil, fmt.Errorf("%s completion count exceeds its total", entryPath)
		}
		if values["must_"+passingField] > values[passingField] {
			return nil, fmt.Errorf("%s must completion count exceeds total completion count", entryPath)
		}
		if kind == "manual" {
			if values["reviewed"] > values["total"] || values["must_reviewed"] > values["must_total"] {
				return nil, fmt.Errorf("%s review count exceeds its total", entryPath)
			}
			if values["approved"] > values["reviewed"] || values["must_approved"] > values["must_reviewed"] {
				return nil, fmt.Errorf("%s approval requires a completed review", entryPath)
			}
		}
		outcomes[kind] = values
	}
	return outcomes, nil
}

func mustCriteriaCompleted(outcomes map[string]counter) bool {
	for kind, values := range outcomes {
		if values["must_"+passingFieldByVerification[kind]] != values["must_total"] {
			return false
		}
	}
	return true
}

func addCriterionCounts(destination counter, outcomes map[string]counter) {
	for kind, values := range outcomes {
		passingField := passingFieldByVerification[kind]
		prefix := "criterion_" + kind + "_"
		destination.add(prefix+"total", values["total"])
		destination.add(prefix+"must_total", values["must_total"])
		destination.add(prefix+passingField, values[passingField])
		destination.add(prefix+"must_"+passingField, values["must_"+passingField])
		if kind == "manual" {
			destination.add(prefix+"reviewed", values["reviewed"])
			destination.add(prefix+"must_reviewed", values["must_reviewed"])
		}
	}
}

func criterionSummary(counts counter) *orderedMap {
	summary := &orderedMap{}
	for _, kind := range verificationKinds {
		passingField := passingFieldByVerification[kind]
		prefix := "criterion_" + kind + "_"
		entry := (&orderedMap{}).
			set("total", counts[prefix+"total"]).
			set("must_total", counts[prefix+"must_total"]).
			set(passingField, counts[prefix+passingField]).
			set("must_"+passingField, counts[prefix+"must_"+passingField]).
			set("completion_rate", rate(counts[prefix+passingField], counts[prefix+"total"])).
			set("must_completion_rate", rate(counts[prefix+"must_"+passingField], counts[prefix+"must_total"]))
		if kind == "manual" {
			entry.set("reviewed", counts[prefix+"reviewed"]).
				set("must_reviewed", counts[prefix+"must_reviewed"]).
				set("review_rate", rate(counts[prefix+"reviewed"], counts[prefix+"total"])).
				set("must_review_rate", rate(counts[prefix+"must_reviewed"], counts[prefix+"must_total"]))
		}
		summary.set(kind, entry)
	}
	return summary
}

func allMustCompletionRate(counts counter) *pyFloat {
	var total, completed int64
	for _, kind := range verificationKinds {
		total += counts["criterion_"+kind+"_must_total"]
		completed += counts["criterion_"+kind+"_must_"+passingFieldByVerification[kind]]
	}
	return rate(completed, total)
}

func totalFloat(values []float64) pyFloat {
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return pyFloat(sum)
}

func aggregate(corpus, results map[string]any) (*orderedMap, error) {
	cases, ok := corpus["cases"].([]any)
	version, _ := jsonx.Int(corpus["schema_version"])
	if !ok || version != corpusSchemaVersion {
		return nil, fmt.Errorf("unsupported benchmark corpus")
	}
	corpusByID := map[string]map[string]any{}
	order := []string{}
	for index, raw := range cases {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("corpus cases[%d] must contain a unique string id and category", index)
		}
		caseID, okID := asString(item["id"])
		_, okCategory := asString(item["category"])
		if !okID || !okCategory {
			return nil, fmt.Errorf("corpus cases[%d] must contain a unique string id and category", index)
		}
		if _, exists := corpusByID[caseID]; exists {
			return nil, fmt.Errorf("corpus cases[%d].id is duplicated: %s", index, caseID)
		}
		corpusByID[caseID] = item
		order = append(order, caseID)
	}
	if len(corpusByID) != len(cases) {
		return nil, fmt.Errorf("benchmark corpus has invalid or duplicate case IDs")
	}
	expectedByCase := map[string]map[string]counter{}
	for caseID, item := range corpusByID {
		expected, err := criterionExpectations(item, caseID)
		if err != nil {
			return nil, err
		}
		expectedByCase[caseID] = expected
	}

	runs, ok := results["runs"].([]any)
	resultVersion, _ := jsonx.Int(results["schema_version"])
	if !ok || resultVersion != resultSchemaVersion {
		return nil, fmt.Errorf("result schema_version=2 and runs array are required")
	}

	seen := map[string]bool{}
	byCategory := map[string]counter{}
	totals := counter{}
	latencies := []float64{}
	repairCounts := []float64{}
	tokenCounts := []float64{}
	costs := []float64{}

	for index, raw := range runs {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("runs[%d] must be an object", index)
		}
		caseID, _ := asString(entry["case_id"])
		if _, exists := corpusByID[caseID]; !exists || caseID == "" || seen[caseID] {
			return nil, fmt.Errorf("runs[%d].case_id must identify one unique corpus case", index)
		}
		seen[caseID] = true
		category, _ := asString(corpusByID[caseID]["category"])

		contractValid, err := asBool(entry["contract_valid"], fmt.Sprintf("runs[%d].contract_valid", index))
		if err != nil {
			return nil, err
		}
		programValid, err := asBool(entry["program_valid"], fmt.Sprintf("runs[%d].program_valid", index))
		if err != nil {
			return nil, err
		}
		programFirstPass, err := asBool(entry["program_first_pass"], fmt.Sprintf("runs[%d].program_first_pass", index))
		if err != nil {
			return nil, err
		}
		endToEnd, err := asBool(entry["end_to_end_completed"], fmt.Sprintf("runs[%d].end_to_end_completed", index))
		if err != nil {
			return nil, err
		}
		if programFirstPass && !programValid {
			return nil, fmt.Errorf("runs[%d].program_first_pass requires program_valid", index)
		}
		taskFieldTotal, err := asNonNegativeInt(entry["task_field_total"], fmt.Sprintf("runs[%d].task_field_total", index))
		if err != nil {
			return nil, err
		}
		taskFieldMatched, err := asNonNegativeInt(entry["task_field_matched"], fmt.Sprintf("runs[%d].task_field_matched", index))
		if err != nil {
			return nil, err
		}
		if taskFieldMatched > taskFieldTotal {
			return nil, fmt.Errorf("runs[%d].task_field_matched exceeds total", index)
		}
		outcomes, err := criterionOutcomes(entry["criterion_outcomes"], expectedByCase[caseID], fmt.Sprintf("runs[%d].criterion_outcomes", index))
		if err != nil {
			return nil, err
		}
		if endToEnd && !mustCriteriaCompleted(outcomes) {
			return nil, fmt.Errorf("runs[%d].end_to_end_completed requires every must criterion to pass, be approved or be accepted", index)
		}
		assetMatch, _ := asString(entry["asset_match"])
		switch assetMatch {
		case "not_applicable", "top1", "candidate_recall", "review_required", "missing":
		default:
			return nil, fmt.Errorf("runs[%d].asset_match is invalid", index)
		}
		addFlag := func(target counter, key string, value bool) {
			if value {
				target.add(key, 1)
			}
		}
		if _, exists := byCategory[category]; !exists {
			byCategory[category] = counter{}
		}
		for _, target := range []counter{totals, byCategory[category]} {
			target.add("runs", 1)
			addFlag(target, "contract_valid", contractValid)
			addFlag(target, "program_valid", programValid)
			addFlag(target, "program_first_pass", programFirstPass)
			addFlag(target, "end_to_end_completed", endToEnd)
			target.add("task_field_total", taskFieldTotal)
			target.add("task_field_matched", taskFieldMatched)
			addCriterionCounts(target, outcomes)
		}
		addFlag(totals, "asset_top1", assetMatch == "top1")
		addFlag(totals, "asset_candidate_recall", assetMatch == "top1" || assetMatch == "candidate_recall")
		addFlag(totals, "asset_review_required", assetMatch == "review_required")
		addFlag(totals, "asset_missing", assetMatch == "missing")

		latency, err := asNonNegativeNumber(entry["latency_ms"], fmt.Sprintf("runs[%d].latency_ms", index))
		if err != nil {
			return nil, err
		}
		latencies = append(latencies, latency)
		repairs, err := asNonNegativeInt(entry["repair_count"], fmt.Sprintf("runs[%d].repair_count", index))
		if err != nil {
			return nil, err
		}
		repairCounts = append(repairCounts, float64(repairs))
		tokens, err := asNonNegativeNumber(entry["token_count"], fmt.Sprintf("runs[%d].token_count", index))
		if err != nil {
			return nil, err
		}
		tokenCounts = append(tokenCounts, tokens)
		cost, err := asNonNegativeNumber(entry["cost_usd"], fmt.Sprintf("runs[%d].cost_usd", index))
		if err != nil {
			return nil, err
		}
		costs = append(costs, cost)
	}

	categories := make([]string, 0, len(byCategory))
	for category := range byCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	categorySummary := &orderedMap{}
	for _, category := range categories {
		counts := byCategory[category]
		categorySummary.set(category, (&orderedMap{}).
			set("run_count", counts["runs"]).
			set("contract_valid_rate", rate(counts["contract_valid"], counts["runs"])).
			set("program_valid_rate", rate(counts["program_valid"], counts["runs"])).
			set("program_first_pass_rate", rate(counts["program_first_pass"], counts["runs"])).
			set("end_to_end_completion_rate", rate(counts["end_to_end_completed"], counts["runs"])).
			set("task_field_recall", rate(counts["task_field_matched"], counts["task_field_total"])).
			set("criterion_verification", criterionSummary(counts)).
			set("all_must_criterion_completion_rate", allMustCompletionRate(counts)))
	}

	missing := []string{}
	for _, caseID := range order {
		if !seen[caseID] {
			missing = append(missing, caseID)
		}
	}
	sort.Strings(missing)

	endToEndRate := rate(totals["end_to_end_completed"], totals["runs"])
	mustRate := allMustCompletionRate(totals)
	meetsTarget := len(seen) == len(corpusByID) && endToEndRate != nil && mustRate != nil &&
		float64(*endToEndRate) >= 0.9 && float64(*mustRate) >= 0.9

	report := (&orderedMap{}).
		set("schema_version", resultSchemaVersion).
		set("corpus_case_count", len(corpusByID)).
		set("evaluated_case_count", totals["runs"]).
		set("missing_case_ids", missing).
		set("contract_valid_rate", rate(totals["contract_valid"], totals["runs"])).
		set("program_valid_rate", rate(totals["program_valid"], totals["runs"])).
		set("program_first_pass_rate", rate(totals["program_first_pass"], totals["runs"])).
		set("end_to_end_completion_rate", endToEndRate).
		set("task_field_recall", rate(totals["task_field_matched"], totals["task_field_total"])).
		set("criterion_verification", criterionSummary(totals)).
		set("all_must_criterion_completion_rate", mustRate).
		set("asset_matching", (&orderedMap{}).
			set("top1_count", totals["asset_top1"]).
			set("candidate_recall_count", totals["asset_candidate_recall"]).
			set("review_required_count", totals["asset_review_required"]).
			set("missing_count", totals["asset_missing"])).
		set("repair", (&orderedMap{}).set("total", totalFloat(repairCounts)).set("p95", percentile(repairCounts, 0.95))).
		set("latency_ms", (&orderedMap{}).set("p50", percentile(latencies, 0.5)).set("p95", percentile(latencies, 0.95))).
		set("token_count", (&orderedMap{}).set("total", totalFloat(tokenCounts)).set("p95", percentile(tokenCounts, 0.95))).
		set("cost_usd", (&orderedMap{}).set("total", totalFloat(costs)).set("p95", percentile(costs, 0.95))).
		set("by_category", categorySummary).
		set("meets_phase0_90_percent_target", meetsTarget)
	return report, nil
}

func main() {
	corpusPath := flag.String("corpus", "", "benchmark corpus JSON path")
	resultsPath := flag.String("results", "", "benchmark results JSON path")
	outputPath := flag.String("output", "", "optional output path")
	flag.Parse()
	if *corpusPath == "" || *resultsPath == "" {
		fmt.Fprintln(os.Stderr, "usage: benchmark --corpus <path> --results <path> [--output <path>]")
		os.Exit(2)
	}
	corpus, err := readJSON(*corpusPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	results, err := readJSON(*resultsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report, err := aggregate(corpus, results)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rendered := buffer.String()
	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, []byte(rendered), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Print(rendered)
}

var _ = strings.TrimSpace
var _ = strconv.Itoa
