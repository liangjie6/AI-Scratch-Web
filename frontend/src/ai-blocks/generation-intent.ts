const CHINESE_PROCEED_PATTERNS = [
  /(?:开始|现在|立即|马上|直接|赶紧).{0,8}(?:写|编写|生成|制作|创建|实现|做)/i,
  /(?:写|编写|生成|制作|创建|实现|做).{0,8}(?:吧|即可|就行|代码|积木|程序)/i,
  /(?:不用|不要|无需|别再?).{0,6}(?:问|确认|澄清)/i,
  /(?:按|用|使用).{0,3}默认/i,
  /^(?:开始|继续|确认|可以|就这样|直接做|开始吧)[\s。！!]*$/i
];

const ENGLISH_PROCEED_PATTERN = /\b(?:start|go ahead|build it|generate it|write it|use defaults?|no more questions?)\b/i;

export function requestsGenerationNow(text: string): boolean {
  const normalized = text.trim();
  return CHINESE_PROCEED_PATTERNS.some(pattern => pattern.test(normalized)) ||
    ENGLISH_PROCEED_PATTERN.test(normalized);
}
