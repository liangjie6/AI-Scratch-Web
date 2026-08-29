# AI 写积木架构重构设计方案

## 文档信息

- **文档版本**: v1.0
- **创建日期**: 2026-08-29
- **文档类型**: 架构设计
- **状态**: 待评审

## 一、现状问题分析

### 1.1 核心问题

根据深度审计报告和代码分析，当前架构存在以下致命问题：

#### 问题 1: JSON 输出不稳定，token 消耗巨大
- **现象**: AI 对 JSON 结构输出不稳定，解析失败时重新生成
- **影响**: 单次生成可能触发 2-4 次完整重试，每次重试都携带完整上下文 + 所有历史失败记录
- **数据**: 一次失败的生成可能消耗 3-5 倍正常 token 量

#### 问题 2: 生成时间过长
- **现象**: 用户等待时间长达 10-30 秒甚至更久
- **原因**:
  - 串行生成：requirements → plan → program，无法并行
  - 渐进式注入：每个积木块间隔 110ms，大型程序注入耗时 5-10 秒
  - 多次重试：每次重试都是完整的 LLM 调用

#### 问题 3: 缓存命中率极低
- **现象**: 提示词缓存几乎无效，每次请求都是冷启动
- **原因**:
  - 每个请求的 system prompt 都包含动态上下文
  - requirements、plan、program 三个阶段使用完全不同的 prompt 结构
  - 修复时 prompt 持续追加失败历史，导致 prompt 不断变化
  - 目标上下文（target context）直接嵌入 prompt，无法复用

#### 问题 4: 流程碎片化
- **现象**: 请求数量过多，每次都要重新注入不同的上下文
- **统计**: 一次完整生成涉及 3-5 个独立的 LLM 请求
  1. `/requirements` - 需求澄清
  2. `/generate` (plan) - 生成计划
  3. `/generate` (program) - 生成程序
  4. 可能的修复请求 × N

### 1.2 架构层面的根本原因

```
当前架构的根本缺陷：
┌─────────────────────────────────────────────────────┐
│ 问题：每个阶段都是独立的 LLM 调用                      │
│ - 不同的 system prompt                               │
│ - 不同的上下文注入方式                                │
│ - 无法利用 prompt cache                              │
└─────────────────────────────────────────────────────┘

当前流程：
Request 1: requirements
  System: "你是需求助手..." + 目标上下文
  User: 用户输入
  → 输出 JSON → 解析失败 → 修复循环

Request 2: plan  
  System: "你是规划器..." + 需求 + 目标上下文
  User: 新的完整上下文
  → 输出 JSON → 解析失败 → 修复循环

Request 3: program
  System: "你是生成器..." + 完整规范 + 计划 + 需求 + 目标上下文
  User: 新的完整上下文
  → 输出 JSON → 解析失败 → 修复循环

每次请求的 prompt 完全不同 → 缓存失效
```

## 二、设计目标

### 2.1 核心目标

1. **提升缓存命中率**: 从 <5% 提升到 >80%
2. **减少生成时间**: 从 15-30 秒降低到 3-8 秒
3. **降低 token 消耗**: 减少 50-70% 的 token 使用
4. **提高稳定性**: JSON 解析成功率从 60% 提升到 95%+

### 2.2 技术指标

- **首屏响应**: <2 秒（流式输出开始）
- **完整生成**: <8 秒（中等复杂度程序）
- **缓存命中**: >80%（同一项目内）
- **解析成功率**: >95%（首次尝试）
- **Token 效率**: 平均每次生成 <10K tokens

## 三、核心设计理念

### 3.1 统一提示词前缀（Unified Prompt Prefix）

**理念**: 将所有静态、可缓存的内容提取为统一的前缀，确保跨请求复用。

```
设计原则：
1. 静态内容前置，动态内容后置
2. 所有生成阶段共享同一个知识库前缀
3. 用户特定的动态上下文延后注入
```

**新架构**:

```
┌──────────────────────────────────────────────────────┐
│ Level 1: 全局知识库（100% 可缓存，所有请求共享）        │
│ - Scratch 积木规范完整版                              │
│ - Block Catalog 完整定义                             │
│ - ProgramIR Schema 完整规范                          │
│ - 代码生成规范和最佳实践                              │
│ - 所有 native opcodes 列表                           │
│ ├─ 约 8K-12K tokens                                  │
│ └─ 缓存时长: 5 分钟                                   │
└──────────────────────────────────────────────────────┘
         ↓ (固定分隔符)
┌──────────────────────────────────────────────────────┐
│ Level 2: 项目级上下文（项目内可缓存）                  │
│ - 当前项目的所有角色列表                              │
│ - 所有素材库（costumes, sounds）                     │
│ - 共享变量和广播定义                                  │
│ - 项目特定的约束规则                                  │
│ ├─ 约 1K-3K tokens                                   │
│ └─ 缓存时长: 5 分钟                                   │
└──────────────────────────────────────────────────────┘
         ↓ (固定分隔符)
┌──────────────────────────────────────────────────────┐
│ Level 3: 目标级上下文（目标内可缓存）                  │
│ - 当前目标（sprite/stage）的基本信息                  │
│ - 现有脚本概览（只包含结构，不含实现细节）              │
│ - 目标的造型和声音列表                                │
│ ├─ 约 0.5K-2K tokens                                 │
│ └─ 缓存时长: 5 分钟                                   │
└──────────────────────────────────────────────────────┘
         ↓ (固定分隔符)
┌──────────────────────────────────────────────────────┐
│ Level 4: 请求特定上下文（不可缓存）                    │
│ - 用户输入和对话历史                                  │
│ - 当前生成模式（append/rewrite/modify）               │
│ - 特定的生成任务要求                                  │
│ └─ 约 0.2K-2K tokens                                 │
└──────────────────────────────────────────────────────┘
```

