# AI积木重构 Phase 1 实施文档

## 项目概览

**目标**：在1周内将项目模式AI生成成功率从40%提升到70%+  
**方案**：增强式修复（方案B）  
**适用模型**：DeepSeek V3 / DeepSeek Chat  
**实施周期**：7个工作日  
**风险等级**：低（不改变核心架构，向后兼容）

---

## Day 1-2：提示词重构

### 目标
将复杂的协议描述重构为结构化、易理解的格式，特别针对DeepSeek模型的理解特点优化。

### DeepSeek模型特性分析

根据DeepSeek的训练特点，该模型：
1. **对表格和结构化信息理解更好**（相比长段落描述）
2. **遵循明确规则的能力强**（相比隐含约束）
3. **对中文提示词响应质量高**（中英混合时保持一致性）
4. **对错误案例学习效果好**（few-shot learning）

### 任务1.1：创建资源操作速查表

**文件**：`backend/app/prompts/resource_ops_reference.py`

```python
"""资源操作协议速查表 - 为DeepSeek模型优化"""

RESOURCE_OPS_QUICK_REFERENCE = '''
## 资源操作速查表

所有资源操作必须严格遵循以下字段组合规则。禁止添加未列出的字段。

### 1. add_builtin_sprite - 从Scratch库添加内置角色

**必需字段：**
- `op`: "add_builtin_sprite"
- `op_id`: 唯一标识符（如 "sprite_1"）
- `name`: 角色名称
- `source`: {"kind":"scratch_library", "asset_type":"sprite", "search_terms":[...]}
- `x`: -10000 到 10000 的整数
- `y`: -10000 到 10000 的整数  
- `size`: 1 到 1000 的整数
- `tags`: 字符串数组（可以为空[]）

**禁止字段：**
- ❌ `target`
- ❌ `select_after`
- ❌ `costumes`

**正确示例：**
```json
{
  "op": "add_builtin_sprite",
  "op_id": "sprite_cat",
  "name": "猫咪",
  "source": {
    "kind": "scratch_library",
    "asset_type": "sprite",
    "search_terms": ["猫", "动物", "宠物"]
  },
  "x": 0,
  "y": 0,
  "size": 100,
  "tags": ["主角", "可爱"]
}
```

---

### 2. add_sprite - 创建新角色（带自定义造型）

**必需字段：**
- `op`: "add_sprite"
- `op_id`: 唯一标识符
- `name`: 角色名称
- `source`: 图像生成或costume类型的scratch_library
- `x`, `y`, `size`: 同add_builtin_sprite
- `tags`: 字符串数组
- `costumes`: 造型数组（最多12个）

**禁止字段：**
- ❌ `target`
- ❌ `select_after`

**source限制：**
- 如果用scratch_library，asset_type必须是"costume"（不能是"sprite"）
- 如果用image_generation，必须包含reason, prompt, style, width, height

**正确示例：**
```json
{
  "op": "add_sprite",
  "op_id": "sprite_custom",
  "name": "魔法师",
  "source": {
    "kind": "image_generation",
    "reason": "需要独特的魔法师角色",
    "prompt": "卡通风格的魔法师，穿蓝色长袍，手持魔杖",
    "style": "cartoon",
    "width": 512,
    "height": 512,
    "sheet_layout": "1x1"
  },
  "x": 100,
  "y": 0,
  "size": 80,
  "tags": ["魔法", "主角"],
  "costumes": []
}
```

---

### 3. add_costume - 为角色添加造型

**必需字段：**
- `op`: "add_costume"
- `op_id`: 唯一标识符
- `target`: 目标对象（见下方说明）
- `name`: 造型名称
- `source`: costume类型的素材
- `select_after`: true 或 false（是否切换到此造型）
- `tags`: 字符串数组

**禁止字段：**
- ❌ `x`
- ❌ `y`
- ❌ `size`
- ❌ `costumes`

**target规则：**
- ✅ `{"kind":"current_target"}` - 当前操作的角色
- ✅ `{"kind":"existing_sprite","name":"角色名"}` - 已存在的角色
- ✅ `{"kind":"created_sprite","op_id":"sprite_1"}` - 前面创建的角色
- ❌ `{"kind":"stage"}` - 舞台不能添加costume

**正确示例：**
```json
{
  "op": "add_costume",
  "op_id": "costume_run",
  "target": {"kind": "existing_sprite", "name": "猫咪"},
  "name": "跑步",
  "source": {
    "kind": "scratch_library",
    "asset_type": "costume",
    "search_terms": ["跑", "运动"]
  },
  "select_after": false,
  "tags": ["动作"]
}
```

---

### 4. add_backdrop - 为舞台添加背景

**必需字段：**
- `op`: "add_backdrop"
- `op_id`: 唯一标识符
- `name`: 背景名称
- `source`: backdrop类型的素材
- `select_after`: true 或 false（是否切换到此背景）
- `tags`: 字符串数组

**禁止字段：**
- ❌ `target`（背景永远是给舞台的，不需要指定）
- ❌ `x`
- ❌ `y`
- ❌ `size`
- ❌ `costumes`

**常见错误：**
- ❌ 添加了target字段
- ❌ 缺少select_after字段
- ❌ source的asset_type不是"backdrop"

**正确示例：**
```json
{
  "op": "add_backdrop",
  "op_id": "bg_forest",
  "name": "森林",
  "source": {
    "kind": "scratch_library",
    "asset_type": "backdrop",
    "search_terms": ["森林", "树木", "自然"]
  },
  "select_after": true,
  "tags": ["自然", "户外"]
}
```

---

### 5. delete_sprite - 删除角色

**必需字段：**
- `op`: "delete_sprite"
- `op_id`: 唯一标识符
- `name`: 要删除的角色名
- `target`: {"kind":"existing_sprite","name":"角色名"}

**禁止字段：**
- ❌ `source`
- ❌ `x`, `y`, `size`
- ❌ `select_after`
- ❌ `tags`
- ❌ `costumes`

**正确示例：**
```json
{
  "op": "delete_sprite",
  "op_id": "del_1",
  "name": "Sprite1",
  "target": {"kind": "existing_sprite", "name": "Sprite1"}
}
```

---

### 6. select_backdrop - 切换到已有背景

**必需字段：**
- `op`: "select_backdrop"
- `op_id`: 唯一标识符
- `name`: 已存在的背景名

**禁止字段：**
- ❌ 所有其他字段（source, target, x, y, size, select_after, tags, costumes）

**正确示例：**
```json
{
  "op": "select_backdrop",
  "op_id": "sel_bg_1",
  "name": "backdrop1"
}
```

---

## 字段组合检查清单

生成每个资源操作后，请按以下清单自检：

### 通用检查
- [ ] op字段是否是以上6种之一？
- [ ] op_id是否唯一（不与其他操作重复）？
- [ ] name字段是否存在且非空？

### 位置字段检查（x, y, size）
- [ ] 如果是add_builtin_sprite或add_sprite：必须有x, y, size
- [ ] 如果是其他操作：不能有x, y, size

### target字段检查
- [ ] 如果是add_costume：必须有target且不能是stage
- [ ] 如果是delete_sprite：必须有target且必须是existing_sprite
- [ ] 如果是add_backdrop、add_builtin_sprite、add_sprite、select_backdrop：不能有target

### select_after字段检查
- [ ] 如果是add_costume或add_backdrop：必须有select_after（true或false）
- [ ] 如果是其他操作：不能有select_after

### source字段检查
- [ ] 如果是select_backdrop或delete_sprite：不能有source
- [ ] 如果是其他操作：必须有source
- [ ] add_backdrop的source如果用scratch_library，asset_type必须是"backdrop"
- [ ] add_builtin_sprite的source，asset_type必须是"sprite"
- [ ] add_costume和add_sprite的source如果用scratch_library，asset_type必须是"costume"

### 引用完整性检查
- [ ] created_sprite引用的op_id是否在前面的add_sprite或add_builtin_sprite中定义？
- [ ] 引用顺序是否正确（先定义后引用）？

---

## 错误案例与修正

### 错误案例1：add_backdrop包含target
```json
// ❌ 错误
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "name": "天空",
  "target": {"kind": "stage"},  // ← 错误：不应有target
  "source": {...},
  "select_after": true,
  "tags": []
}

