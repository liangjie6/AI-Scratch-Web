from __future__ import annotations

import re


_PATH_HINTS = (
    ('resource_ops', '资源操作必须严格匹配资源契约；检查 op、op_id、target、source 以及互斥字段。'),
    ('program_targets', '项目目标必须引用舞台、已有角色、已创建角色或 current_target，并按清单顺序生成。'),
    ('shared_state', '项目全局变量、列表和广播必须与 manifest 完整一致；目标私有变量和列表要标记为 local。'),
    ('scripts', '脚本 id、数量、积木层级和输入类型必须符合 ProgramIR 与原生积木目录。'),
    ('blocks', '检查 opcode、inputs、substacks 和 reporter 类型，不要输出目录之外的积木。'),
    ('current_target', '当前目标才允许使用 modify 或 rewrite；其他项目目标只能使用 append。'),
)

_DETAIL_HINTS = (
    ('trailing content', '删除 JSON 对象后的所有额外文本。'),
    ('invalid JSON', '只输出一个合法 JSON 对象，不要使用 Markdown 代码围栏。'),
    ('required input is missing', '补齐该 opcode 契约声明的所有必需输入。'),
    ('expected boolean', '将布尔输入改为嵌套 boolean reporter，而不是普通字符串。'),
    ('unknown opcode', '改用 BLOCK_CATALOG 中存在的原生 opcode。'),
)


def _array_index(path: str) -> str:
    match = re.search(r'(?:resource_ops|program_targets)(?:\.|\[)(\d+)', path)
    return f'这是数组第 {int(match.group(1)) + 1} 项。' if match else ''


def repair_hint(phase: str | None, path: str | None, detail: str | None) -> str:
    normalized_path = str(path or '')
    normalized_detail = str(detail or '')
    for marker, hint in _PATH_HINTS:
        if marker in normalized_path or marker in normalized_detail:
            return f'{_array_index(normalized_path)}{hint}'
    for marker, hint in _DETAIL_HINTS:
        if marker.lower() in normalized_detail.lower():
            return hint
    if phase == 'program':
        return '对照 ProgramIR schema 与 BLOCK_CATALOG 逐项检查 metadata、shared_state 和 scripts。'
    if phase == 'manifest':
        return '对照 ProjectManifest v2 检查资源操作、目标引用和 shared_state。'
    return '对照当前 segment 的 FINAL OUTPUT CONTRACT 检查字段、类型和输出顺序。'
