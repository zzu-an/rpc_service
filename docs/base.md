- 最新版map的思想（粒度细化）

```text
group.go
   ↓
table.Get
   ↓
table.PutSlot
   ↓
probe sequence
   ↓
table grow
   ↓
table split
   ↓
Map.directoryIndex
```