// ✅ 正确
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "name": "天空",
  "source": {...},
  "select_after": true,
  "tags": []
}
```

### 错误案例2：add_backdrop缺少select_after
```json
// ❌ 错误
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "name": "天空",
  "source": {...},
  "tags": []  // ← 错误：缺少select_after
}

// ✅ 正确
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "name": "天空",
  "source": {...},
  "select_after": true,
  "tags": []
}
```

### 错误案例3：add_costume给stage添加造型
```json
// ❌ 错误
{
  "op": "add_costume",
  "op_id": "c1",
  "target": {"kind": "stage"},  // ← 错误：stage不能有costume
  "name": "背景",
  "source": {...},
  "select_after": true,
  "tags": []
}

// ✅ 正确：应该用add_backdrop
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "name": "背景",
  "source": {...},
  "select_after": true,
  "tags": []
}
```

### 错误案例4：created_sprite引用不存在的op_id
```json
// ❌ 错误
{
  "resource_ops": [
    {
      "op": "add_costume",
      "op_id": "c1",
      "target": {"kind": "created_sprite", "op_id": "sprite_1"},  // ← 引用了不存在的op_id
      "name": "造型1",
      ...
    }
  ]
}

// ✅ 正确：先创建角色，再添加造型
{
  "resource_ops": [
    {
      "op": "add_sprite",
      "op_id": "sprite_1",  // ← 先定义
      "name": "新角色",
      ...
    },
    {
      "op": "add_costume",
      "op_id": "c1",
      "target": {"kind": "created_sprite", "op_id": "sprite_1"},  // ← 再引用
      "name": "造型1",
      ...
    }
  ]
}
```

---

## 生成流程建议

1. **先规划后生成**：在脑海中明确需要哪些操作
2. **逐个生成**：一次生成一个操作，确保正确
3. **自检**：生成后对照检查清单验证
4. **检查引用**：确保created_sprite引用的op_id已定义
5. **最终验证**：确认op_id全局唯一

---

## DeepSeek特别提示

DeepSeek模型在处理此类结构化任务时，建议：

1. **先生成outline**：
   ```
   需要的操作：
   1. add_backdrop - 添加森林背景
   2. add_builtin_sprite - 添加猫咪
   3. add_costume - 给猫咪添加跑步造型
   ```

2. **再填充细节**：逐个补充每个操作的完整字段

3. **使用检查清单**：生成后对照上面的检查清单验证

4. **注意字段互斥性**：某些字段组合是互斥的，不能同时出现
'''
```

### 任务1.2：更新主提示词文件

**文件**：`backend/app/prompt_builder.py`

**修改点1**：简化UNIFIED_OUTPUT_CONTRACT

```python
UNIFIED_OUTPUT_CONTRACT = '''
## 最终输出协议（最高优先级）

传输协议：带标签的流式输出，每个标签内是一个JSON对象。

**标签顺序：**
1. <requirements>{RequirementsResponse JSON}</requirements>
2. <plan>{Plan JSON}</plan>
3. [项目模式] <manifest>{ProjectManifest JSON}</manifest>
4. <program target_index="N">...</program>

**重要规则：**
- 标签外禁止输出任何内容（无Markdown、代码围栏、解释）
- 每个标签体是单个严格JSON对象
- 项目模式必须在plan后输出manifest
- 单目标模式不输出manifest

**项目模式manifest结构：**
```json
{
  "schema_version": 1,
  "scope": "project",
  "summary": "项目概述",
  "resource_ops": [...],  // 详见资源操作速查表
  "program_targets": [...],
  "shared_state": {"variables": [], "lists": [], "broadcasts": []}
}
```

**program_targets规则：**
- target必须是对象：{"kind":"stage"} / {"kind":"existing_sprite","name":"..."} / {"kind":"created_sprite","op_id":"..."} / {"kind":"current_target"}
- mode只能是append/modify/rewrite
- 只有current_target可用modify或rewrite
- stage、existing_sprite、created_sprite必须用append

**资源操作详细规则见下方资源操作速查表。**
'''

# 在_build_unified_output_contract方法中引入速查表
def _build_unified_output_contract(self, request: UnifiedGenerationRequest) -> str:
    from .prompts.resource_ops_reference import RESOURCE_OPS_QUICK_REFERENCE
    
    if request.scope == 'project':
        scope_note = (
            '当前是project模式。必须在plan后输出manifest，然后为每个program_targets生成对应程序。'
            '\n\n' + RESOURCE_OPS_QUICK_REFERENCE  # 插入详细速查表
        )
    else:
        scope_note = '当前是single_sprite模式。不输出manifest，只输出一个program（target_index=0）。'
    
    return chr(10).join((UNIFIED_OUTPUT_CONTRACT.strip(), scope_note))
```

### 任务1.3：创建中文友好的错误消息

**文件**：`backend/app/prompts/error_messages.py`

