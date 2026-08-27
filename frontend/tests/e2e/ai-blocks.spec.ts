import {expect, test, type Page} from '@playwright/test';

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
  schema_version: 1,
  summary: '绿旗后移动角色',
  remove_script_ids: [],
  variables: [],
  lists: [],
  broadcasts: [],
  scripts: [{
    id: 'script_green_flag_move',
    purpose: '绿旗点击后移动角色',
    x: 40,
    y: 40,
    blocks: [
      {opcode: 'event_whenflagclicked'},
      {opcode: 'motion_movesteps', inputs: {STEPS: {kind: 'number', value: 10}}}
    ]
  }]
};

test('opens AI blocks when randomUUID is unavailable on a LAN origin', async ({page}) => {
  const pageErrors: Error[] = [];
  page.on('pageerror', error => pageErrors.push(error));
  await page.addInitScript(() => {
    Object.defineProperty(Crypto.prototype, 'randomUUID', {configurable: true, value: undefined});
  });

  await waitForEditor(page);
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();

  await expect(page.getByRole('heading', {name: 'AI 写积木'})).toBeVisible();
  await expect(page.getByText(/角色 · 角色1/)).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('uses defaults, locks, and directly injects AI blocks for a simple request', async ({page}) => {
  let chatRequests = 0;
  let releaseGeneration!: () => void;
  const generationGate = new Promise<void>(resolve => { releaseGeneration = resolve; });
  let generateRequest: Record<string, unknown> | null = null;

  await page.route('**/api/ai-blocks/chat', route => {
    chatRequests += 1;
    return route.fulfill({
      status: 200,
      contentType: 'text/event-stream; charset=utf-8',
      body: 'event: message_complete\ndata: {}\n\n'
    });
  });
  await page.route('**/api/ai-blocks/requirements', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(requirements)
  }));
  await page.route('**/api/ai-blocks/generate', async route => {
    generateRequest = route.request().postDataJSON();
    await generationGate;
    await route.fulfill({
      status: 200,
      contentType: 'text/event-stream; charset=utf-8',
      body: 'event: phase\ndata: {"phase":"planning","message":"正在规划积木步骤"}\n\n' +
        'event: plan_ready\ndata: {"steps":["添加绿旗事件","添加移动积木"]}\n\n' +
        'event: phase\ndata: {"phase":"generating","message":"正在生成积木程序"}\n\n' +
        'event: program_ready\ndata: ' + JSON.stringify({program}) + '\n\n' +
        'event: message_complete\ndata: {}\n\n'
    });
  });

  await waitForEditor(page);
  const originalBlocks = await page.locator('.blocklyDraggable').count();
  const aiButton = page.getByRole('button', {name: '打开 AI 写积木'});
  await expect(aiButton).toBeVisible();
  await aiButton.click();
  await expect(page.getByRole('heading', {name: 'AI 写积木'})).toBeVisible();
  await expect(page.getByText(/角色 · 角色1/)).toBeVisible();

  const prompt = page.getByLabel('描述要实现的积木功能');
  await prompt.fill('点击绿旗后移动 10 步');
  await page.getByRole('button', {name: '发送需求'}).click();

  await expect(page.getByText('AI 正在规划代码，请稍等')).toBeVisible();
  await expect(page.locator('.scratch-host__container')).toHaveAttribute('inert', '');
  await expect(aiButton).toBeDisabled();
  const processBubble = page.locator('.ai-blocks-message__process');
  const panelDots = processBubble.locator('.ai-blocks-working-dots');
  const inlineRing = processBubble.locator('.ai-blocks-inline-spinner');
  await expect(page.getByText('点击绿旗后移动 10 步')).toBeVisible();
  await expect(prompt).toBeVisible();
  await expect(prompt).toBeDisabled();
  await expect(processBubble).toContainText('正在推理并规划代码');
  await expect(panelDots).toBeVisible();
  await expect(inlineRing).toBeVisible();
  await expect(page.locator('.scratch-generation-lock')).toHaveCount(0);
  await expect(processBubble.locator('.ai-blocks-progress__track.is-indeterminate')).toBeVisible();
  expect(await panelDots.locator('span').first().evaluate(element => getComputedStyle(element).animationName))
    .toBe('ai-blocks-working-dot');
  expect(await inlineRing.evaluate(element => getComputedStyle(element).animationName))
    .toBe('ai-blocks-ring-spin');
  const initialRingTransform = await inlineRing.evaluate(element => getComputedStyle(element).transform);
  await expect.poll(() => inlineRing.evaluate(element => getComputedStyle(element).transform))
    .not.toBe(initialRingTransform);
  await page.emulateMedia({reducedMotion: 'reduce'});
  await expect(inlineRing).toHaveCSS('animation-iteration-count', 'infinite');
  const reducedMotionTransform = await inlineRing.evaluate(element => getComputedStyle(element).transform);
  await expect.poll(() => inlineRing.evaluate(element => getComputedStyle(element).transform))
    .not.toBe(reducedMotionTransform);
  await page.emulateMedia({reducedMotion: 'no-preference'});
  expect(await processBubble.locator('.ai-blocks-progress__track.is-indeterminate span').evaluate(element =>
    getComputedStyle(element).animationName
  )).toBe('ai-blocks-indeterminate');
  await page.screenshot({path: 'test-results/ai-blocks-loading-1280.png'});
  await page.setViewportSize({width: 390, height: 844});
  await expect(processBubble).toBeVisible();
  await expect(prompt).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({path: 'test-results/ai-blocks-loading-390.png'});
  await page.setViewportSize({width: 1280, height: 720});
  releaseGeneration();

  await expect(page.getByText(/积木生成完成。已为角色1新增 2 块积木、1 个脚本/)).toBeVisible({timeout: 30_000});
  await expect(page.getByRole('heading', {name: '积木生成完成'})).toHaveCount(0);
  await expect(page.getByRole('button', {name: '运行查看'})).toBeVisible();
  await expect(page.getByRole('button', {name: '撤销生成'})).toBeVisible();
  await expect(page.locator('.ai-blocks-count')).toHaveText('2 块');
  await expect.poll(() => page.locator('.blocklyDraggable').count()).toBeGreaterThan(originalBlocks);
  await expect(page.getByText(/AI 生成：绿旗点/)).toBeVisible();
  const xField = page.getByRole('textbox', {name: 'x'});
  await expect(xField).toHaveValue('0');
  await page.getByRole('button', {name: '运行查看'}).click();
  await expect.poll(() => xField.inputValue()).toBe('10');
  expect(generateRequest).toMatchObject({target: {name: '角色1', is_stage: false, mode: 'append'}});
  expect(chatRequests).toBe(0);
  await page.screenshot({path: 'test-results/ai-blocks-complete-1280.png'});

  await expect(page.getByText('修改当前代码')).toBeVisible();
  await expect(page.getByText('旁路新增')).toBeVisible();
  await expect(page.getByText('清空重写')).toBeVisible();

  await page.setViewportSize({width: 390, height: 844});
  await expect(page.locator('.workspace__ai')).toHaveCSS('width', '390px');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({path: 'test-results/ai-blocks-390.png'});
});

