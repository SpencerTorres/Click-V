package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const (
	clickhouseHost     = "localhost"
	clickhousePort     = 9000
	clickhouseDatabase = "default"
	clickhouseUsername = "default"
	clickhousePassword = ""

	tableName = "clickv.memory"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <bin-file>\n", os.Args[0])
		os.Exit(1)
	}

	filePath := os.Args[1]

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", clickhouseHost, clickhousePort)},
		Auth: clickhouse.Auth{
			Database: clickhouseDatabase,
			Username: clickhouseUsername,
			Password: clickhousePassword,
		},
	})
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}
	defer conn.Close()

	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	log.Printf("Inserting %d bytes from %s", len(data), filePath)

	ctx := context.Background()
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s", tableName))
	if err != nil {
		log.Fatalf("Failed to prepare batch: %v", err)
	}

	for i, b := range data {
		if err := batch.Append(uint32(i), b); err != nil {
			log.Fatalf("Failed to append data at address 0x%08x: %v", i, err)
		}
	}

	if err := batch.Send(); err != nil {
		log.Fatalf("Failed to send batch: %v", err)
	}

	log.Printf("Successfully inserted %d bytes", len(data))
}