```python
"""错误消息模板 - DeepSeek中文友好版"""

ERROR_MESSAGES = {
    'add_backdrop_missing_select_after': {
        'pattern': r'add_backdrop.*select_after',
        'message': '''
add_backdrop操作错误：缺少select_after字段

add_backdrop必须包含select_after字段（值为true或false），用于指定是否在添加后切换到此背景。

错误的输出：
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "name": "森林",
  "source": {...},
  "tags": []
}

正确的输出：
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "name": "森林",
  "source": {...},
  "select_after": true,  ← 添加此字段
  "tags": []
}
''',
    },
    
    'add_backdrop_has_target': {
        'pattern': r'add_backdrop.*no target',
        'message': '''
add_backdrop操作错误：不应包含target字段

背景永远是添加到舞台的，不需要指定target字段。

错误的输出：
{
  "op": "add_backdrop",
  "target": {"kind": "stage"},  ← 删除此行
  "op_id": "bg1",
  ...
}

正确的输出：
{
  "op": "add_backdrop",
  "op_id": "bg1",
  ...
}
''',
    },
    
    'add_backdrop_has_position': {
        'pattern': r'add_backdrop.*placement',
        'message': '''
add_backdrop操作错误：不应包含位置字段（x, y, size）

背景是全屏的，不需要位置和大小信息。

错误的输出：
{
  "op": "add_backdrop",
  "op_id": "bg1",
  "x": 0,      ← 删除
  "y": 0,      ← 删除
  "size": 100, ← 删除
  ...
}

正确的输出：
{
  "op": "add_backdrop",
  "op_id": "bg1",
  ...
}
''',
    },
    
    'add_costume_missing_target': {
        'pattern': r'add_costume.*target',
        'message': '''
add_costume操作错误：缺少target字段或target不合法

add_costume必须指定为哪个角色添加造型（不能是舞台）。

正确的target示例：
- {"kind": "current_target"} - 当前角色
- {"kind": "existing_sprite", "name": "猫咪"} - 已存在的角色
- {"kind": "created_sprite", "op_id": "sprite_1"} - 前面创建的角色

错误示例：
- 缺少target字段
- {"kind": "stage"} ← 舞台不能有costume，应该用add_backdrop
''',
    },
    
    'add_costume_missing_select_after': {
        'pattern': r'add_costume.*select_after',
        'message': '''
add_costume操作错误：缺少select_after字段

add_costume必须包含select_after字段，指定添加造型后是否切换到此造型。

正确示例：
{
  "op": "add_costume",
  "target": {"kind": "existing_sprite", "name": "猫咪"},
  "name": "跑步",
  "source": {...},
  "select_after": false,  ← 必须有此字段
  "tags": []
}
''',
    },
    
    'created_sprite_invalid_reference': {
        'pattern': r'created_sprite.*reference',
        'message': '''
引用错误：created_sprite引用了不存在的op_id

当target使用created_sprite时，op_id必须引用前面的add_sprite或add_builtin_sprite操作。

检查要点：
1. 被引用的op_id是否存在？
2. 引用顺序是否正确（先定义后使用）？
3. op_id是否拼写正确？

正确示例：
{
  "resource_ops": [
    {
      "op": "add_sprite",
      "op_id": "sprite_hero",  ← 1. 先定义
      "name": "英雄",
      ...
    },
    {
      "op": "add_costume",
      "target": {"kind": "created_sprite", "op_id": "sprite_hero"},  ← 2. 再引用
      "name": "攻击",
      ...
    }
  ]
}
''',
    },
    
    'program_target_mode_invalid': {
        'pattern': r'stage.*existing_sprite.*created_sprite.*append',
        'message': '''
program_targets的mode错误

规则：
- stage、existing_sprite、created_sprite的目标必须使用mode="append"
- 只有current_target可以使用mode="modify"或mode="rewrite"

错误示例：
{"target": {"kind": "stage"}, "mode": "modify"}  ← stage只能用append

正确示例：
{"target": {"kind": "stage"}, "mode": "append"}
{"target": {"kind": "current_target"}, "mode": "modify"}  ← current_target可以用modify/rewrite
''',
    },
}


def match_error_message(error_detail: str) -> str:
    """根据错误详情匹配最合适的错误消息"""
    import re
    
    for key, config in ERROR_MESSAGES.items():
        if re.search(config['pattern'], error_detail, re.IGNORECASE):
            return config['message']
    
    # 默认通用消息
    return f'''
验证失败：{error_detail}

请检查：
1. 所有必需字段是否存在？
2. 是否有禁止的字段？
3. 字段值类型是否正确？
4. 引用关系是否完整？

请参考资源操作速查表修正输出。
'''
```

---

## Day 3-4：实现结构化错误反馈

### 目标
将验证错误转换为具体、可操作的修复提示，让DeepSeek能够精准修正。

### 任务2.1：增强orchestrator的错误处理

**文件**：`backend/app/orchestration/orchestrator.py`

**修改点1**：导入错误消息模块

```python
# 文件开头添加
from ..prompts.error_messages import match_error_message
```

**修改点2**：增强_repair_messages方法

```python
def _repair_messages(
    self,
    messages: list[dict[str, str]],
    raw_output: str,
    failure: GenerationEvent,
    resume_kind: str,
) -> list[dict[str, str]]:
    """生成修复提示词 - DeepSeek优化版"""
    error_detail = failure.data.get('detail', '')
    error_path = failure.data.get('path', '')
    phase = failure.phase
    
    # 使用中文友好的错误消息
    specific_hint = match_error_message(error_detail)
    
    # 提取失败的具体位置
    context_hint = ''
    if 'resource_ops' in error_path:
        # 提取resource_ops的索引
        import re
        match = re.search(r'resource_ops\.(\d+)', error_path)
        if match:
            idx = int(match.group(1))
            context_hint = f'\n\n错误位置：resource_ops数组的第{idx}个元素（索引{idx}）'
    
    # 构建修复提示
    repair_prompt = f'''
上一次生成未通过验证，需要修正。

**验证失败的阶段：** {phase}
**错误路径：** {error_path}
**错误详情：** {error_detail}{context_hint}

{specific_hint}

**修复要求：**
1. 仔细阅读上面的错误说明和正确示例
2. 找到失败的具体位置
3. 对照资源操作速查表修正
4. 从<{resume_kind}>标签重新开始输出
5. 保持其他正确的部分不变

**重要：** 只输出标签和JSON，不要输出任何解释或说明。
'''

    # 返回带修复提示的消息历史
    return messages + [
        {'role': 'assistant', 'content': raw_output},
        {'role': 'user', 'content': repair_prompt},
    ]
```

