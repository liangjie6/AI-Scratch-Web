import {describe, expect, it} from 'vitest';
import {requestsGenerationNow} from '../src/ai-blocks/generation-intent';

describe('AI block generation intent', () => {
  it.each([
    '开始编写',
    '现在直接生成代码',
    '不要再问了，按默认即可',
    '直接生成一个小游戏',
    'go ahead and build it'
  ])('recognizes an explicit proceed command: %s', text => {
    expect(requestsGenerationNow(text)).toBe(true);
  });

  it.each([
    '让角色移动十步',
    '我还没想好',
    '你能做什么'
  ])('does not treat an ordinary message as a forced proceed command: %s', text => {
    expect(requestsGenerationNow(text)).toBe(false);
  });
});
