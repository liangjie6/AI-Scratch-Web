export const SCRATCH_LIBRARY_ALIASES: Record<string, string[]> = {
  角色: ['sprite', 'character', 'person', 'animal'],
  人物: ['people', 'person'],
  猫: ['cat', 'animal'],
  狗: ['dog', 'animal'],
  背景: ['backdrop', 'background'],
  舞台: ['stage', 'backdrop'],
  太空: ['space'],
  海底: ['underwater', 'ocean'],
  城堡: ['castle'],
  森林: ['forest'],
};

export function expandScratchSearchTerms(terms: string[]) {
  const expanded = new Set<string>();
  for (const term of terms) {
    const normalized = term.trim().toLocaleLowerCase();
    if (!normalized) continue;
    expanded.add(normalized);
    for (const alias of SCRATCH_LIBRARY_ALIASES[term.trim()] ?? []) expanded.add(alias);
  }
  return [...expanded];
}
