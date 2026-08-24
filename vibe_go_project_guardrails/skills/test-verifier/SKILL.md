# Skill: test-verifier

## 目标
证明当前 TASK 完成，而不是“看起来能工作”。

## 验证层次
1. 编译
2. 单元测试
3. 集成测试
4. 并发正确性测试
5. benchmark（需要性能结论时）
6. race detector（Go 并发代码）
7. 故障注入（高可用阶段）

## 常用命令
```bash
go test ./...
go test -race ./...
go test -count=100 ./path/to/pkg
go test -bench=. -benchmem ./path/to/pkg
go vet ./...
```

## 输出
- 执行命令
- PASS / FAIL
- 失败原因
- 是否阻塞合并
- 未覆盖风险

## 原则
没有测试证据，不得宣称“性能提升”或“并发安全”。
