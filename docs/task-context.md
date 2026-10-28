# Optional task context

Task context is opt-in. Start the MCP server with `aoci --repo <repository> mcp --task-context`; omit the flag to retain the existing complete-cognition mode and nine-tool surface. In an MCP configuration, add `--task-context` to that server's arguments only after the user authorizes the configuration change. Existing installations are not changed automatically.

Read `aoci_rules` to confirm `context_mode=task_context`. Begin through `aoci_get_entries`:

```json
{"task":{"action":"start","goal":"Investigate the requested behavior"}}
```

Save the returned `task_id`. Use subsequent task actions with that identity: `read`, `search`, `relations`, `status`, `map`, `checkpoint`, and `charge`. Read the bounded system map first. If it is absent or invalid, read current source evidence and let the Host model author a map; submit `map_text` and the exact `map_evidence` references from reads. Task maps and checkpoints are task materials; they are not formal complete-cognition or Apply proofs. The Host-authored map is shared across tasks. `read(kind=map)` refreshes its current version without resetting the phase or budget; if another task updated it, read that version and recheck its evidence before submitting a map update.

`read` accepts `kind=source` with `path`, `kind=entry` with `refs`, or `kind=meta/map`. Follow the returned `next_cursor` for more pages. `status` normally returns compact progress; `kind=pending/evidence/checkpoint/decisions` returns the corresponding details in bounded pages. When an AOCI tool returns `status=output_deferred` and a saved `result_ref`, read it with `kind=result` and its `path`; the complete result remains available without expanding a tool response beyond the task page. Read every candidate-response page before formal authoring; paging never permits slicing the machine-issued batch. Ordinary `aoci_search`, `aoci_get_entries` without `task`, and `aoci_header` return `bounded_read_required` in this mode: use the corresponding task actions instead.

Near the reading budget, checkpoint the current note and pending questions. This service automatically meters its tool output, and explicitly charged external reads, in bytes; it cannot measure or cap every Host token. Register external tool output using `charge`, `external_bytes`, and a descriptive `note`, and report any gap honestly. Unknown and unresolved relationships stay pending until the task's risk justifies more evidence.

When a deferred Maintain diagnostic positively proves an empty budget stop before batch issuance, its compact `stop_summary` reports `current_batch_issued=false` and the remaining target count. `next_action=checkpoint` with `full_result_read_required_for_next_action=false` permits saving progress without first reading that entire diagnostic. The complete diagnostic stays available at `result_ref`; every later issued candidate batch still requires complete reading before authoring. Unknown failures and missing fields do not qualify for this exception.

Budgets apply to each task and its current phase; they are not a cumulative limit for the MCP connection or all Host context. A genuinely new objective may start a new task. Continue the same unfinished objective with its saved `task_id`; do not change identity or repeat `start` merely to obtain another allowance. Use `resume` only after a real Host context reset and preserve prior progress and accounting gaps.

To settle selected pending questions, send `checkpoint` with `resolved` containing their exact existing text, a nonempty `note` explaining the Host judgment, and `resolution_evidence` containing current `source/entry/meta` evidence ranges actually read by this task. `status(kind=decisions)` pages through those judgments. Source, Entry, or policy drift invalidates affected judgments and reopens their questions. This does not increase machine progress or the formal Apply count; other pending work remains.

During formal Recovery, safe `checkpoint/status/charge` controls preserve cached progress. Their response declares `verification=cached_unverified` and `state.map_valid=false`; it does not deliver new repository evidence or resolve pending judgments. Recovery status permits only summary, `pending`, and `checkpoint`; material reads, `start/resume`, and budget resets remain blocked until Recovery closes.

When valid repository configuration exists but the Root index is absent, `aoci_rules` and source-first task reads remain available. They do not authorize Root/Meta, migration, layout, or any other formal write. Existing damaged cognition remains an error.

After compaction, recover the saved identity with a fresh event identity:

```json
{"task":{"action":"resume","task_id":"<saved task_id>","event_id":"<fresh compact event>"}}
```

Recheck the task state and reread only invalidated or necessary evidence. Configure `aoci hook codex-compact --task-context` explicitly when using the task-mode Codex compact hook; the default hook retains complete-cognition recovery. `aoci index agent guide --agent codex --task-context --json` names this task entry while preserving its existing governance stops. No command in this guide changes installed user configuration.

Complete Overview remains available on explicit request with its existing delivery and proof requirements. As an explicit global request, it is metered honestly and may exceed the task budget. Task context preserves complete machine-issued Code batches, source hashes, CAS, Recovery, approvals, and Root/Meta/migration boundaries.