test('stops asking and generates when the user says to start with defaults', async ({page}) => {
  const requirementRequests: Record<string, unknown>[] = [];
  let requirementCall = 0;
  const missingCoreBehavior = {
    ready: false,
    title: '',
    goal: '',
    behavior_steps: [],
    triggers: [],
    controls: [],
    success_criteria: [],
    missing_questions: ['希望角色实现什么主要效果？']
  };

  await page.route('**/api/ai-blocks/requirements', route => {
    requirementRequests.push(route.request().postDataJSON());
    requirementCall += 1;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(requirementCall === 1 ? missingCoreBehavior : requirements)
    });
  });
  await page.route('**/api/ai-blocks/generate', route => route.fulfill({
    status: 200,
    contentType: 'text/event-stream; charset=utf-8',
    body: 'event: phase\ndata: {"phase":"planning","message":"正在规划积木步骤"}\n\n' +
      'event: plan_ready\ndata: {"steps":["使用默认值实现功能"]}\n\n' +
      'event: program_ready\ndata: ' + JSON.stringify({program}) + '\n\n' +
      'event: message_complete\ndata: {}\n\n'
  }));

  await waitForEditor(page);
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  const prompt = page.getByLabel('描述要实现的积木功能');
  await prompt.fill('帮我做一个角色效果');
  await page.getByRole('button', {name: '发送需求'}).click();
  await expect(page.getByText('希望角色实现什么主要效果？')).toBeVisible();

  await prompt.fill('开始编写，按默认即可');
  await page.getByRole('button', {name: '发送需求'}).click();
  await expect(page.getByText(/积木生成完成。已为/)).toBeVisible({timeout: 30_000});

  expect(requirementRequests).toHaveLength(2);
  expect(requirementRequests[1]).toMatchObject({proceed_with_defaults: true});
});