### 任务2.2：添加更详细的日志

**文件**：`backend/app/orchestration/orchestrator.py`

**修改_consume_frame方法中的异常处理**：

```python
except (ValidationError, ValueError, StreamProtocolError) as exc:
    path = self._error_path(exc)
    phase = self._phase_for(frame)
    detail = self._error_detail(exc)
    
    # 新增：记录完整错误上下文
    logger.warning(
        '统一生成 校验失败: phase=%s path=%s message=%s detail=%s',
        phase, path, self._safe_message(exc), detail,
        extra={
            'request_id': request.request_id,
            'scope': request.scope,
            'frame_kind': frame.kind,
            'frame_content_preview': frame.content[:200] if frame.content else '',
            'error_type': type(exc).__name__,
            'validation_errors': exc.errors() if isinstance(exc, ValidationError) else None,
        }
    )
    
    return [], GenerationEvent('validation_error', request.request_id, phase=phase, data={
        'path': path,
        'message': self._safe_message(exc),
        'detail': detail,
        'recoverable': False,
    })
```

### 任务2.3：创建错误分类统计

**新文件**：`backend/app/orchestration/error_stats.py`

```python
"""错误统计模块 - 用于追踪和分析常见错误"""

from collections import defaultdict
from datetime import datetime
from typing import Dict, List
import json


class ErrorStatsCollector:
    """收集和分析验证错误统计"""
    
    def __init__(self):
        self.errors: List[Dict] = []
        self.error_counts = defaultdict(int)
    
    def record_error(
        self,
        request_id: str,
        phase: str,
        path: str,
        detail: str,
        attempt: int,
    ):
        """记录一次错误"""
        # 提取错误类型
        error_type = self._classify_error(path, detail)
        
        self.errors.append({
            'timestamp': datetime.now().isoformat(),
            'request_id': request_id,
            'phase': phase,
            'path': path,
            'detail': detail,
            'attempt': attempt,
            'error_type': error_type,
        })
        
        self.error_counts[error_type] += 1
    
    def _classify_error(self, path: str, detail: str) -> str:
        """分类错误类型"""
        if 'add_backdrop' in detail:
            if 'select_after' in detail:
                return 'add_backdrop_missing_select_after'
            elif 'target' in detail or 'placement' in detail:
                return 'add_backdrop_forbidden_fields'
        elif 'add_costume' in detail:
            if 'select_after' in detail:
                return 'add_costume_missing_select_after'
            elif 'target' in detail:
                return 'add_costume_invalid_target'
        elif 'created_sprite' in detail:
            return 'created_sprite_invalid_reference'
        elif 'program_targets' in path and 'mode' in detail:
            return 'program_target_mode_invalid'
        elif 'resource_ops' in path:
            return 'resource_ops_other'
        else:
            return 'unknown'
    
    def get_summary(self) -> Dict:
        """获取统计摘要"""
        total = len(self.errors)
        if total == 0:
            return {'total': 0, 'distribution': {}}
        
        distribution = {
            error_type: {
                'count': count,
                'percentage': round(count / total * 100, 1)
            }
            for error_type, count in self.error_counts.items()
        }
        
        return {
            'total': total,
            'distribution': dict(sorted(
                distribution.items(),
                key=lambda x: x[1]['count'],
                reverse=True
            )),
            'most_common': max(self.error_counts.items(), key=lambda x: x[1])[0] if self.error_counts else None,
        }
    
    def save_to_file(self, filepath: str):
        """保存错误记录到文件"""
        with open(filepath, 'w', encoding='utf-8') as f:
            json.dump({
                'summary': self.get_summary(),
                'errors': self.errors,
            }, f, ensure_ascii=False, indent=2)


# 全局实例（用于开发和测试环境）
_global_collector = ErrorStatsCollector()


def record_validation_error(request_id: str, phase: str, path: str, detail: str, attempt: int):
    """记录验证错误（便捷函数）"""
    _global_collector.record_error(request_id, phase, path, detail, attempt)


def get_error_stats() -> Dict:
    """获取错误统计"""
    return _global_collector.get_summary()


def save_error_stats(filepath: str = 'error_stats.json'):
    """保存错误统计"""
    _global_collector.save_to_file(filepath)
```

**在orchestrator中集成**：

```python
# 在orchestrator.py中导入
from .error_stats import record_validation_error

# 在_consume_frame的异常处理中调用
except (ValidationError, ValueError, StreamProtocolError) as exc:
    path = self._error_path(exc)
    phase = self._phase_for(frame)
    detail = self._error_detail(exc)
    
    # 记录到统计
    record_validation_error(
        request.request_id,
        phase,
        path,
        detail,
        attempt=0,  # 首次尝试
    )
    
    logger.warning(...)
    # ... 其余代码
```

---

## Day 5：提升修复轮次和配置优化

### 目标
增加修复机会，给DeepSeek更多次数纠正错误。

### 任务3.1：调整配置参数

**文件**：`backend/app/config.py`

```python
from pydantic_settings import BaseSettings

class Settings(BaseSettings):
    # AI Blocks配置
    ai_blocks_max_concurrency: int = 2
    ai_blocks_max_repair_rounds: int = 3  # 从1提升到3
    ai_blocks_provider_timeout_seconds: float = 120.0  # 适当延长超时
    
    # 新增：DeepSeek专用配置
    ai_blocks_enable_error_stats: bool = True  # 启用错误统计
    ai_blocks_repair_temperature: float = 0.3  # 修复时使用更低的temperature
    
    # ... 其他配置
```

### 任务3.2：在修复时调整温度参数

**文件**：`backend/app/orchestration/orchestrator.py`

修复轮次应该使用更低的temperature以提高准确性：

```python
async def generate_unified(
    self,
    request: UnifiedGenerationRequest,
    provider: AgnesTextProvider,
) -> AsyncIterator[GenerationEvent]:
    # ... 前面的代码
    
    for attempt in range(rounds):
        if attempt > 0:
            # 修复轮次的配置
            repair_config = {
                'thinking_enabled': request.thinking.enabled,
                'thinking_strength': 'high',  # 修复时使用更强的thinking
                'json_output': False,
                'temperature': 0.3,  # 降低temperature提高准确性
            }
        else:
            # 首次生成的配置
            repair_config = {
                'thinking_enabled': request.thinking.enabled,
                'thinking_strength': request.thinking.strength,
                'json_output': False,
            }
        
        try:
            stream = provider.stream_with_reasoning(
                attempt_messages,
                **repair_config
            )
            # ... 其余代码
```

