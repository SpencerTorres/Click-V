package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const (
	clickhouseHost     = "localhost"
	clickhousePort     = 9000
	clickhouseDatabase = "default"
	clickhouseUsername = "default"
	clickhousePassword = ""
)

func ConnectClickHouse() (clickhouse.Conn, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", clickhouseHost, clickhousePort)},
		Auth: clickhouse.Auth{
			Database: clickhouseDatabase,
			Username: clickhouseUsername,
			Password: clickhousePassword,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}
	return conn, nil
}

func BootstrapCPU(conn clickhouse.Conn, cpu *CPU) error {
	ctx := context.Background()

	log.Printf("Inserting PC value: 0x%08x", cpu.pc)
	if err := insertPC(ctx, conn, cpu.pc); err != nil {
		return fmt.Errorf("failed to insert PC: %w", err)
	}

	log.Printf("Inserting 32 registers")
	if err := insertRegisters(ctx, conn, cpu.regs); err != nil {
		return fmt.Errorf("failed to insert registers: %w", err)
	}

	log.Printf("Inserting %d bytes of memory", len(cpu.memory))
	if err := insertMemory(ctx, conn, cpu.memory); err != nil {
		return fmt.Errorf("failed to insert memory: %w", err)
	}

	log.Printf("Clearing print logs")
	if err := clearPrintLogs(ctx, conn); err != nil {
		return fmt.Errorf("failed to clear print logs: %w", err)
	}

	log.Printf("Successfully bootstrapped CPU state")
	return nil
}

func insertPC(ctx context.Context, conn clickhouse.Conn, pc uint32) error {
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO clickv.pc")
	if err != nil {
		return err
	}

	if err := batch.Append(pc); err != nil {
		return err
	}

	return batch.Send()
}

func insertRegisters(ctx context.Context, conn clickhouse.Conn, regs [32]uint32) error {
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO clickv.registers")
	if err != nil {
		return err
	}

	for i := 0; i < 32; i++ {
		if err := batch.Append(uint8(i), regs[i]); err != nil {
			return fmt.Errorf("failed to append register x%d: %w", i, err)
		}
	}

	return batch.Send()
}

func insertMemory(ctx context.Context, conn clickhouse.Conn, memory []byte) error {
	batch, err := conn.PrepareBatch(ctx, "INSERT INTO clickv.memory")
	if err != nil {
		return err
	}

	for i, b := range memory {
		if err := batch.Append(uint32(i), b); err != nil {
			return fmt.Errorf("failed to append memory at address 0x%08x: %w", i, err)
		}
	}

	return batch.Send()
}

func clearPrintLogs(ctx context.Context, conn clickhouse.Conn) error {
	err := conn.Exec(ctx, "TRUNCATE clickv.print")
	if err != nil {
		return err
	}

	return nil
}

func ReadClickHouseMemoryRange(conn clickhouse.Conn, start, end uint32) ([]byte, error) {
	ctx := context.Background()

	size := end - start
	buffer := make([]byte, size)

	query := `
		SELECT address, value 
		FROM clickv.memory 
		WHERE address >= ? AND address < ?
		ORDER BY address
	`

	rows, err := conn.Query(ctx, query, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query memory: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var address uint32
		var value uint8

		if err := rows.Scan(&address, &value); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		offset := address - start
		if offset < size {
			buffer[offset] = value
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	log.Printf("Read %d bytes from memory range [0x%08x, 0x%08x)", size, start, end)
	return buffer, nil
}

func ReadClickHousePC(conn clickhouse.Conn) (uint32, error) {
	ctx := context.Background()

	query := `SELECT value FROM clickv.pc`
	row := conn.QueryRow(ctx, query)
	if row.Err() != nil {
		return 0, fmt.Errorf("failed to query program counter: %w", row.Err())
	}

	var pc uint32
	if err := row.Scan(&pc); err != nil {
		return 0, fmt.Errorf("failed to scan row: %w", err)
	}

	return pc, nil
}

func ReadClickHouseRegisters(conn clickhouse.Conn) ([32]uint32, error) {
	var registers [32]uint32

	ctx := context.Background()

	query := `
		SELECT address, value 
		FROM clickv.registers
		ORDER BY address
	`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return registers, fmt.Errorf("failed to query registers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var address uint8
		var value uint32

		if err := rows.Scan(&address, &value); err != nil {
			return registers, fmt.Errorf("failed to scan row: %w", err)
		}

		registers[address] = value
	}

	if err := rows.Err(); err != nil {
		return registers, fmt.Errorf("row iteration error: %w", err)
	}

	return registers, nil
}

func RunClickHouseClockCycle(conn clickhouse.Conn) error {
	err := conn.Exec(context.Background(), "INSERT INTO clickv.clock (_) VALUES ()")
	if err != nil {
		return err
	}

	return nil
}

func compareRegisters(a, b [32]uint32) bool {
	for i := range a {
		aValue := a[i]
		bValue := b[i]

		if bValue != aValue {
			log.Printf("Register mismatch! Register: x%-2d A: 0x%08x (%d) != B: 0x%08x (%d)\n", i, aValue, int32(aValue), bValue, int32(bValue))
			return false
		}
	}

	return true
}