# 可选任务上下文

任务模式必须显式启用：`aoci --repo <仓库> mcp --task-context`。省略该参数时仍使用原完整认知模式，公开工具仍为九个。只有获得用户授权后才在现有 MCP 服务参数中添加该开关，不自动修改既有安装。

先读 `aoci_rules` 确认 `context_mode=task_context`，再以 `aoci_get_entries` 的 `task.action=start` 和 `goal` 开始，保存返回的 `task_id`。后续通过 `read/search/relations/status/map/checkpoint/charge` 有界读取。先读系统地图；缺失或失效时先读取当前源码证据，再由 Host 模型独立创作地图并提交 `map_text` 与读取返回的精确 `map_evidence`。地图与 checkpoint 都不是正式完整认知或 Apply 证明。Host 创作的地图跨任务共享复用；`read(kind=map)` 刷新当前版本且不重置阶段或预算。其他任务更新地图后，先读取该版本并核对绑定证据，再提交地图更新。

源码用 `read(kind=source,path=...)`，Entry 用 `read(kind=entry,refs=[...])`，也可读取 `meta/map`。按 `next_cursor` 续页。默认 `status` 只返回摘要；`kind=pending/evidence/checkpoint/decisions` 分页返回相应详情。AOCI 工具结果过大时返回 `status=output_deferred` 和 `result_ref`，以该身份作为 `path`，用 `read(kind=result)` 分页读取完整结果。正式创作前必须读完候选响应全部页面，不得私切机器签发的完整批次。本模式普通 `aoci_search`、不带 `task` 的 `aoci_get_entries` 和 `aoci_header` 返回 `bounded_read_required`，应改用对应任务动作。

接近读取预算前先 checkpoint 保存进度与待查项。本服务工具输出自动按字节计量，宿主外部读取须显式登记，不能测量或硬控宿主所有 token；其他工具输出通过 `charge`、`external_bytes` 与 `note` 登记，缺口须如实说明。未知和未解析关系保留待查，按任务风险扩展证据。

Maintain 的大诊断若明确证明预算不足、零候选且未签发批次，短响应通过 `stop_summary` 说明本次没有批次以及剩余目标数，并以 `next_action=checkpoint`、`full_result_read_required_for_next_action=false` 允许直接保存进度。完整诊断仍保存在 `result_ref`；后续真正签发的候选批次仍须完整读取后才能创作。未知失败或缺字段不适用此例外。

预算按每个任务及其当前阶段计算，不是 MCP 连接或宿主全部上下文的累计上限。真正的新目标可以开始新任务；同一未完成目标应沿用保存的 `task_id`，不得只为获得新额度而更换身份或重复 `start`。仅在实际发生宿主上下文重置后使用 `resume`，并保留已有进度与计量缺口。

结清选定待查项时，发送 `checkpoint`，以 `resolved` 精确列出已有待查文本，提供非空 `note` 说明 Host 判断，并以 `resolution_evidence` 绑定本任务实际已读、当前有效的 `source/entry/meta` 证据范围。`status(kind=decisions)` 分页返回这些判断；源码、Entry 或策略漂移使相关判断失效、对应问题重新待查。此类结清不增加机器进度或正式 Apply 计数，其余待查工作保留。

正式 Recovery 期间，安全的 `checkpoint/status/charge` 控制仍可保存缓存进度。响应标记 `verification=cached_unverified` 且 `state.map_valid=false`，不交付新的仓库证据，也不结清待查判断；此时 status 仅允许摘要、`pending` 和 `checkpoint`。材料读取、`start/resume` 和预算重置继续阻断，直到 Recovery 关闭。

仓库已有有效配置但尚无 Root 索引时，仍可使用 `aoci_rules` 和从源码证据开始的任务读取；这些操作不授权 Root/Meta、迁移、布局或其他正式写入。已有认知损坏仍返回错误。

压缩后使用 `task.action=resume`、保存的 `task_id` 和新的 `event_id` 恢复，重新核对并只补读失效或必要的材料。Codex 压缩 hook 须显式配置 `aoci hook codex-compact --task-context`；默认 hook 仍执行完整认知恢复。`aoci index agent guide --agent codex --task-context --json` 会说明任务入口并保留原治理停点；本文命令不修改用户安装配置。

显式完整 Overview 仍遵守原交付与证明要求；作为明确全局请求例外，其交付如实计量并可能超过任务预算。任务模式保留机器签发的完整 Code 批次、源码摘要、CAS、Recovery、批准以及 Root/Meta/迁移边界。
