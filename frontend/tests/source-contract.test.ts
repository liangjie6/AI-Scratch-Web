import {describe, expect, it} from 'vitest';
import fixture from './fixtures/source-project.json';
import {assertFrozenSourceContract, sourceCanonicalJson, sourceContractSha256, sourceRequestBindingHash, sourceSnapshotHash, validateFrozenProjection, type FrozenSourceContract} from '../src/ai-blocks/source-contract';
import {contractSha256, type ProjectBlueprint} from '../src/ai-blocks/project-contract';
import type {ProjectManifest} from '../src/ai-blocks/project-plan';
import type {UnifiedGenerationRequest} from '../src/ai-blocks/ai-blocks-api';

// Regenerate in backend-go with AUTHORING_FIXTURE_OUTPUT=/tmp/source-project-fixture.json go test ./internal/authoring -count=1.
// Copy that real Go Freeze/DeriveLegacy result into tests/fixtures/source-project.json.
const request = fixture.request as unknown as UnifiedGenerationRequest;
const contract = fixture.source_contract as unknown as FrozenSourceContract;
const blueprint = fixture.blueprint as unknown as ProjectBlueprint;
const manifest = fixture.manifest as unknown as ProjectManifest;

describe('frozen Scratch source contract', () => {
  it('matches real Go Freeze hashes, including null fields and the cleared inner hash', async () => {
    expect(sourceCanonicalJson({b: null, a: 1})).toBe('{"a":1,"b":null}');
    expect(await sourceContractSha256(contract)).toBe(fixture.source_contract_hash);
    expect(await sourceContractSha256({...contract, hash: ''})).toBe(contract.hash);
    expect(await sourceSnapshotHash(request)).toBe(contract.base_snapshot_hash);
    expect(await sourceRequestBindingHash(request)).toBe(contract.request_binding_hash);
    expect(await contractSha256(blueprint)).toBe(fixture.blueprint_hash);
    expect(await assertFrozenSourceContract(contract, fixture.source_contract_hash, request)).toBe(contract);
  });

  it('matches real Go target/state/slot/resource projections and excludes the resource-only stage from jobs', async () => {
    expect(contract.targets).toHaveLength(3);
    expect(manifest.program_targets).toHaveLength(2);
    expect(manifest.program_targets.map(item => item.owner_entity_id)).not.toContain(contract.targets.find(item => item.kind === 'stage')!.entity_id);
    expect(await validateFrozenProjection(contract, blueprint, manifest)).toEqual([]);
  });

  it('rejects modified inner contracts even if the full envelope hash was recomputed', async () => {
    const changed = structuredClone(contract);
    changed.states[0].value = 123;
    await expect(assertFrozenSourceContract(changed, await sourceContractSha256(changed), request)).rejects.toThrow('哈希');
  });

  it.each(['session_id', 'request_id'] as const)('rejects a different %s even when the project snapshot is unchanged', async key => {
    const changedRequest = {...request, [key]: request[key] + '-different'};
    expect(await sourceSnapshotHash(changedRequest)).toBe(contract.base_snapshot_hash);
    await expect(assertFrozenSourceContract(contract, fixture.source_contract_hash, changedRequest)).rejects.toThrow('会话或请求 ID');
  });

  it('requires the code-owned request binding rather than falling back to the project snapshot', async () => {
    const changed = structuredClone(contract) as Partial<FrozenSourceContract>;
    delete changed.request_binding_hash;
    changed.hash = await sourceContractSha256({...changed, hash: ''});
    await expect(assertFrozenSourceContract(changed, await sourceContractSha256(changed), request)).rejects.toThrow('绑定无效');
  });

  it('requires a positive integer expiry while allowing historical artifacts to be decoded', async () => {
    for (const expiresAt of [0, -1, 1.5]) {
      const changed = {...contract, expires_at_unix: expiresAt};
      changed.hash = await sourceContractSha256({...changed, hash: ''});
      await expect(assertFrozenSourceContract(changed, await sourceContractSha256(changed), request)).rejects.toThrow('绑定无效');
    }
    const historical = {...contract, expires_at_unix: 1};
    historical.hash = await sourceContractSha256({...historical, hash: ''});
    await expect(assertFrozenSourceContract(historical, await sourceContractSha256(historical), request)).resolves.toBe(historical);
  });

  it('checks the full projection rather than accepting a valid-looking manifest without legacy TaskSpec', async () => {
    const changedState = structuredClone(manifest);
    changedState.shared_state.variables[0].value = 999;
    expect(await validateFrozenProjection(contract, blueprint, changedState)).toEqual(expect.arrayContaining([expect.objectContaining({path: '/manifest'})]));
    const changedSlot = structuredClone(blueprint);
    changedSlot.behavior_graph[0].owner_entity_id = contract.targets[2].entity_id;
    expect(await validateFrozenProjection(contract, changedSlot, manifest)).toEqual(expect.arrayContaining([expect.objectContaining({path: '/blueprint'})]));
    const changedResource = structuredClone(manifest);
    const operation = changedResource.resource_ops[0];
    if ('name' in operation) operation.name = 'unrelated resource';
    expect(await validateFrozenProjection(contract, blueprint, changedResource)).toHaveLength(1);
  });

  it('rejects scope/version/catalog mismatches and a current target switch', async () => {
    await expect(assertFrozenSourceContract(contract, fixture.source_contract_hash, {...request, scope: 'single_sprite'})).rejects.toThrow('版本');
    await expect(assertFrozenSourceContract({...contract, catalog_hash: '0'.repeat(64)}, fixture.source_contract_hash, request)).rejects.toThrow('目录');
    await expect(assertFrozenSourceContract(contract, fixture.source_contract_hash, {...request, target: {...request.target, name: 'another sprite'}})).rejects.toThrow('快照');
  });
});
