# Final deployed-page acceptance: Agnes

The [unchanged browser report](report.json) records the actual deployed bundle `index-DGU_tyKe.js`, `normal` tier, committed native Scratch blocks, movement to `(30,0)`, successful undo restoring an empty script graph and the original logical sprite under its newly allocated ID, and no page errors. [Screenshots](committed.png) and [undo screenshot](undone.png) capture both states.

[Recorded-SSE postcheck](postcheck.json) independently parses both HTTP streams, checks successful terminal events and absence of errors, and confirms actual `usage.provider: agnes` for both calls: 6,916 reported tokens. This total is separate from the DeepSeek suite and earlier Agnes suite history. `generation.json.gz` contains the complete requests and response streams; [archive.json](archive.json) records original SHA-256/size.

The UI check observes the requested position. The same generated ProgramIR additionally passed [the independent real VM check](vm-result.json) after 20 ticks and actual SB3 export/new-VM load. No `control_forever` is present. The [input](vm-input.json) supplies the original prompt's expected final position independently.

The browser driver was subsequently strengthened to wait for stopped threads, assert final coordinates, and validate parsed terminal events and usage provider directly. Provider/terminal validation and the independent final-position oracle were applied to this saved run offline without another model call; the browser report predates the direct in-page thread-stop assertion. This is one Agnes movement scenario, not an all-six Agnes acceptance claim.
