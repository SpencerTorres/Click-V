package main

import (
	"context"
	"fmt"
	"time"

	"clickhouse.com/clickv/internal/db"
)

func main() {
	conn, err := db.GetClickHouseConnection()
	if err != nil {
		fmt.Println(err)
		return
	}

	var now time.Time
	var lastTime time.Time
	var cycles int64 = 0
	var totalCycles int64 = 0
	for {
		err := conn.Exec(context.Background(), "INSERT INTO clickv.clock (_) VALUEs ()")
		if err != nil {
			fmt.Println(err)
		}
		// time.Sleep(500 * time.Millisecond)
		cycles++
		totalCycles++

		now = time.Now()
		if now.Sub(lastTime) > time.Duration(1*time.Second) {
			fmt.Printf("clock speed: %dhz total cycles: %d\n", cycles, totalCycles)
			cycles = 0
			lastTime = now
		}
	}
}
