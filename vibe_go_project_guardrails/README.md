# Go 秒杀 / 微服务 Vibe Coding Guardrails

这是一个为长期 Vibe Coding 准备的“防失控”脚手架。

## 使用方法

1. 把本目录内容复制到你的项目根目录。
2. 让 Codex / 其他 Agent 优先读取 `AGENTS.md`。
3. 开始某个版本前，先让 `project-planner` 根据对应 spec 规划 milestone。
4. 再用 `task-decomposer` 拆成小 TASK。
5. 每次只让 `implementation-guard` 完成一个 TASK。
6. 完成后执行 `test-verifier`。
7. 若涉及架构变化，用 `architecture-reviewer` 审查并写 ADR。
8. 一个 TASK 完成后再进入下一个。

## 最推荐的循环

```text
Spec
 ↓
Planner
 ↓
Task Decomposer
 ↓
Implementation Guard
 ↓
Test Verifier
 ↓
Architecture Review
 ↓
Commit
```

## 最重要的一条

不要直接对 Agent 说：

> 帮我把 v0.4 MQ 秒杀系统做完。

而应该说：

> 阅读 AGENTS.md、ARCHITECTURE.md、ROADMAP.md 和当前 spec。
> 只规划当前 milestone，不写代码。

随后逐 TASK 实现。