test('generates native blocks for the selected stage', async ({page}) => {
  let generateRequest: Record<string, unknown> | null = null;
  const stageRequirements = {
    ...requirements,
    title: '绿旗后切换背景',
    goal: '点击绿旗后切换到下一个背景',
    behavior_steps: ['添加绿旗事件', '切换到下一个背景'],
    success_criteria: ['舞台切换背景']
  };
  const stageProgram = {
    ...program,
    summary: '绿旗后切换背景',
    scripts: [{
      id: 'script_stage_backdrop',
      purpose: '绿旗点击后切换背景',
      x: 40,
      y: 40,
      blocks: [
        {opcode: 'event_whenflagclicked'},
        {opcode: 'looks_nextbackdrop'}
      ]
    }]
  };
  await page.route('**/api/ai-blocks/chat', route => route.fulfill({
    status: 200,
    contentType: 'text/event-stream; charset=utf-8',
    body: 'event: assistant_delta\ndata: {"text":"确认：绿旗后切换到下一个背景。"}\n\n' +
      'event: message_complete\ndata: {}\n\n'
  }));
  await page.route('**/api/ai-blocks/requirements', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(stageRequirements)
  }));
  await page.route('**/api/ai-blocks/generate', route => {
    generateRequest = route.request().postDataJSON();
    return route.fulfill({
      status: 200,
      contentType: 'text/event-stream; charset=utf-8',
      body: 'event: phase\ndata: {"phase":"planning","message":"正在规划积木步骤"}\n\n' +
        'event: plan_ready\ndata: {"steps":["添加绿旗事件","切换背景"]}\n\n' +
        'event: program_ready\ndata: ' + JSON.stringify({program: stageProgram}) + '\n\n' +
        'event: message_complete\ndata: {}\n\n'
    });
  });

  await waitForEditor(page);
  await page.locator('[class*="stage-selector_stage-selector"]').click();
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await expect(page.getByText(/舞台 · /)).toBeVisible();
  await page.getByLabel('描述要实现的积木功能').fill('点击绿旗后切换背景');
  await page.getByRole('button', {name: '发送需求'}).click();
  await expect(page.getByText(/积木生成完成。已为/)).toBeVisible({timeout: 30_000});
  await expect(page.getByText(/AI 生成：绿旗点/)).toBeVisible();
  expect(generateRequest).toMatchObject({target: {is_stage: true, mode: 'append'}});
});

test('cancels progressive injection and restores the Scratch workspace', async ({page}) => {
  const longProgram = {
    ...program,
    summary: '生成较长脚本以测试取消',
    scripts: [{
      id: 'script_cancel_test',
      purpose: '取消恢复测试',
      x: 40,
      y: 40,
      blocks: [
        {opcode: 'event_whenflagclicked'},
        ...Array.from({length: 80}, (_, index) => ({
          opcode: 'looks_say',
          inputs: {MESSAGE: {kind: 'text', value: '步骤 ' + (index + 1)}}
        }))
      ]
    }]
  };
  await page.route('**/api/ai-blocks/chat', route => route.fulfill({
    status: 200,
    contentType: 'text/event-stream; charset=utf-8',
    body: 'event: assistant_delta\ndata: {"text":"需求已确认。"}\n\n' +
      'event: message_complete\ndata: {}\n\n'
  }));
  await page.route('**/api/ai-blocks/requirements', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(requirements)
  }));
  await page.route('**/api/ai-blocks/generate', route => route.fulfill({
    status: 200,
    contentType: 'text/event-stream; charset=utf-8',
    body: 'event: phase\ndata: {"phase":"planning","message":"正在规划积木步骤"}\n\n' +
      'event: reasoning_delta\ndata: {"text":"先建立绿旗事件，再按顺序编写每条消息。","phase":"planning"}\n\n' +
      'event: plan_ready\ndata: {"steps":["创建长脚本"]}\n\n' +
      'event: program_ready\ndata: ' + JSON.stringify({program: longProgram}) + '\n\n' +
      'event: message_complete\ndata: {}\n\n'
  }));

  await waitForEditor(page);
  const originalBlocks = await page.locator('.blocklyDraggable').count();
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await page.getByLabel('描述要实现的积木功能').fill('依次说出十二条消息');
  await page.getByRole('button', {name: '发送需求'}).click();
  const cancelButton = page.getByRole('button', {name: '取消生成'});
  await expect(cancelButton).toBeVisible({timeout: 30_000});
  const processBubble = page.locator('.ai-blocks-message__process');
  await expect(processBubble).toContainText('推理过程');
  await expect(processBubble).toContainText('先建立绿旗事件，再按顺序编写每条消息。');
  await expect(page.getByLabel('描述要实现的积木功能')).toBeVisible();
  await expect.poll(() => page.locator('.blocklyDraggable').count()).toBeGreaterThan(originalBlocks);
  await cancelButton.click();
  await expect(page.getByText('已取消生成，项目已恢复到生成前状态。')).toBeVisible({timeout: 30_000});
  await expect(page.locator('.blocklyDraggable')).toHaveCount(originalBlocks);
  await expect(page.locator('.scratch-host__container')).not.toHaveAttribute('inert', '');
});

