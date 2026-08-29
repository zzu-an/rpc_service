package mysqlrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV04JobMigrationContract(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	up, err := os.ReadFile(filepath.Join(root, "migrations", "000008_create_seckill_order_jobs.up.sql"))
	if err != nil {
		t.Fatalf("read v0.4 up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join(root, "migrations", "000008_create_seckill_order_jobs.down.sql"))
	if err != nil {
		t.Fatalf("read v0.4 down migration: %v", err)
	}

	// 这不是数据库集成测试；这里只把最容易被误删的 schema 不变量锁进快速测试。
	// 真正的 up/down/up 仍由 verify-v04 在隔离 MySQL 8 上执行，不能用文本断言冒充。
	for _, required := range []string{
		"CREATE TABLE seckill_order_jobs",
		"UNIQUE KEY uk_seckill_jobs_event_id",
		"UNIQUE KEY uk_seckill_jobs_order_no",
		"KEY idx_seckill_jobs_dispatch (status, next_publish_at, id)",
		"CHECK (status IN (1, 2, 3, 4))",
		"payload JSON NOT NULL",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("up migration misses %q", required)
		}
	}
	if strings.Count(string(down), "DROP TABLE") != 1 || !strings.Contains(string(down), "DROP TABLE IF EXISTS seckill_order_jobs") {
		t.Fatalf("down migration must only drop seckill_order_jobs")
	}
}