**关键优势**:
- Level 1 对所有用户的所有请求都相同 → 100% 命中
- Level 2 对同一项目的所有请求都相同 → 90% 命中
- Level 3 对同一目标的所有请求都相同 → 80% 命中
- 只有 Level 4 每次不同，但它只占总 tokens 的 10-20%

### 3.2 流式结构化输出（Streaming Structured Output）

**理念**: 不再一次性生成完整 JSON，改为流式生成结构化片段。

#### 方案 A: 渐进式 JSON 流（推荐）

```typescript
// 新的生成协议
interface StreamingProgramOutput {
  phase: 'metadata' | 'variables' | 'scripts';
  data: MetadataChunk | VariableChunk | ScriptChunk;
}

// 生成顺序
1. metadata: {summary, remove_script_ids} → 立即验证
2. variables: {variables[], lists[], broadcasts[]} → 立即创建
3. script_1: {...} → 立即编译和注入
4. script_2: {...} → 立即编译和注入
...
```

**优势**:
1. **即时反馈**: 用户看到第一个脚本生成（~2 秒）就有视觉反馈
2. **提前失败**: metadata 或 variables 错误立即发现，不浪费后续生成
3. **部分成功**: 即使最后一个脚本失败，前面的脚本已经注入成功
4. **降低内存**: 不需要一次性存储完整 JSON

#### 方案 B: 使用结构化输出 API

如果 AI provider 支持（如 OpenAI 的 Structured Outputs），直接使用：

```python
# 使用 JSON Schema 约束输出
response = provider.generate_structured(
    messages=messages,
    response_format={
        "type": "json_schema",
        "json_schema": PROGRAM_IR_SCHEMA
    }
)
# 保证输出符合 schema，无需手动解析和验证
```

**优势**: 
- 几乎 100% 的格式正确率
- 无需修复循环
- 生成速度更快

### 3.3 一次对话，多阶段生成（Single Conversation, Multi-Phase）

**理念**: 用一个长对话完成所有阶段，而不是分割成多个独立请求。

**新流程**:

```
┌─────────────────────────────────────────────────────┐
│ 单次 LLM 会话                                        │
├─────────────────────────────────────────────────────┤
│ System: [统一的知识库前缀]                          │
│                                                     │
│ User: [用户需求]                                    │
│                                                     │
│ Assistant: 让我分阶段完成：                          │
│   阶段 1 - 需求理解                                  │
│   <requirements>                                    │
│   {...JSON...}                                      │
│   </requirements>                                   │
│                                                     │
│ Tool: validate_requirements(passed=true)            │
│                                                     │
│ Assistant:                                          │
│   阶段 2 - 实现规划                                  │
│   <plan>                                            │
│   {...JSON...}                                      │
│   </plan>                                           │
│                                                     │
│ Tool: validate_plan(passed=true)                    │
│                                                     │
│ Assistant:                                          │
│   阶段 3 - 程序生成                                  │
│   <program>                                         │
│   {...流式脚本...}                                   │
│   </program>                                        │
│                                                     │
│ Tool: validate_and_inject(success=true)             │
└─────────────────────────────────────────────────────┘
```

**优势**:
1. **上下文连贯**: LLM 记住前面阶段的决策
2. **缓存有效**: 整个对话的前缀（knowledge base）完全缓存
3. **减少请求**: 从 3-5 次请求降低到 1 次
4. **自然修复**: 如果某阶段失败，直接在对话中追加错误，无需重建整个上下文

### 3.4 智能验证分层

**理念**: 不同层次的验证在不同时机进行，避免过早的重型验证。

```
验证层次：
├─ L1: 格式验证（实时，生成中）
│   └─ 基础 JSON 格式、必填字段
├─ L2: 结构验证（流式，每个片段）
│   └─ Opcode 存在性、字段类型正确性
├─ L3: 语义验证（组装后）
│   └─ 变量引用完整性、契约一致性
└─ L4: 运行时验证（注入时）
    └─ Scratch VM 兼容性、积木连接正确性
```

