# Scratch capability contracts

Regenerate from the installed, pinned dependencies:

```sh
node scripts/generate-scratch-capabilities.mjs
node scripts/generate-scratch-capabilities.mjs --check
node --test scripts/scratch-capabilities.test.mjs
```

`scratch-policy-overlay.json` is the reviewed AI subset: allowed opcodes and extensions, target restrictions, authoring literal kinds, reference resolvers and current compiler shadows. It deliberately retains the existing 138-opcode surface. Structural fields and substacks come from the source probe. Changes to literal kinds or extensions require an explicit policy edit and review; native toolbox defaults never widen authoring permissions.

`scratch-capabilities.json` is the canonical generated artifact. Each entry separates `source_facts` (native connections, static serialized menu values, messages, toolbox defaults, VM implementation existence and source location) from `authoring` (the compiler's supported subset plus menu metadata). `input_order` preserves the authoring port order. The SHA-256 `catalog_hash` covers canonical content, including pinned package versions and source-file/policy/generator hashes. No time, absolute checkout path, random ID or fixture asset is included.

The build probe uses an isolated JSDOM `WorkspaceSvg`, the installed ScratchBlocks module, the GUI's VM menu wiring and stage/sprite toolboxes, variable flyout callbacks and VM source primitives/hats. Extension declarations are read only from the three allowlisted built-in implementations. The AST index records native definitions and message keys; it does not attempt to statically execute arbitrary JavaScript. `--check` repeats extraction and compares the JSON and both generated languages without writing them. It requires frontend dependencies and `gofmt`.

TypeScript and Go are generated from this artifact with existing public exports preserved. Static menu metadata uses serialized strings (`allowedValues` / `FieldEnums` / `InputEnums`); numeric authoring literals remain numbers. Runtime data menus have a symbolic `menuProvider` (`FieldProviders` / `InputProviders` in Go) and separate builtin values. Rename, delete, new-message and record menu commands are excluded. Providers must resolve against the actual frozen project/target; fixture choices are not a production enum.

State storage is polymorphic: `data_setvariableto.VALUE` and the `ITEM` ports of `data_addtolist`, `data_insertatlist`, and `data_replaceitemoflist` use `any`, retaining the native `text` fallback shadow. Their native inputs have no connection check, and the VM stores the supplied value without a string cast. Numeric literals compile to numeric shadows; text literals keep their text representation. Connected reporters retain the editable text fallback. This reviewed exception does not widen unrelated text ports such as `looks_say.MESSAGE`. The compiler and real VM regression in `frontend/tests/polymorphic-state-values.test.ts` verifies numeric and text values, mixed lists, boolean reporter output, and SB3 round-trip preservation.

`control_stop` retains the current cap policy. Its source variants document that “other scripts in sprite” has a next connection; recording this native fact does not enable continuation in the existing compiler. Native `math_whole_number` and other GUI shadows likewise remain distinct from the compiler's reviewed shadow choices. Procedures, hidden compatibility blocks and other registered types are not automatically added to the AI allowlist. Full browser rendering and runtime execution remain separate verification layers from this build-time structural probe.
