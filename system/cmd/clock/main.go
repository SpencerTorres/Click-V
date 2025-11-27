package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"clickhouse.com/clickv/internal/db"
)

func main() {
	conn, err := db.GetClickHouseConnection()
	if err != nil {
		fmt.Println(err)
		return
	}

	var cycles atomic.Int64
	var totalCycles atomic.Int64

	go func() {
		for {
			<-time.After(1 * time.Second)
			fmt.Printf("clock speed: %dhz total cycles: %d\n", cycles.Load(), totalCycles.Load())
			cycles.Store(0)
		}
	}()

	for {
		err := conn.Exec(context.Background(), "INSERT INTO clickv.clock (_) VALUES ()")
		if err != nil {
			fmt.Println(err)
		}

		cycles.Add(1)
		totalCycles.Add(1)
	}
}