**快速失败策略**:
- L1 失败: 立即中断流，请求重新生成该片段（<100ms）
- L2 失败: 标记问题，继续生成其他片段，最后批量修复
- L3 失败: 生成修复建议，追加到对话中重新生成
- L4 失败: 回滚整个注入，报告具体错误位置

## 四、详细架构设计

### 4.1 后端架构重构

#### 4.1.1 新的服务分层

```
┌──────────────────────────────────────────────────┐
│ API Layer (FastAPI)                             │
│ ├─ /api/ai-blocks/generate-unified (新)         │
│ │   └─ 统一生成端点，替代旧的多端点               │
│ ├─ /api/ai-blocks/chat (保留，用于纯对话)        │
│ └─ /api/ai-blocks/validate (新，独立验证端点)    │
└──────────────────────────────────────────────────┘
         ↓
┌──────────────────────────────────────────────────┐
│ Orchestration Layer                             │
│ ├─ GenerationOrchestrator (新)                  │
│ │   ├─ 管理整个生成流程                          │
│ │   ├─ 处理流式输出                              │
│ │   └─ 协调验证和修复                            │
│ └─ CacheManager (新)                            │
│     ├─ 管理提示词缓存                            │
│     └─ 跟踪缓存命中率                            │
└──────────────────────────────────────────────────┘
         ↓
┌──────────────────────────────────────────────────┐
│ Prompt Layer (重构)                             │
│ ├─ PromptBuilder (新)                           │
│ │   ├─ build_knowledge_base() → 缓存key 1       │
│ │   ├─ build_project_context() → 缓存key 2      │
│ │   ├─ build_target_context() → 缓存key 3       │
│ │   └─ build_request_context() → 不缓存         │
│ └─ PromptTemplateRegistry (新)                  │
│     └─ 管理所有模板版本                          │
└──────────────────────────────────────────────────┘
         ↓
┌──────────────────────────────────────────────────┐
│ Generation Layer                                │
│ ├─ StreamingGenerator (新)                      │
│ │   └─ 处理流式结构化输出                        │
│ ├─ ValidationPipeline (重构)                    │
│ │   ├─ FormatValidator                          │
│ │   ├─ StructureValidator                       │
│ │   ├─ SemanticValidator                        │
│ │   └─ RuntimeValidator                         │
│ └─ RepairEngine (新)                            │
│     └─ 智能修复策略                              │
└──────────────────────────────────────────────────┘
         ↓
┌──────────────────────────────────────────────────┐
│ Provider Layer (保持)                           │
│ └─ AgnesTextProvider                            │
└──────────────────────────────────────────────────┘
```

#### 4.1.2 核心类设计

**PromptBuilder** (新):

```python
class PromptBuilder:
    """统一的提示词构建器"""
    
    def __init__(self):
        self.knowledge_base = self._load_knowledge_base()
        self.cache = PromptCache()
    
    def build_unified_prompt(
        self,
        project_context: ProjectContext,
        target_context: TargetContext,
        request: GenerateRequest
    ) -> tuple[list[dict], CacheMetadata]:
        """构建分层缓存的提示词"""
        
        # Level 1: 全局知识库（永久缓存）
        level1_key = "knowledge_base_v1"
        level1 = self.cache.get_or_build(
            level1_key,
            lambda: self._build_knowledge_base()
        )
        
        # Level 2: 项目上下文（5分钟缓存）
        level2_key = f"project_{project_context.project_id}_v{project_context.version}"
        level2 = self.cache.get_or_build(
            level2_key,
            lambda: self._build_project_context(project_context),
            ttl=300
        )
        
        # Level 3: 目标上下文（5分钟缓存）
        level3_key = f"target_{target_context.target_id}_v{target_context.revision}"
        level3 = self.cache.get_or_build(
            level3_key,
            lambda: self._build_target_context(target_context),
            ttl=300
        )
        
        # Level 4: 请求特定内容（不缓存）
        level4 = self._build_request_context(request)
        
        messages = [
            {"role": "system", "content": level1 + "\n\n" + level2 + "\n\n" + level3},
            *request.messages,
            {"role": "user", "content": level4}
        ]
        
        cache_meta = CacheMetadata(
            keys=[level1_key, level2_key, level3_key],
            estimated_hit_rate=0.85
        )
        
        return messages, cache_meta
    
    def _build_knowledge_base(self) -> str:
        """构建全局知识库（所有请求共享）"""
        return f"""# Scratch AI 代码生成系统

## 你的角色
你是 Scratch 积木代码生成专家，负责将用户需求转化为符合 ProgramIR 规范的 Scratch 程序。

## 核心协议
你将分三个阶段完成生成：
1. 需求理解阶段：输出 <requirements>...</requirements>
2. 规划阶段：输出 <plan>...</plan>  
3. 生成阶段：流式输出 <program>...</program>

## Scratch 积木完整规范
{SCRATCH_CODE_GENERATION_PROMPT}

## Block Catalog
{self._format_block_catalog()}

## ProgramIR Schema
{self._format_program_ir_schema()}

## 生成规范
{SCRATCH_CODE_STANDARD}

## 允许的 Opcodes
{', '.join(sorted(NATIVE_OPCODES))}
"""

    def _build_project_context(self, ctx: ProjectContext) -> str:
        """构建项目级上下文"""
        return f"""## 当前项目上下文
- 项目ID: {ctx.project_id}
- 所有角色: {', '.join(ctx.sprite_names)}
- 舞台背景: {', '.join(ctx.backdrop_names)}
- 共享变量: {', '.join(ctx.global_variables)}
- 共享广播: {', '.join(ctx.broadcasts)}
"""

    def _build_target_context(self, ctx: TargetContext) -> str:
        """构建目标级上下文"""
        return f"""## 当前生成目标
- 名称: {ctx.name}
- 类型: {'舞台' if ctx.is_stage else '角色'}
- 现有脚本数: {ctx.script_count}
- 造型: {', '.join(ctx.costumes)}
- 声音: {', '.join(ctx.sounds)}
- 当前模式: {ctx.mode}
"""

    def _build_request_context(self, req: GenerateRequest) -> str:
        """构建请求特定上下文（不缓存）"""
        return f"""## 当前任务
用户需求: {req.requirements.goal}

生成模式: {req.target.mode}
- append: 只新增脚本，不修改现有脚本
- rewrite: 重写所有可编辑脚本
- modify: 修改指定脚本

请开始分阶段生成。
"""
```

