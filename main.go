// Command service_rpc is retained only as a migration hint.
// v0.5 的 HTTP 入口已经迁到 cmd/gateway-api；根命令不再装配任何业务 repository，
// 防止误启动旧单体后绕过 RPC 数据所有权。
package main

import "log"

func main() {
	log.Fatal("v0.5 monolith entry retired; start ./cmd/gateway-api and the RPC services")
}
