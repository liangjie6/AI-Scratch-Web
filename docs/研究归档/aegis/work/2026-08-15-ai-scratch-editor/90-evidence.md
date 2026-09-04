# AI Scratch Editor - Evidence

This file accumulates concise, reproducible evidence for each implementation
slice. Raw build and test output is not committed unless it is needed to
diagnose a failure.

## Baseline

- Plan baseline commit: `d9f4256 docs: add implementation plan`.
- Package discovery: `@scratch/scratch-gui`, VM, renderer, storage, and SVG
  renderer have aligned npm version `15.0.1`.
- Runtime discovery: Node 24.15.0, npm 11.12.1, Python 3.12.1.

## Implementation verification

- `backend\\.venv\\Scripts\\python.exe -m pytest backend/tests -q`: 7 passed.
- `npm --prefix frontend run test:unit`: 4 passed.
- `npm --prefix frontend run build`: production bundle completed.
- `npm --prefix frontend run test:e2e`: 4 passed (desktop offline audit, 390px
  layout, backdrop import, sprite import, SB3 download and reload).
- Browser request audit: no request outside `http://127.0.0.1:5173` during the
  local editor load.
- SB3 evidence: `evidence-project.sb3` contains `project.json` and the imported
  MD5-named PNG costume asset; the sprite survived browser reload.
- Frontend bundle scan: no `SUI_XIANG_API_KEY` value or provider URL in `dist`.