**GenerationOrchestrator** (新):

```python
class GenerationOrchestrator:
    """统一生成流程编排器"""
    
    async def generate_unified(
        self,
        request: GenerateRequest,
        provider: AgnesTextProvider
    ) -> AsyncIterator[GenerationEvent]:
        """统一生成流程"""
        
        # 1. 构建分层提示词
        prompt_builder = PromptBuilder()
        messages, cache_meta = prompt_builder.build_unified_prompt(
            self._get_project_context(request),
            self._get_target_context(request),
            request
        )
        
        yield CacheHitEvent(cache_meta)
        
        # 2. 开始流式生成
        phase = 'requirements'
        buffer = ''
        
        async for delta in provider.stream_with_reasoning(messages, ...):
            if delta.kind == 'content':
                buffer += delta.text
                yield ContentDeltaEvent(delta.text)
                
                # 3. 实时解析阶段标记
                if '<requirements>' in buffer and phase == 'requirements':
                    yield PhaseStartEvent('requirements')
                elif '</requirements>' in buffer and phase == 'requirements':
                    # 提取并验证 requirements
                    req_json = self._extract_xml_content(buffer, 'requirements')
                    result = self._validate_requirements(req_json)
                    if result.valid:
                        yield PhaseCompleteEvent('requirements', result.data)
                        phase = 'plan'
                    else:
                        # 在对话中注入验证错误，让 LLM 自我修复
                        yield ValidationErrorEvent('requirements', result.errors)
                        # 继续流式输出，LLM 会自动重试
                
                elif '<plan>' in buffer and phase == 'plan':
                    yield PhaseStartEvent('plan')
                elif '</plan>' in buffer and phase == 'plan':
                    plan_json = self._extract_xml_content(buffer, 'plan')
                    result = self._validate_plan(plan_json)
                    if result.valid:
                        yield PhaseCompleteEvent('plan', result.data)
                        phase = 'program'
                    else:
                        yield ValidationErrorEvent('plan', result.errors)
                
                elif '<program>' in buffer and phase == 'program':
                    yield PhaseStartEvent('program')
                    # 切换到流式脚本解析模式
                    await self._stream_program_generation(
                        delta_stream=...,
                        on_event=yield
                    )
        
        yield GenerationCompleteEvent()
    
    async def _stream_program_generation(
        self,
        delta_stream,
        on_event: Callable
    ):
        """流式程序生成"""
        script_buffer = ''
        script_index = 0
        
        async for delta in delta_stream:
            script_buffer += delta.text
            
            # 检测单个脚本完成
            if self._is_script_complete(script_buffer):
                script_json = self._extract_script(script_buffer)
                
                # 立即验证和编译
                result = self._validate_and_compile_script(script_json, script_index)
                
                if result.valid:
                    # 立即注入到 Scratch
                    await on_event(ScriptReadyEvent(script_index, result.compiled))
                    script_buffer = ''
                    script_index += 1
                else:
                    # 累积错误，继续生成其他脚本
                    await on_event(ScriptErrorEvent(script_index, result.errors))
        
        # 所有脚本生成完成
        await on_event(ProgramCompleteEvent())
```

**StreamingGenerator** (新):

