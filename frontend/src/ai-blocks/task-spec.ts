/** Public TaskSpec contract entry point. Definitions remain centralized so
 * lineage helpers and consumers cannot drift across duplicate interfaces. */
export type {
  ScratchTaskSpec,
  Scalar,
  TaskOperand,
  TaskTrigger,
  TaskCondition,
  TaskAction,
  TaskStateKind,
} from './project-contract';
