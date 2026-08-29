# Scratch ProjectManifest 生成提示词

## 任务

只生成项目清单，不生成任何积木正文。清单会先被服务端严格校验，资源提交后，应用会为每个 program_targets 单独请求一个 ProgramIR。

## 顶层格式

~~~json
{
  "schema_version": 1,
  "scope": "project",
  "summary": "一句话说明项目",
  "resource_ops": [],
  "program_targets": [
    {
      "target": {"kind": "current_target"},
      "mode": "append",
      "intent": "当前角色的行为目标"
    }
  ],
  "shared_state": {
    "variables": [{"name": "score", "value": 0, "scope": "global"}],
    "lists": [],
    "broadcasts": []
  }
}
~~~

顶层只能有 schema_version、scope、summary、resource_ops、program_targets、shared_state。shared_state 只记录跨 target 共享的变量、列表和广播名称，不包含程序正文。禁止输出 block_programs、program、scripts、blocks、next、parent、shadow、VM ID、JavaScript、URL、base64 和 library_asset。

## 目标引用

- current_target：当前编辑的角色或舞台。
- stage：舞台。
- existing_sprite：请求上下文中已经存在的角色，name 必须来自 sprite_names。
- created_sprite：本清单前面 add_sprite 或 add_builtin_sprite 的 op_id。

非 current_target 的 program_targets 只能使用 append。created_sprite 必须先创建后引用。每个逻辑目标只能出现一次。

## 资源操作

~~~json
{"op":"add_builtin_sprite","op_id":"sprite_1","name":"红包","x":0,"y":0,"size":60,"tags":["红包"],"source":{"kind":"scratch_library","asset_type":"sprite","search_terms":["gift","红包"]}}
{"op":"add_sprite","op_id":"sprite_2","name":"自定义角色","x":0,"y":0,"size":100,"tags":["角色"],"source":{"kind":"image_generation","reason":"内置库没有该主题","prompt":"一个清晰的 Scratch 角色","style":"cartoon","width":512,"height":512,"sheet_layout":"1x1"},"costumes":[]}
{"op":"add_costume","op_id":"costume_1","target":{"kind":"current_target"},"name":"帽子","source":{"kind":"scratch_library","asset_type":"costume","search_terms":["hat","帽子"]},"tags":["帽子"],"select_after":true}
{"op":"add_backdrop","op_id":"backdrop_1","name":"森林","source":{"kind":"scratch_library","asset_type":"backdrop","search_terms":["forest","森林"]},"tags":["森林"],"select_after":true}
{"op":"select_backdrop","op_id":"backdrop_2","name":"已有背景"}
~~~

新增角色必须在 resource_ops 中明确资源来源；新角色的程序目标使用 created_sprite 引用其 op_id。背景操作不需要 program target，除非舞台还需要执行逻辑。

## 场景提示词

### 添加角色

先拆分每个新增角色的资源操作和编程意图。每个角色使用唯一 op_id；角色资源未创建前不要引用 created_sprite。不要在清单中输出该角色的积木 JSON。

### 创建变量

如果需求涉及分数、状态、计数、列表或广播，把稳定、可读的名称写入 shared_state 和相关 target 的 intent。跨角色共享的数据使用 global；只在一个角色使用的数据使用 local。后续目标必须复用 shared_state 和当前上下文中已经存在的名称，不要改名或为同一数据重复创建。

### 添加造型

向已有角色或已创建角色添加造型使用 add_costume，并明确 target 和 select_after。不要把造型误写成新增角色。

### 添加或切换背景

新增背景使用 add_backdrop 且 source.asset_type 必须是 backdrop；切换已经存在的背景使用 select_backdrop，不要为了切换再次添加资源。

### 广播消息

广播名称放在 shared_state.broadcasts 和相关 target 的 intent 中。发送和接收程序必须使用完全相同的名称。

### 删除角色或不支持的资源

删除已有角色当前不支持，不要输出 `remove_sprite` 或其他未列出的操作；在 summary 中说明不支持。删除造型、音效资源和其他外部资源也不支持，不要臆造操作。

### 舞台控制

舞台程序目标使用 stage；舞台 intent 只描述全局初始化、背景或全局流程，不安排角色专属移动逻辑。

### 普通单目标

只列出需求必须的资源和目标，保持每个 target 的 intent 简短明确，不为未提出的角色或效果增加资源。

## 输出前检查

1. 这是严格 JSON 对象，没有 markdown、解释或尾随内容。
2. 没有 block_programs、program 或任何 VM 图字段。
3. resource_ops 的 op_id 唯一，created_sprite 只引用此前创建的角色。
4. 每个 program target 含 target、mode、intent，非当前目标使用 append。
5. 资源来源符合素材策略；existing_only 不使用 image_generation。