**注意**：需要确认provider支持temperature参数，如果不支持，可以跳过此优化。

### 任务3.3：添加修复轮次的渐进式提示

随着修复轮次增加，提示应该越来越具体：

```python
def _repair_messages(
    self,
    messages: list[dict[str, str]],
    raw_output: str,
    failure: GenerationEvent,
    resume_kind: str,
    attempt: int = 1,  # 新增参数
) -> list[dict[str, str]]:
    """生成修复提示词 - 根据尝试次数调整强度"""
    error_detail = failure.data.get('detail', '')
    error_path = failure.data.get('path', '')
    
    specific_hint = match_error_message(error_detail)
    
    # 根据尝试次数调整提示强度
    if attempt == 1:
        urgency = '请仔细修正'
    elif attempt == 2:
        urgency = '⚠️ 这是第2次修复，请格外仔细检查每个字段'
    else:
        urgency = '🚨 最后一次修复机会！请逐字对照速查表检查'
    
    repair_prompt = f'''
{urgency}

**修复轮次：** {attempt}/{self.max_repair_rounds}
**验证失败的阶段：** {failure.phase}
**错误路径：** {error_path}
**错误详情：** {error_detail}

{specific_hint}

**逐步修复流程：**
1. 定位错误：找到resource_ops中失败的操作
2. 对照速查表：查看该操作的必需字段和禁止字段
3. 逐字段检查：
   - 必需字段是否全部存在？
   - 是否有多余的禁止字段？
   - 字段值类型是否正确？
4. 修正后从<{resume_kind}>重新输出完整内容

**检查清单（必须全部打勾）：**
□ 我已找到具体的错误位置
□ 我已对照速查表确认字段组合
□ 我已检查是否有禁止字段
□ 我已确认引用关系完整
□ 我准备输出不含任何解释的纯标签+JSON
'''

    return messages + [
        {'role': 'assistant', 'content': raw_output},
        {'role': 'user', 'content': repair_prompt},
    ]
```

---

## Day 6-7：测试和优化

### 目标
全面测试修改，确保Phase 1达到70%+成功率。

### 任务4.1：编写测试用例

**新文件**：`backend/tests/test_resource_ops_validation.py`

```python
"""资源操作验证测试 - 覆盖6种操作类型"""

import pytest
from pydantic import ValidationError

from app.schemas.ai_blocks import ResourceOp, ProjectManifest


class TestAddBackdrop:
    """测试add_backdrop操作"""
    
    def test_valid_add_backdrop(self):
        """正确的add_backdrop应该通过验证"""
        op = ResourceOp(
            op='add_backdrop',
            op_id='bg1',
            name='森林',
            source={
                'kind': 'scratch_library',
                'asset_type': 'backdrop',
                'search_terms': ['森林', '自然']
            },
            select_after=True,
            tags=['自然']
        )
        assert op.op == 'add_backdrop'
    
    def test_add_backdrop_missing_select_after(self):
        """缺少select_after应该失败"""
        with pytest.raises(ValidationError) as exc_info:
            ResourceOp(
                op='add_backdrop',
                op_id='bg1',
                name='森林',
                source={
                    'kind': 'scratch_library',
                    'asset_type': 'backdrop',
                    'search_terms': ['森林']
                },
                tags=[]
                # 缺少select_after
            )
        
        error_detail = str(exc_info.value)
        assert 'select_after' in error_detail
    
    def test_add_backdrop_with_target(self):
        """包含target应该失败"""
        with pytest.raises(ValidationError) as exc_info:
            ResourceOp(
                op='add_backdrop',
                op_id='bg1',
                name='森林',
                target={'kind': 'stage'},  # 不应有target
                source={
                    'kind': 'scratch_library',
                    'asset_type': 'backdrop',
                    'search_terms': ['森林']
                },
                select_after=True,
                tags=[]
            )
        
        error_detail = str(exc_info.value)
        assert 'target' in error_detail or 'no target' in error_detail
    
    def test_add_backdrop_with_position(self):
        """包含位置字段应该失败"""
        with pytest.raises(ValidationError) as exc_info:
            ResourceOp(
                op='add_backdrop',
                op_id='bg1',
                name='森林',
                source={
                    'kind': 'scratch_library',
                    'asset_type': 'backdrop',
                    'search_terms': ['森林']
                },
                x=0,  # 不应有位置
                y=0,
                size=100,
                select_after=True,
                tags=[]
            )
        
        error_detail = str(exc_info.value)
        assert 'placement' in error_detail or 'position' in error_detail


class TestAddCostume:
    """测试add_costume操作"""
    
    def test_valid_add_costume(self):
        """正确的add_costume应该通过验证"""
        op = ResourceOp(
            op='add_costume',
            op_id='c1',
            name='跑步',
            target={'kind': 'existing_sprite', 'name': '猫咪'},
            source={
                'kind': 'scratch_library',
                'asset_type': 'costume',
                'search_terms': ['跑']
            },
            select_after=False,
            tags=[]
        )
        assert op.op == 'add_costume'
    
    def test_add_costume_to_stage(self):
        """给stage添加costume应该失败"""
        with pytest.raises(ValidationError) as exc_info:
            ResourceOp(
                op='add_costume',
                op_id='c1',
                name='背景',
                target={'kind': 'stage'},  # stage不能有costume
                source={
                    'kind': 'scratch_library',
                    'asset_type': 'costume',
                    'search_terms': ['背景']
                },
                select_after=True,
                tags=[]
            )
        
        error_detail = str(exc_info.value)
        assert 'stage' in error_detail.lower()
    
    def test_add_costume_missing_select_after(self):
        """缺少select_after应该失败"""
        with pytest.raises(ValidationError) as exc_info:
            ResourceOp(
                op='add_costume',
                op_id='c1',
                name='跑步',
                target={'kind': 'existing_sprite', 'name': '猫咪'},
                source={
                    'kind': 'scratch_library',
                    'asset_type': 'costume',
                    'search_terms': ['跑']
                },
                tags=[]
                # 缺少select_after
            )
        
        error_detail = str(exc_info.value)
        assert 'select_after' in error_detail


class TestAddBuiltinSprite:
    """测试add_builtin_sprite操作"""
    
    def test_valid_add_builtin_sprite(self):
        """正确的add_builtin_sprite应该通过验证"""
        op = ResourceOp(
            op='add_builtin_sprite',
            op_id='sprite1',
            name='猫咪',
            source={
                'kind': 'scratch_library',
                'asset_type': 'sprite',
                'search_terms': ['猫']
            },
            x=0,
            y=0,
            size=100,
            tags=[]
        )
        assert op.op == 'add_builtin_sprite'
    
    def test_add_builtin_sprite_missing_position(self):
        """缺少位置字段应该失败"""
        with pytest.raises(ValidationError) as exc_info:
            ResourceOp(
                op='add_builtin_sprite',
                op_id='sprite1',
                name='猫咪',
                source={
                    'kind': 'scratch_library',
                    'asset_type': 'sprite',
                    'search_terms': ['猫']
                },
                tags=[]
                # 缺少x, y, size
            )
        
        error_detail = str(exc_info.value)
        assert 'x' in error_detail or 'y' in error_detail or 'size' in error_detail


class TestCreatedSpriteReference:
    """测试created_sprite引用"""
    
    def test_valid_created_sprite_reference(self):
        """正确的引用应该通过验证"""
        manifest = ProjectManifest(
            schema_version=1,
            scope='project',
            summary='测试',
            resource_ops=[
                ResourceOp(
                    op='add_sprite',
                    op_id='sprite_new',
                    name='新角色',
                    source={
                        'kind': 'image_generation',
                        'reason': '测试',
                        'prompt': '测试角色',
                        'style': 'cartoon',
                        'width': 512,
                        'height': 512,
                        'sheet_layout': '1x1'
                    },
                    x=0,
                    y=0,
                    size=100,
                    tags=[],
                    costumes=[]
                ),
                ResourceOp(
                    op='add_costume',
                    op_id='c1',
                    name='造型1',
                    target={'kind': 'created_sprite', 'op_id': 'sprite_new'},  # 引用前面的
                    source={
                        'kind': 'scratch_library',
                        'asset_type': 'costume',
                        'search_terms': ['造型']
                    },
                    select_after=True,
                    tags=[]
                )
            ],
            program_targets=[],
            shared_state={'variables': [], 'lists': [], 'broadcasts': []}
        )
        assert len(manifest.resource_ops) == 2
    
    def test_invalid_created_sprite_reference(self):
        """无效的引用应该失败"""
        with pytest.raises(ValidationError) as exc_info:
            ProjectManifest(
                schema_version=1,
                scope='project',
                summary='测试',
                resource_ops=[
                    ResourceOp(
                        op='add_costume',
                        op_id='c1',
                        name='造型1',
                        target={'kind': 'created_sprite', 'op_id': 'not_exist'},  # 引用不存在的
                        source={
                            'kind': 'scratch_library',
                            'asset_type': 'costume',
                            'search_terms': ['造型']
                        },
                        select_after=True,
                        tags=[]
                    )
                ],
                program_targets=[],
                shared_state={'variables': [], 'lists': [], 'broadcasts': []}
            )
        
        error_detail = str(exc_info.value)
        assert 'reference' in error_detail or 'not_exist' in error_detail
```

