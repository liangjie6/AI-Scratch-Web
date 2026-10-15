package runmanager

import (
	"log/slog"

	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// programGenerationLayers builds stable, cross-job dependency layers from the
// verified Blueprint.
//
// Python: _program_generation_layers(blueprint, manifest).
func programGenerationLayers(blueprint *schema.ProjectBlueprint, manifest *schema.ProjectManifest) ([][]int, error) {
	behaviorToTarget := map[string]int{}
	for index := range manifest.ProgramTargets {
		for _, behaviorID := range manifest.ProgramTargets[index].BehaviorIDs {
			if _, exists := behaviorToTarget[behaviorID]; exists {
				return nil, runstore.NewRunTransitionError("behavior is assigned to multiple program targets: %s", behaviorID)
			}
			behaviorToTarget[behaviorID] = index
		}
	}

	dependencies := make([]map[int]struct{}, len(manifest.ProgramTargets))
	for index := range dependencies {
		dependencies[index] = map[int]struct{}{}
	}
	for index := range blueprint.BehaviorGraph {
		behavior := &blueprint.BehaviorGraph[index]
		targetIndex, ok := behaviorToTarget[behavior.BehaviorID]
		if !ok {
			return nil, runstore.NewRunTransitionError("Blueprint behavior has no program target: %s", behavior.BehaviorID)
		}
		for _, dependencyID := range behavior.DependsOn {
			dependencyTarget, ok := behaviorToTarget[dependencyID]
			if !ok {
				return nil, runstore.NewRunTransitionError("Blueprint dependency has no program target: %s", dependencyID)
			}
			if dependencyTarget != targetIndex {
				dependencies[targetIndex][dependencyTarget] = struct{}{}
			}
		}
	}

	remaining := make([]map[int]struct{}, len(dependencies))
	for index := range dependencies {
		item := make(map[int]struct{}, len(dependencies[index]))
		for dependency := range dependencies[index] {
			item[dependency] = struct{}{}
		}
		remaining[index] = item
	}
	completed := map[int]struct{}{}
	layers := [][]int{}
	for len(completed) < len(manifest.ProgramTargets) {
		layer := []int{}
		for index := range remaining {
			if len(remaining[index]) == 0 {
				if _, done := completed[index]; !done {
					layer = append(layer, index)
				}
			}
		}
		if len(layer) == 0 {
			slog.Warn("Blueprint program dependencies contain a cycle; falling back to self-healing layers")
			minDeps := -1
			fallbackCandidate := -1
			for idx := range manifest.ProgramTargets {
				if _, done := completed[idx]; done {
					continue
				}
				if minDeps < 0 || len(remaining[idx]) < minDeps {
					minDeps = len(remaining[idx])
					fallbackCandidate = idx
				}
			}
			if fallbackCandidate >= 0 {
				layer = []int{fallbackCandidate}
			} else {
				for idx := range manifest.ProgramTargets {
					if _, done := completed[idx]; !done {
						layer = append(layer, idx)
					}
				}
			}
		}
		layers = append(layers, layer)
		for _, index := range layer {
			completed[index] = struct{}{}
		}
		for index := range remaining {
			for _, index2 := range layer {
				delete(remaining[index], index2)
			}
		}
	}
	return layers, nil
}
