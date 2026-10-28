---
anamnesis_version: 0.2
scope: project
inherit: false
---

# Anamnesis: AOCI 按需上下文实验

## 项目简介

在 AOCI 上游基础上试验按任务读取、证据核对及持久续接，解决连续建索引时全量读取与压缩反复占满上下文的问题。最终还需用真实开发验证能否减少漏改。

## Scope

本层属于修改版工具的开发项目，记录动机、决定、验证边界和跨电脑试用入口；游戏模组等使用方各自拥有项目记忆和索引。不复制用户全局偏好、业务私密内容、正式 Whole-Index/Entry 正文、凭证或整段对话。

## 文件清单

1. `current-state.md` — 当前实现、验证边界和下一步
2. `decisions.md` — 用户确认的设计与否决路线
3. `trial-guide.md` — 另一台电脑构建和试用入口
4. `research-history.md` — 按需调查背景、社区证据及从范围优化转向程序改造的原因
5. `lessons.md` — 按需读取的实验教训

## 项目阅读入口

日常用法以仓库 `docs/task-context.md` 为准，机器语义以 `spec/public/aoci-task-context-v1.txt` 和实时工具合同为准。记忆提供方向，不替代当前代码、任务状态或正式写入证明。仅在需要解释设计来由时读取 research-history；默认恢复不重放调查全文。

## 维护

当前状态覆盖更新；稳定选择进入 decisions；历史证据进入 research-history；跨电脑步骤进入 trial-guide。使用 Anamnesis MCP 维护。本层已获用户授权创建；远程发布方式仍待用户选择，不据此擅自公开。