### 任务4.2：端到端测试场景

**新文件**：`backend/tests/test_project_generation_e2e.py`

```python
"""端到端测试 - 模拟真实用户场景"""

import pytest
from app.orchestration.orchestrator import GenerationOrchestrator
from app.prompt_builder import PromptBuilder
from app.schemas.ai_blocks import UnifiedGenerationRequest, TargetContext, ProjectContext, ProjectTargetContext


@pytest.fixture
def orchestrator():
    return GenerationOrchestrator(PromptBuilder(), max_repair_rounds=3)


@pytest.fixture
def project_request():
    """典型的项目模式请求"""
    return UnifiedGenerationRequest(
        model_tier='normal',
        session_id='test_session',
        request_id='test_request',
        scope='project',
        asset_policy='builtin_first',
        messages=[
            {
                'role': 'user',
                'content': '创建一个接苹果游戏：玩家控制篮子左右移动，苹果从上方随机落下，接到得分'
            }
        ],
        target=TargetContext(
            name='Stage',
            is_stage=True,
            has_scripts=False,
            mode='append',
            costumes=['backdrop1'],
            sounds=[],
            sprite_names=['Sprite1'],
            current_program='{}'
        ),
        project_context=ProjectContext(
            project_id='test_project',
            version=1,
            targets=[
                ProjectTargetContext(
                    target_id='stage_id',
                    revision=0,
                    name='Stage',
                    is_stage=True,
                    has_scripts=False,
                    mode='append',
                    costumes=['backdrop1'],
                    sounds=[],
                    sprite_names=['Sprite1'],
                    current_program='{}'
                ),
                ProjectTargetContext(
                    target_id='sprite1_id',
                    revision=0,
                    name='Sprite1',
                    is_stage=False,
                    has_scripts=False,
                    mode='append',
                    costumes=['costume1'],
                    sounds=[],
                    sprite_names=['Sprite1'],
                    current_program='{}'
                )
            ],
            shared_state={}
        ),
        thinking={'enabled': True, 'strength': 'medium'}
    )


# 注意：这个测试需要真实的LLM调用，所以标记为integration test
@pytest.mark.integration
@pytest.mark.asyncio
async def test_project_generation_with_resources(orchestrator, project_request, mock_provider):
    """测试带资源操作的项目生成"""
    events = []
    
    async for event in orchestrator.generate_unified(project_request, mock_provider):
        events.append(event)
    
    # 验证关键事件
    event_types = [e.type for e in events]
    assert 'phase_start' in event_types  # requirements阶段开始
    assert 'phase_complete' in event_types  # 至少一个阶段完成
    
    # 检查是否成功（最后一个事件应该是complete或error）
    final_event = events[-1]
    assert final_event.type in ('complete', 'error')
    
    if final_event.type == 'complete':
        # 成功情况：检查是否生成了manifest
        manifest_events = [e for e in events if 'manifest' in e.data]
        assert len(manifest_events) > 0, "项目模式应该生成manifest"
```

### 任务4.3：性能基准测试

**新文件**：`backend/tests/benchmark_repair_rounds.py`

