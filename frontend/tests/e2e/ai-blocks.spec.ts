import {expect, test, type Page} from '@playwright/test';

const NL = String.fromCharCode(10);

async function waitForEditor(page: Page) {
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 30_000});
  await expect(page.locator('canvas')).toHaveCount(3);
  await expect(page.getByRole('button', {name: '角色1'})).toBeVisible({timeout: 30_000});
}

const requirements = {
  ready: true,
  title: '绿旗后移动角色',
  goal: '点击绿旗后让当前角色移动 10 步',
  behavior_steps: ['添加绿旗事件', '让角色移动 10 步'],
  triggers: ['点击绿旗'],
  controls: [],
  success_criteria: ['角色向当前方向移动 10 步'],
  missing_questions: []
};

const program = {
  id: 'script_green_flag_move',
  purpose: '绿旗点击后移动角色',
  x: 40,
  y: 40,
  blocks: [
    {opcode: 'event_whenflagclicked'},
    {opcode: 'motion_movesteps', inputs: {STEPS: {kind: 'number', value: 10}}}
  ]
};

function frame(requestId: string, event: string, data: Record<string, unknown>, phase?: string) {
  return 'event: ' + event + NL + 'data: ' + JSON.stringify({request_id: requestId, ...(phase ? {phase} : {}), data}) + NL + NL;
}

function unifiedStream(requestId: string, scope: string, value = program) {
  const plan = {steps: ['添加绿旗事件', '添加移动积木']};
  const manifest = {
    schema_version: 2,
    scope: 'project',
    summary: '为项目添加绿旗移动逻辑',
    resource_ops: [],
    program_targets: [{target: {kind: 'current_target'}, mode: 'append', intent: '让当前目标移动'}],
    shared_state: {variables: [], lists: [], broadcasts: []}
  };
  return [
    frame(requestId, 'phase_start', {message: '整理需求'}, 'requirements'),
    frame(requestId, 'phase_complete', requirements, 'requirements'),
    frame(requestId, 'phase_start', {message: '规划实现'}, 'plan'),
    frame(requestId, 'phase_complete', scope === 'project' ? {...plan, manifest} : plan, 'plan'),
    frame(requestId, 'phase_start', {target_index: 0}, 'program'),
    frame(requestId, 'metadata', {target_index: 0, schema_version: 1, summary: requirements.title, remove_script_ids: []}, 'program'),
    frame(requestId, 'shared_state', {target_index: 0, variables: [], lists: [], broadcasts: []}, 'program'),
    frame(requestId, 'script_ready', {target_index: 0, index: 0, total: 1, script: value}, 'program'),
    frame(requestId, 'phase_complete', {target_index: 0, script_count: 1}, 'program'),
    frame(requestId, 'complete', {scope, program_count: 1})
  ].join('');
}

test('opens AI blocks when randomUUID is unavailable on a LAN origin', async ({page}) => {
  const pageErrors: Error[] = [];
  page.on('pageerror', error => pageErrors.push(error));
  await page.addInitScript(() => {
    Object.defineProperty(Crypto.prototype, 'randomUUID', {configurable: true, value: undefined});
  });
  await waitForEditor(page);
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await expect(page.getByRole('heading', {name: 'AI 写积木'})).toBeVisible();
  await expect(page.getByRole('region', {name: 'AI 写积木'}).getByText(/角色.*角色1/)).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('uses one unified request and commits the validated program atomically', async ({page}) => {
  let requestCount = 0;
  let generationRequest: Record<string, any> | null = null;
  await page.route('**/api/ai-blocks/generate-unified', route => {
    requestCount += 1;
    const request = route.request().postDataJSON() as Record<string, any>;
    generationRequest = request;
    return route.fulfill({status: 200, contentType: 'text/event-stream; charset=utf-8', body: unifiedStream(request.request_id, request.scope)});
  });
  await waitForEditor(page);
  const originalBlocks = await page.locator('.blocklyDraggable').count();
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await page.getByLabel('描述要实现的积木功能').fill('点击绿旗后移动 10 步');
  await page.getByRole('button', {name: '发送需求'}).click();
  await expect(page.getByText(/已完成统一生成并写入 1 个目标/)).toBeVisible({timeout: 30_000});
  await expect.poll(() => page.locator('.blocklyDraggable').count()).toBeGreaterThan(originalBlocks);
  expect(requestCount).toBe(1);
  expect(generationRequest).toMatchObject({scope: 'single_sprite', target: {name: '角色1', is_stage: false, mode: 'append'}});
  await expect(page.getByRole('button', {name: '撤销生成'})).toBeVisible();
});

test('runs the same unified flow for a project-scoped program', async ({page}) => {
  let generationRequest: Record<string, any> | null = null;
  await page.route('**/api/ai-blocks/generate-unified', route => {
    const request = route.request().postDataJSON() as Record<string, any>;
    generationRequest = request;
    return route.fulfill({status: 200, contentType: 'text/event-stream; charset=utf-8', body: unifiedStream(request.request_id, request.scope)});
  });
  await waitForEditor(page);
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await page.getByText('整个项目', {exact: true}).click();
  await page.getByLabel('描述要实现的积木功能').fill('让项目中的当前角色移动');
  await page.getByRole('button', {name: '发送需求'}).click();
  await expect(page.getByText(/已完成统一生成并写入 1 个目标/)).toBeVisible({timeout: 30_000});
  expect(generationRequest).toMatchObject({scope: 'project', project_context: {project_id: 'scratch-project'}});
});

test('cancels unified preparation without changing the Scratch workspace', async ({page}) => {
  let release!: () => void;
  const gate = new Promise<void>(resolve => {release = resolve;});
  await page.route('**/api/ai-blocks/generate-unified', async route => {
    await gate;
    try {
      const request = route.request().postDataJSON();
      await route.fulfill({status: 200, contentType: 'text/event-stream; charset=utf-8', body: unifiedStream(request.request_id, request.scope)});
    } catch {
      // The browser may abort the route before the gate is released.
    }
  });
  await waitForEditor(page);
  const originalBlocks = await page.locator('.blocklyDraggable').count();
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await page.getByLabel('描述要实现的积木功能').fill('生成一个移动脚本');
  await page.getByRole('button', {name: '发送需求'}).click();
  const cancelButton = page.getByRole('button', {name: '取消', exact: true});
  await expect(cancelButton).toBeVisible({timeout: 30_000});
  await cancelButton.click();
  release();
  await expect(page.getByText('已取消生成，Scratch 项目未被修改。')).toBeVisible({timeout: 30_000});
  await expect(page.locator('.blocklyDraggable')).toHaveCount(originalBlocks);
});