```python
class StreamingGenerator:
    """流式结构化生成器"""
    
    async def generate_streaming_program(
        self,
        messages: list[dict],
        provider: AgnesTextProvider
    ) -> AsyncIterator[ProgramChunk]:
        """
        流式生成程序，按结构分块输出
        
        输出顺序:
        1. metadata: {summary, remove_script_ids}
        2. shared_state: {variables, lists, broadcasts}
        3. script_0: {...}
        4. script_1: {...}
        ...
        """
        
        # 指示 LLM 按结构分块输出
        structured_instruction = """
请按以下顺序流式输出 ProgramIR 的各个部分：

1. <metadata>
{
  "schema_version": 1,
  "summary": "...",
  "remove_script_ids": [...]
}
</metadata>

2. <shared_state>
{
  "variables": [...],
  "lists": [...],
  "broadcasts": [...]
}
</shared_state>

3. <script id="script_0">
{
  "id": "script_0",
  "purpose": "...",
  "blocks": [...]
}
</script>

4. <script id="script_1">
...
</script>

每个部分完成后立即输出，不要等待整个 JSON 完成。
"""
        
        messages_with_instruction = messages + [
            {"role": "user", "content": structured_instruction}
        ]
        
        buffer = ''
        async for delta in provider.stream_with_reasoning(messages_with_instruction, ...):
            if delta.kind == 'content':
                buffer += delta.text
                
                # 解析已完成的块
                for chunk in self._parse_complete_chunks(buffer):
                    if chunk.type == 'metadata':
                        yield MetadataChunk(**chunk.data)
                    elif chunk.type == 'shared_state':
                        yield SharedStateChunk(**chunk.data)
                    elif chunk.type == 'script':
                        yield ScriptChunk(**chunk.data)
                    
                    # 从buffer中移除已处理的块
                    buffer = self._remove_processed_chunk(buffer, chunk)
```

### 4.2 前端架构重构

#### 4.2.1 新的生成流程管理

```typescript
// generation-runner-v2.ts

interface UnifiedGenerationOptions {
  bridge: ScratchGuiBridge;
  snapshot: AiTargetSnapshot;
  requirements: Requirements;
  mode: AiBlocksMode;
  onProgress: (event: GenerationProgressEvent) => void;
  signal?: AbortSignal;
}

interface GenerationProgressEvent {
  type: 'cache_hit' | 'phase_start' | 'phase_complete' | 
        'script_ready' | 'content_delta' | 'error';
  phase?: 'requirements' | 'plan' | 'program';
  data?: any;
}

class UnifiedGenerationRunner {
  async run(options: UnifiedGenerationOptions): Promise<AiGenerationResult> {
    const {bridge, snapshot, requirements, mode, onProgress, signal} = options;
    
    // 1. 单一 SSE 连接
    const eventSource = await this.api.generateUnified({
      requirements,
      target: snapshot,
      mode,
      model_tier: this.modelTier,
      thinking: this.thinkingSettings
    });
    
    let plan: Plan | null = null;
    const compiledScripts: CompiledScript[] = [];
    const pendingVariables: PendingVariable[] = [];
    
    // 2. 处理流式事件
    for await (const event of eventSource) {
      throwIfAborted(signal);
      
      switch (event.type) {
        case 'cache_hit':
          // 缓存命中，预期后续生成更快
          onProgress({
            type: 'cache_hit',
            data: {estimated_hit_rate: event.hit_rate}
          });
          break;
        
        case 'phase_start':
          onProgress({
            type: 'phase_start',
            phase: event.phase
          });
          break;
        
        case 'phase_complete':
          if (event.phase === 'plan') {
            plan = event.data;
            onProgress({type: 'phase_complete', phase: 'plan', data: plan});
          }
          break;
        
        case 'script_ready':
          // 脚本已验证和编译，立即注入
          const compiled = event.data;
          await this.injectScript(bridge, snapshot, compiled);
          compiledScripts.push(compiled);
          
          onProgress({
            type: 'script_ready',
            phase: 'program',
            data: {
              current: compiledScripts.length,
              total: event.total_scripts
            }
          });
          break;
        
        case 'content_delta':
          // 可选：显示生成中的文本（用于调试）
          break;
        
        case 'validation_error':
          // LLM 正在自我修复，无需用户干预
          console.warn('AI self-repairing:', event.errors);
          break;
        
        case 'error':
          throw new Error(event.message);
      }
    }
    
    return {
      targetId: snapshot.targetId,
      targetName: snapshot.targetName,
      addedScripts: compiledScripts.length,
      addedBlocks: compiledScripts.reduce((sum, s) => sum + s.blockIds.length, 0),
      removedScripts: 0,
      revisionAtComplete: bridge.projectRevision,
      backup: await bridge.saveProject()
    };
  }
  
  private async injectScript(
    bridge: ScratchGuiBridge,
    snapshot: AiTargetSnapshot,
    compiled: CompiledScript
  ): Promise<void> {
    // 单个脚本注入（无需 110ms 延迟）
    const target = this.getTarget(bridge, snapshot);
    const blocks = target.blocks!;
    
    // 一次性创建所有块
    for (const blockId of compiled.blockIds) {
      blocks.createBlock(compiled.blocks[blockId]);
    }
    
    // 刷新工作区
    bridge.refreshWorkspace();
  }
}
```

