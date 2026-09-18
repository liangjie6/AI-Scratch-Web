/** Public Blueprint contract entry point; implementation stays in the shared
 * contract module to preserve one source of truth for lineage validation. */
export type {
  ProjectBlueprint,
  BlueprintAcceptanceTest,
  AcceptanceTrigger,
  AcceptanceArrange,
  AcceptanceExpectation,
} from './project-contract';
export {projectTaskSpecToBlueprint, projectBlueprintToManifest, projectProgramGenerationLayers} from './project-contract';