```python
"""性能基准测试 - 评估修复轮次对成功率和延迟的影响"""

import asyncio
import time
from typing import List, Dict
import json


class RepairBenchmark:
    """修复轮次基准测试"""
    
    def __init__(self):
        self.results: List[Dict] = []
    
    async def run_test_case(
        self,
        orchestrator,
        provider,
        request,
        test_name: str
    ) -> Dict:
        """运行单个测试用例"""
        start_time = time.time()
        repair_count = 0
        success = False
        error_message = None
        
        events = []
        async for event in orchestrator.generate_unified(request, provider):
            events.append(event)
            if event.type == 'repair':
                repair_count = event.data.get('attempt', 0)
            elif event.type == 'complete':
                success = True
            elif event.type == 'error':
                error_message = event.data.get('message')
        
        end_time = time.time()
        duration = end_time - start_time
        
        return {
            'test_name': test_name,
            'success': success,
            'repair_count': repair_count,
            'duration_seconds': round(duration, 2),
            'error_message': error_message,
            'event_count': len(events)
        }
    
    async def run_benchmark(
        self,
        test_cases: List[tuple],  # (orchestrator, provider, request, name)
        iterations: int = 5
    ):
        """运行完整基准测试"""
        print(f"开始基准测试：{len(test_cases)} 个用例，每个 {iterations} 次迭代")
        
        for i, (orchestrator, provider, request, name) in enumerate(test_cases):
            print(f"\n测试用例 {i+1}/{len(test_cases)}: {name}")
            
            case_results = []
            for iteration in range(iterations):
                print(f"  迭代 {iteration+1}/{iterations}...", end='')
                result = await self.run_test_case(
                    orchestrator, provider, request, f"{name}_iter{iteration+1}"
                )
                case_results.append(result)
                print(f" {'✓' if result['success'] else '✗'} ({result['duration_seconds']}s)")
            
            # 统计此用例的结果
            success_count = sum(1 for r in case_results if r['success'])
            success_rate = success_count / iterations * 100
            avg_duration = sum(r['duration_seconds'] for r in case_results) / iterations
            avg_repairs = sum(r['repair_count'] for r in case_results) / iterations
            
            summary = {
                'test_case': name,
                'iterations': iterations,
                'success_rate': round(success_rate, 1),
                'avg_duration': round(avg_duration, 2),
                'avg_repair_count': round(avg_repairs, 2),
                'individual_results': case_results
            }
            
            self.results.append(summary)
            
            print(f"\n  成功率: {success_rate}%")
            print(f"  平均耗时: {avg_duration}s")
            print(f"  平均修复: {avg_repairs}次")
    
    def save_results(self, filepath: str = 'benchmark_results.json'):
        """保存基准测试结果"""
        with open(filepath, 'w', encoding='utf-8') as f:
            json.dump({
                'timestamp': time.strftime('%Y-%m-%d %H:%M:%S'),
                'results': self.results,
                'overall': self._calculate_overall()
            }, f, ensure_ascii=False, indent=2)
        
        print(f"\n结果已保存到: {filepath}")
    
    def _calculate_overall(self) -> Dict:
        """计算总体统计"""
        if not self.results:
            return {}
        
        total_tests = sum(r['iterations'] for r in self.results)
        weighted_success_rate = sum(
            r['success_rate'] * r['iterations'] for r in self.results
        ) / total_tests
        
        return {
            'total_tests': total_tests,
            'overall_success_rate': round(weighted_success_rate, 1),
            'avg_duration': round(
                sum(r['avg_duration'] for r in self.results) / len(self.results), 2
            ),
            'avg_repair_count': round(
                sum(r['avg_repair_count'] for r in self.results) / len(self.results), 2
            )
        }


# 使用示例
async def main():
    benchmark = RepairBenchmark()
    
    # 准备测试用例
    test_cases = [
        # (orchestrator, provider, request, "用例名称")
        # 需要实际填充
    ]
    
    await benchmark.run_benchmark(test_cases, iterations=5)
    benchmark.save_results('phase1_benchmark.json')


if __name__ == '__main__':
    asyncio.run(main())
```

### 任务4.4：创建测试运行脚本

**新文件**：`scripts/test_phase1.sh`

```bash
#!/bin/bash
# Phase 1测试运行脚本

echo "========================================"
echo "Phase 1 实施测试"
echo "========================================"

# 设置环境变量
export PYTHONPATH="${PYTHONPATH}:$(pwd)/backend"

echo -e "\n1. 单元测试..."
python -m pytest backend/tests/test_resource_ops_validation.py -v

echo -e "\n2. 集成测试（需要配置真实LLM）..."
python -m pytest backend/tests/test_project_generation_e2e.py -v -m integration

echo -e "\n3. 错误统计分析..."
python -c "
from app.orchestration.error_stats import get_error_stats, save_error_stats
stats = get_error_stats()
print('\n错误类型分布:')
for error_type, data in stats.get('distribution', {}).items():
    print(f'  {error_type}: {data[\"count\"]}次 ({data[\"percentage\"]}%)')
save_error_stats('phase1_error_stats.json')
print('\n详细统计已保存到: phase1_error_stats.json')
"

echo -e "\n4. 性能基准测试..."
# python backend/tests/benchmark_repair_rounds.py

echo -e "\n========================================"
echo "测试完成！"
echo "========================================"
```

---

## 验收标准

### 成功标准
- [ ] **成功率提升**：项目模式成功率从40%提升到70%+
- [ ] **修复效果**：平均修复次数<1.0
- [ ] **延迟可接受**：端到端延迟增加<30%（相比Phase 0）
- [ ] **错误分类清晰**：能够识别和统计至少5种主要错误类型
- [ ] **测试覆盖**：所有6种资源操作都有对应的单元测试

### 具体验收测试

**测试1：add_backdrop常见错误**
```python
# 应该能够识别并修复这些错误
test_cases = [
    '缺少select_after',
    '包含target字段',
    '包含x/y/size字段',
    'source.asset_type不是backdrop'
]
# 预期：至少3/4能在2轮内修复成功
```

**测试2：created_sprite引用**
```python
# 测试引用关系验证
test_cases = [
    '引用顺序正确',
    '引用不存在的op_id',
    'op_id拼写错误'
]
# 预期：正确的通过，错误的能给出明确提示
```

**测试3：实际项目场景**
```python
# 5个典型项目场景
scenarios = [
    '接苹果游戏',
    '打飞机游戏',
    '故事动画（多角色）',
    '绘图项目',
    '问答互动'
]
# 预期：至少4/5成功生成
```

---

## DeepSeek特定优化建议

### 1. Token效率优化

DeepSeek相比GPT-4在中文理解上更高效，充分利用这一点：

```python
# 不要这样（英文+中文混杂）
prompt = '''
The add_backdrop operation requires select_after field.
add_backdrop操作需要select_after字段。
'''

# 应该这样（纯中文，更简洁）
prompt = '''
add_backdrop操作必须包含select_after字段（true或false）。
'''
```

### 2. 结构化优先

DeepSeek对表格和列表的理解特别好：