#### 4.2.2 缓存感知的 UI

```vue
<!-- AiBlocksPanel.vue (增强) -->
<template>
  <div class="ai-blocks-panel">
    <!-- 缓存状态指示器 -->
    <div v-if="cacheHitRate > 0" class="cache-indicator">
      <Icon name="lightning" />
      <span>智能缓存加速 {{ Math.round(cacheHitRate * 100) }}%</span>
    </div>
    
    <!-- 实时进度 -->
    <div v-if="isGenerating" class="generation-progress">
      <div v-if="currentPhase === 'requirements'" class="phase">
        <Spinner /> 理解需求中...
      </div>
      <div v-else-if="currentPhase === 'plan'" class="phase">
        <CheckIcon /> 需求已确认
        <Spinner /> 规划实现方案...
      </div>
      <div v-else-if="currentPhase === 'program'" class="phase">
        <CheckIcon /> 需求已确认
        <CheckIcon /> 方案已规划
        <Spinner /> 生成积木 {{ scriptsReady }}/{{ totalScripts }}
        
        <!-- 实时脚本列表 -->
        <ul class="script-list">
          <li v-for="script in readyScripts" :key="script.id">
            <CheckIcon /> {{ script.purpose }}
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
const currentPhase = ref<'requirements' | 'plan' | 'program' | null>(null);
const cacheHitRate = ref(0);
const scriptsReady = ref(0);
const totalScripts = ref(0);
const readyScripts = ref<ScriptInfo[]>([]);

async function handleGenerate() {
  const runner = new UnifiedGenerationRunner(api);
  
  await runner.run({
    bridge,
    snapshot,
    requirements: currentRequirements.value,
    mode: generationMode.value,
    onProgress: (event) => {
      if (event.type === 'cache_hit') {
        cacheHitRate.value = event.data.estimated_hit_rate;
      } else if (event.type === 'phase_start') {
        currentPhase.value = event.phase;
      } else if (event.type === 'script_ready') {
        scriptsReady.value = event.data.current;
        totalScripts.value = event.data.total;
        readyScripts.value.push(event.data.script);
      }
    },
    signal: abortController.signal
  });
}
</script>
```

### 4.3 数据流优化

#### 4.3.1 对比：旧 vs 新

**旧架构数据流**:
```
用户输入
  ↓ HTTP Request 1
[后端] /requirements
  → 构建 prompt (无缓存)
  → LLM 调用 1 (8-12K tokens)
  → JSON 解析
  → 失败? 修复循环 (每次 +12K tokens)
  ↓ HTTP Response 1
前端收到 requirements
  ↓ HTTP Request 2
[后端] /generate (plan)
  → 构建 prompt (无缓存)
  → LLM 调用 2 (10-15K tokens)
  → JSON 解析
  → 失败? 修复循环 (每次 +15K tokens)
  ↓ HTTP Response 2
前端收到 plan
  ↓ HTTP Request 3
[后端] /generate (program)
  → 构建 prompt (无缓存)
  → LLM 调用 3 (12-20K tokens)
  → JSON 解析
  → 失败? 修复循环 (每次 +20K tokens)
  ↓ HTTP Response 3
前端收到完整 ProgramIR
  ↓ 
前端编译和渐进注入 (5-10 秒)
  ↓
完成

总耗时: 15-30 秒
总 tokens: 30-60K (无失败) / 80-150K (有失败)
缓存命中: <5%
```

**新架构数据流**:
```
用户输入
  ↓ HTTP Request (SSE)
[后端] /generate-unified
  → 构建分层 prompt
    ├─ Level 1: 知识库 (缓存命中 100%)
    ├─ Level 2: 项目 (缓存命中 90%)
    ├─ Level 3: 目标 (缓存命中 80%)
    └─ Level 4: 请求 (新)
  → 单次 LLM 调用 (流式)
    ├─ 输出 requirements (2 秒)
    │   → 实时验证 → 通过 → 继续
    ├─ 输出 plan (1 秒)
    │   → 实时验证 → 通过 → 继续
    └─ 流式输出 scripts
        ├─ script_0 完成 → 验证 → 编译 → 注入 (即时)
        ├─ script_1 完成 → 验证 → 编译 → 注入 (即时)
        └─ ...
  ↓ SSE Stream
前端实时接收并注入脚本
  ↓
完成

总耗时: 3-8 秒
总 tokens: 8-15K (80% 缓存命中)
缓存命中: 80-95%
```

**性能提升**:
- **速度**: 5-10× 更快（15-30s → 3-8s）
- **Tokens**: 3-5× 更少（40K → 10K 平均）
- **用户体验**: 渐进式反馈 vs 长时间等待
- **成本**: 降低 60-80%

## 五、实施计划

### 5.1 阶段划分

#### Phase 1: 基础设施（1-2 周）

**目标**: 搭建新架构的核心基础

