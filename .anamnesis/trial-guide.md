# 跨电脑试用入口

## 先拿到真正的修改

当前修改尚未提交或推送；只克隆上游或只切到当前HEAD不会得到实验实现。远端准备好后，确认获取的是包含修改及本记忆的提交。公共Fork不能作为私有仓库使用；仓库方式与可见性需按用户选择落实。

记忆应随选定分支交付；本层不依赖本机绝对路径，不需要复制小绿树的业务索引或本机.aoci任务缓存到模组仓库。游戏模组源代码留在它自己的仓库。

## 构建与接入

仓库go.mod声明Go 1.26.10。在当前克隆内安装/解析依赖；保留third_party中的本地replace目标，不借另一工作区的依赖替代验证。

PowerShell构建示例：

```powershell
New-Item -ItemType Directory -Force build | Out-Null
go build -o build/aoci-experiment.exe ./cmd/aoci
```

调用模式：

```text
aoci-experiment.exe --repo <模组仓库绝对路径> mcp --task-context
```

上面是stdio MCP服务，应由宿主配置启动。把本机生成程序的绝对路径和实际模组路径填入该项目服务配置；不能照搬另一台电脑路径。连接后先aoci_rules确认context_mode=task_context和目标仓库。省略开关仍是上游完整认知模式。既有全读规则或压缩hook必须核对模式，不能让配置相互矛盾。完整接口示例只维护在../docs/task-context.md。

模组未初始化时按该程序的init帮助与实时Guide建仓；已有索引先核对当前状态、版本及恢复事项。不导入别的仓库的Baseline，不重新初始化来掩盖冲突。遇到Root/Meta或正式批准停点按实时Guide处理，task_context不自动豁免。

## 小样验收

先登记范围与起始状态，完成一小批完整机器候选，再保存任务身份、进展、未决写入及待查；发生真实Host压缩后用同task_id和新event_id恢复，完成下一批。记录正式批次数、读取量、重复读取、待查保留和证据漂移。若实际先被全局治理挡住，如实记录零写入，不用模拟事件或新Agent冒充原生压缩。

第一层成功后才试真实跨模块修改；对比不用索引/用索引的遗漏与成本。游戏模组尚未测试，未知框架、语言、源码规模和生成物分布，不套用小绿树范围。

## 本机已有测试证据（未随Git保存的build报告需另行导出或重跑）

- build/task-context-report.json：固定夹具3+3完整提交、两进程、Overview0。
- build/self-index-final-report.json：8批31次语义写入、484/484对齐；阶段恢复来自新模型上下文，非原生compact验收。
- build/mcp-conformance-final.log：最终默认协议53通过。
- build/task-final-regression.log、build/test-repaired-packages.log：定向与修复包回归；旧测试运行不是本轮重跑。
- build/mcp-lifecycle-final.log：75通过、3失败、2平台特征；不能写全绿。
- build/make-fast.log、build/make-verify-windows.log：原生Windows包装失败。发布前应在适合环境跑仓库门禁，如实处理失败。

本记忆记录既有结果，并补充了2026-10-29排除build临时脚本后的定向Go包测试通过；没有把它描述成完整平台门禁通过。仅文件可读、程序能启动和索引覆盖率都不能证明模型理解正确。
