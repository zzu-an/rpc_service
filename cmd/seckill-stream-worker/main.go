// Command seckill-stream-worker is retained only as an explicit v0.4.2 tombstone.
// v0.5 must run seckill-orchestrator; keeping the old direct-SQL worker runnable would allow
// two consumers with different transaction boundaries to process the same Stream.
package main

import (
	"errors"
	"log"
)

var ErrRetired = errors.New("seckill-stream-worker retired in v0.5; run cmd/seckill-orchestrator")

func main() { log.Fatal(ErrRetired) }

func run() error { return ErrRetired }