**任务**:
1. 实现 `PromptBuilder` 和分层缓存机制
2. 实现 `GenerationOrchestrator` 基础框架
3. 创建新的 `/api/ai-blocks/generate-unified` 端点
4. 实现基础的流式事件协议

**交付物**:
- `backend/app/prompt_builder.py`
- `backend/app/orchestration/`
  - `__init__.py`
  - `orchestrator.py`
  - `cache_manager.py`
- `backend/app/routes/ai_blocks_v2.py`
- 单元测试覆盖率 >80%

**验收标准**:
- 分层提示词构建正确
- 缓存机制工作正常（能命中和失效）
- 新端点能返回流式事件

#### Phase 2: 流式生成（2-3 周）

**目标**: 实现流式结构化输出

**任务**:
1. 实现 `StreamingGenerator`
2. 实现 XML 标签式的分阶段输出解析
3. 实现流式验证管道
4. 后端与前端的 SSE 集成

**交付物**:
- `backend/app/generation/streaming_generator.py`
- `backend/app/validation/streaming_validator.py`
- `frontend/src/ai-blocks/generation-runner-v2.ts`
- 完整的端到端测试

**验收标准**:
- 能流式生成 requirements → plan → program
- 每个阶段能独立验证
- 前端能实时接收和显示进度

#### Phase 3: 智能修复（1-2 周）

**目标**: 实现对话内的自动修复

**任务**:
1. 实现 `RepairEngine`
2. 设计验证错误的反馈格式
3. 优化修复提示词
4. 错误处理和降级策略

**交付物**:
- `backend/app/repair/repair_engine.py`
- 修复策略文档
- 错误案例库和测试

**验收标准**:
- 验证失败时能在对话中自动修复
- 修复成功率 >90%
- 修复不超过 2 轮

#### Phase 4: 前端优化（1-2 周）

**目标**: 优化用户体验

**任务**:
1. 实现新的进度 UI
2. 缓存状态可视化
3. 实时脚本注入
4. 错误反馈优化

**交付物**:
- `frontend/src/ai-blocks/AiBlocksPanel-v2.vue`
- `frontend/src/ai-blocks/CacheIndicator.vue`
- UI 交互录屏演示

**验收标准**:
- 用户能看到实时生成进度
- 缓存命中有视觉反馈
- 脚本注入无明显卡顿

#### Phase 5: 灰度发布（1 周）

**目标**: 平滑切换到新架构

**任务**:
1. 实现特性开关（Feature Flag）
2. A/B 测试配置
3. 监控和日志增强
4. 性能基准测试

**交付物**:
- Feature flag 配置
- 监控 Dashboard
- 性能对比报告

**验收标准**:
- 新旧架构能灵活切换
- 关键指标有监控
- 性能提升达到预期

#### Phase 6: 全量上线（1 周）

**目标**: 完全迁移到新架构

**任务**:
1. 逐步扩大新架构流量
2. 监控和问题修复
3. 清理旧代码
4. 文档更新

**交付物**:
- 100% 流量切换
- 旧代码删除 PR
- 更新的 API 文档

**验收标准**:
- 新架构稳定运行
- 无重大 bug
- 性能指标持续达标

### 5.2 风险和缓解

#### 风险 1: LLM 不遵循结构化输出格式

**风险等级**: 高

**影响**: 流式解析失败，回退到旧模式

**缓解措施**:
1. 多轮 prompt 工程和测试
2. 使用 XML 标签比纯 JSON 更可靠
3. 如果 provider 支持，使用 Structured Output API
4. 保留降级路径：检测到格式混乱时回退到完整 JSON 模式

#### 风险 2: 缓存策略不符合实际使用模式

**风险等级**: 中

**影响**: 缓存命中率低于预期

**缓解措施**:
1. 在 Phase 5 收集真实缓存命中数据
2. 根据数据调整缓存分层策略
3. 引入更细粒度的缓存 key

#### 风险 3: 流式注入导致 Scratch VM 不稳定

**风险等级**: 中

**影响**: 脚本注入失败或显示异常

**缓解措施**:
1. 充分测试各种边缘情况
2. 保留完整的回滚机制
3. 如果不稳定，退回到批量注入模式（仍比旧版快）

#### 风险 4: 迁移期间影响现有用户

**风险等级**: 低

**影响**: 生成功能短暂不可用

**缓解措施**:
1. 使用 Feature Flag 控制切换
2. 灰度发布，逐步扩大范围
3. 保留旧架构作为备份
4. 24 小时监控和快速回滚能力

### 5.3 成功指标

| 指标 | 当前值 | 目标值 | 测量方法 |
|-----|--------|--------|---------|
| 平均生成时间 | 15-30s | <8s | 服务端计时 |
| 首屏反馈时间 | 15s+ | <2s | 前端计时 |
| 缓存命中率 | <5% | >80% | 后端日志统计 |
| Token 消耗 | 30-60K | <15K | Provider API 统计 |
| JSON 解析成功率 | ~60% | >95% | 后端日志统计 |
| 修复循环次数 | 0.8次/请求 | <0.1次/请求 | 后端日志统计 |
| 用户满意度 | 基线 | +30% | 用户反馈调查 |