```python
# 好的做法
FIELD_RULES = '''
| 操作 | 必需字段 | 禁止字段 |
|------|---------|---------|
| add_backdrop | op, op_id, name, source, select_after, tags | target, x, y, size |
'''

# 避免的做法
FIELD_RULES = '''
add_backdrop需要op、op_id、name、source、select_after和tags字段，
但不能包含target、x、y或size字段...
'''
```

### 3. 示例驱动

DeepSeek的few-shot学习效果很好，多用正确示例：

```python
REPAIR_HINT = '''
错误示例：
{错误的JSON}

正确示例：
{正确的JSON}

请参考正确示例修正你的输出。
'''
```

---

## 监控和度量

### 每日检查清单

**Day 1-2完成后：**
- [ ] 速查表是否清晰易懂？
- [ ] 错误消息是否中文友好？
- [ ] 提示词token数是否合理？

**Day 3-4完成后：**
- [ ] 错误统计是否正常收集？
- [ ] 修复提示是否针对性强？
- [ ] 日志是否足够详细？

**Day 5完成后：**
- [ ] 修复轮次是否生效？
- [ ] 配置参数是否合理？
- [ ] 性能是否在可接受范围？

**Day 6-7完成后：**
- [ ] 单元测试是否全部通过？
- [ ] 成功率是否达到70%+？
- [ ] 准备好进入Phase 2？

### 关键指标监控

**实时监控：**
```python
# 在orchestrator中添加metrics
metrics = {
    'total_requests': 0,
    'successful_requests': 0,
    'failed_requests': 0,
    'repair_attempts': {0: 0, 1: 0, 2: 0, 3: 0},
    'avg_duration': 0.0,
}

# 每次请求结束后更新
def update_metrics(success: bool, repair_count: int, duration: float):
    metrics['total_requests'] += 1
    if success:
        metrics['successful_requests'] += 1
    else:
        metrics['failed_requests'] += 1
    metrics['repair_attempts'][repair_count] += 1
    # 更新平均耗时...
```

**周报告：**
```python
# 生成周报告
weekly_report = {
    'week': 'Phase 1 实施周',
    'success_rate': metrics['successful_requests'] / metrics['total_requests'],
    'repair_distribution': metrics['repair_attempts'],
    'most_common_errors': get_error_stats()['most_common'],
    'avg_duration': metrics['avg_duration'],
    'baseline_comparison': {
        'before': 0.40,
        'after': current_success_rate,
        'improvement': current_success_rate - 0.40
    }
}
```

---

## 风险缓解

### 风险1：修复轮次增加导致超时

**缓解措施：**
- 设置合理的timeout（120秒）
- 每轮修复后检查剩余时间
- 超过80秒后不再尝试修复，直接返回错误

```python
MAX_TOTAL_DURATION = 120.0  # 秒

start_time = time.time()
for attempt in range(rounds):
    elapsed = time.time() - start_time
    if elapsed > 0.8 * MAX_TOTAL_DURATION:
        logger.warning('接近超时限制，停止修复')
        break
    # ... 正常修复流程
```

### 风险2：修复提示过长导致token超限

**缓解措施：**
- 控制每个错误消息<500 tokens
- 修复提示中只包含最相关的部分
- 移除冗余的示例

### 风险3：错误统计影响性能

**缓解措施：**
- 错误统计异步执行
- 只在开发/测试环境启用详细统计
- 生产环境使用采样统计

```python
# config.py
class Settings(BaseSettings):
    ai_blocks_enable_error_stats: bool = Field(
        default=True if ENV == 'development' else False
    )
```

---

## 下一步：Phase 2准备

Phase 1完成后（Day 7），评估结果：

**如果成功率>75%：**
- ✅ Phase 1超预期成功
- 考虑Phase 1.5（智能修复优化）
- 或直接准备Phase 2（分阶段生成）

**如果成功率65-75%：**
- ✅ Phase 1基本达标
- 继续优化1-2天
- 按计划进入Phase 2

**如果成功率<65%：**
- ⚠️ 需要深入分析
- 查看错误统计找根本原因
- 可能需要调整提示词策略或模型参数

---

## 附录

### A. DeepSeek API配置示例

```python
# backend/app/providers/deepseek_provider.py

class DeepSeekProvider(AgnesTextProvider):
    """DeepSeek专用provider"""
    
    def __init__(self, settings):
        super().__init__(settings)
        self.api_base = "https://api.deepseek.com/v1"
        self.model = "deepseek-chat"  # 或 deepseek-coder
    
    async def stream_with_reasoning(
        self,
        messages: list[dict],
        thinking_enabled: bool = True,
        thinking_strength: str = 'medium',
        json_output: bool = False,
        temperature: float = 0.7,
    ):
        """DeepSeek特定的流式调用"""
        # DeepSeek的thinking参数映射
        reasoning_effort = {
            'low': 'low',
            'medium': 'medium',
            'high': 'high',
            'xhigh': 'extended',
            'max': 'extended'
        }.get(thinking_strength, 'medium')
        
        # 构建请求
        payload = {
            'model': self.model,
            'messages': messages,
            'temperature': temperature,
            'stream': True,
        }
        
        # DeepSeek的reasoning参数
        if thinking_enabled:
            payload['reasoning_effort'] = reasoning_effort
        
        # ... 发送请求并处理流式响应
```

### B. 提示词版本管理

```python
# backend/app/prompts/__init__.py

PROMPT_VERSION = '1.0.0-phase1'

CHANGELOG = '''
v1.0.0-phase1 (2026-08-30):
- 重构资源操作描述为速查表格式
- 添加6种操作的详细示例和错误案例
- 优化中文表述，提升DeepSeek理解准确性
- 增加结构化检查清单
'''
```

### C. 快速调试工具

```bash
# scripts/debug_validation_error.py
# 快速测试单个资源操作是否通过验证

import json
import sys
from app.schemas.ai_blocks import ResourceOp

if len(sys.argv) < 2:
    print("用法: python debug_validation_error.py '<json>'")
    sys.exit(1)

json_str = sys.argv[1]
try:
    data = json.loads(json_str)
    op = ResourceOp(**data)
    print("✓ 验证通过")
    print(json.dumps(op.model_dump(), ensure_ascii=False, indent=2))
except Exception as e:
    print("✗ 验证失败")
    print(str(e))
```

使用方式：
```bash
python scripts/debug_validation_error.py '{"op":"add_backdrop","op_id":"bg1","name":"森林","source":{...},"select_after":true,"tags":[]}'
```

---

**文档版本：** v1.0  
**最后更新：** 2026-08-30  
**适用模型：** DeepSeek V3 / DeepSeek Chat  
**预期完成时间：** 7个工作日  
**预期成功率提升：** 40% → 70%+
