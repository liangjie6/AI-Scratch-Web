# AI Scratch Editor - Task Intent

## Requested outcome

Build and verify the local Scratch editor and AI image workflow defined in
[计划大纲](../../../../产品设计/计划大纲.md)。

## Goal

Deliver a Chinese, locally hosted Scratch 3 editor with standard SB3 import and
export. A FastAPI backend is the only owner of the Sui Xiang image provider
contract. Users generate a 2x2 candidate sheet, choose one cell, and import the
cropped image into the VM as a real backdrop or sprite costume.

## Success evidence

- Frontend build, unit tests, backend tests, and Playwright pass.
- Offline network audit proves the editor and local assets do not use Scratch
  CDN or platform APIs.
- Frontend bundle, browser requests, and browser storage contain no API key.
- Both backdrop and sprite imports survive SB3 download and reload.
- A real Sui Xiang smoke test succeeds when a service key is available.

## Stop condition

- `done`: every plan acceptance item has current evidence.
- `blocked`: an external dependency or required secret prevents further safe
  progress after mock and local paths have been exhausted.
- `needs-verification`: implementation exists but browser, SB3, offline, or
  real-provider evidence is incomplete.
- `scope-exceeded`: continuing would require modifying Scratch VM internals or
  adding a plan non-goal.

## Scope and non-goals

In scope: Vue/Vite host, Scratch GUI/VM integration, local static resources,
FastAPI image proxy, 2x2 selection/cropping, VM asset import, SB3 persistence,
security and offline verification.

Out of scope: community publishing, cloud variables, Backpack, online asset
libraries, long-term image hosting, a custom block interpreter, and edits to
the saved competitor snapshot.

## Baseline read set

- [计划大纲](../../../../产品设计/计划大纲.md): intent, ownership, compatibility, verification, and drift
  constraints.
- `D:/ywj/各种参考文档/Scratch网页竞品/图形化编程-自由创作区.html`: UI and
  initialization reference only.
- `D:/ywj/各种参考文档/Scratch网页竞品/图形化编程-自由创作区_files/material-center.js.下载`:
  public library hook reference only.
- `C:/Users/Think/.agents/skills/sui-xiang-imagegen/SKILL.md`: fixed provider
  URL, model, and OpenAI-compatible call shape. Its hardcoded skill secret is
  not copied into this project.

## Baseline usage

All required baseline refs were read before source edits. The competitor
snapshot remains read-only. Current package discovery found the aligned
`@scratch/*` 15.0.1 release set. The project begins at Git baseline `d9f4256`.

## Impact statement

The new project creates a local editor and a single explicit provider network
path. Scratch VM remains the canonical project and asset owner. Provider
details remain behind the backend route, and UI logic cannot directly mutate
VM target internals.

## Execution readiness view

- Intent lock: local Scratch editor plus AI candidate-sheet import.
- Scope fence: only plan-defined frontend, backend, assets, tests, and docs.
- Baseline lock: reuse behavior and public APIs; do not edit snapshots/bundles.
- Owner constraints: provider in backend, cropper in selector utility, VM
  conversion in `ScratchAssetAdapter`.
- Compatibility: standard Scratch 3 opcodes, coordinates, and SB3 format.
- Retirement: provider-specific code remains replaceable in its provider file.
- Task batches: baseline, Scratch host, backend, AI UI, asset import, release QA.
- Test obligations: focused tests per batch and final browser/offline/SB3 audit.
- Review gates: API contract, public VM asset path, key/external-link scan, UI.
- Drift rule: pause before direct mutation of VM target internals.