## 六、长期优化方向

### 6.1 智能预测和预缓存

基于用户历史行为，预测下一步可能的操作并预缓存：

```python
class PredictiveCacheWarmer:
    """智能缓存预热"""
    
    async def warm_cache_for_user(self, user_id: str, project_id: str):
        """根据用户行为预测并预热缓存"""
        
        # 分析用户最近的操作序列
        recent_actions = await self.get_recent_actions(user_id, limit=10)
        
        # 预测可能的下一步
        predictions = self.predict_next_targets(recent_actions)
        
        # 预构建提示词并缓存
        for prediction in predictions:
            if prediction.confidence > 0.7:
                await self.prebuild_prompt(project_id, prediction.target_id)
```

### 6.2 增量更新而非完整重写

对于 `modify` 模式，改为生成"补丁"而非完整程序：

```typescript
interface ProgramPatch {
  type: 'add' | 'remove' | 'modify';
  target_script_id?: string;
  operations: Operation[];
}

// 例如：只新增一个积木
{
  type: 'modify',
  target_script_id: 'script_1',
  operations: [
    {
      type: 'insert_after',
      anchor_block: 'block_5',
      new_blocks: [...]
    }
  ]
}
```

优势：
- 生成更快（只生成变化部分）
- 更精确（不会意外改变其他部分）
- 更易验证

### 6.3 多模态输入

支持用户通过截图、手绘草图描述需求：

```python
async def generate_from_image(
    image: UploadFile,
    text_hint: str
) -> Requirements:
    """从图像提取需求"""
    
    # 使用视觉模型分析图像
    vision_result = await vision_model.analyze(image)
    
    # 结合文字提示
    combined_prompt = f"""
用户上传了一张图像，描述了想要的效果：
{vision_result.description}

用户的额外说明：{text_hint}

请提取 Scratch 需求。
"""
    
    return await extract_requirements(combined_prompt)
```

### 6.4 协作式生成

多个用户同时编辑时，AI 感知其他用户的操作：

```python
class CollaborativeGenerator:
    """协作式生成器"""
    
    async def generate_with_awareness(
        self,
        request: GenerateRequest,
        active_users: list[ActiveUser]
    ):
        """感知其他用户正在做什么"""
        
        context = f"""
当前项目中其他用户正在：
{self._format_active_users(active_users)}

请生成不冲突的代码，避免修改其他用户正在编辑的部分。
"""
        
        return await self.generate(request, additional_context=context)
```

### 6.5 自学习优化

收集成功和失败的案例，持续优化提示词：

```python
class PromptOptimizer:
    """提示词自学习优化器"""
    
    async def learn_from_feedback(
        self,
        generation_id: str,
        user_feedback: Feedback
    ):
        """从用户反馈中学习"""
        
        if user_feedback.rating >= 4:
            # 成功案例：记录有效的模式
            await self.record_success_pattern(generation_id)
        else:
            # 失败案例：分析问题并调整
            await self.analyze_failure(generation_id, user_feedback.issues)
    
    async def evolve_prompts(self):
        """定期演化提示词"""
        
        # 分析最近 1000 次生成
        patterns = await self.analyze_recent_generations(limit=1000)
        
        # 发现问题模式
        issues = self.find_common_issues(patterns)
        
        # 生成新的提示词变体
        new_variants = await self.generate_prompt_variants(issues)
        
        # A/B 测试新变体
        await self.deploy_for_testing(new_variants)
```

## 七、总结

### 7.1 核心改进

本架构重构通过以下三大核心改进解决现有问题：

1. **统一提示词前缀 + 分层缓存**: 将缓存命中率从 <5% 提升到 >80%
2. **单次对话 + 流式生成**: 将生成时间从 15-30s 降低到 3-8s
3. **智能验证分层**: 将 JSON 解析成功率从 ~60% 提升到 >95%

### 7.2 预期收益

- **用户体验**: 5-10× 响应速度提升，实时反馈取代长时间等待
- **成本**: 降低 60-80% 的 AI 调用成本
- **稳定性**: 减少 90% 的解析失败和修复循环
- **可扩展性**: 为未来的多模态、协作式生成打下基础

### 7.3 实施建议

1. **优先级**: 按 Phase 1-6 顺序实施，每个阶段验收后再进入下一阶段
2. **风险控制**: 使用 Feature Flag 和灰度发布，随时可回滚
3. **数据驱动**: 在 Phase 5 收集详细的性能数据，验证假设
4. **持续优化**: 上线后继续监控关键指标，根据实际数据调整策略

### 7.4 下一步行动

1. **评审**: 组织架构评审会议，确认方案可行性
2. **细化**: 补充技术细节和边缘情况处理
3. **启动**: 创建 Phase 1 的详细任务列表，分配开发资源
4. **跟踪**: 建立项目看板，每周同步进度

---

**文档结束**
