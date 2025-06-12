package test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	cdb "clickhouse.com/clickv/internal/db"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

/**
 * This file assumes the CPU has been set up once.
 * The tests will handle resetting memory/program.
 *
 * optionally clear test cache: go clean -testcache
 */

const ROM_SIZE uint32 = 128                 // We don't need more than a few instructions
const RAM_SIZE uint32 = 32                  // With a wee bit of RAM
const MEM_SIZE uint32 = ROM_SIZE + RAM_SIZE // bytes

var instructionPerf = make(map[string]time.Duration, 64)
var reusableDB driver.Conn = nil

func getDB() (driver.Conn, error) {
	if reusableDB == nil {
		db, err := cdb.GetClickHouseConnection()
		if err != nil {
			return nil, fmt.Errorf("failed to get ClickHouse connection: %w", err)
		}

		reusableDB = db
	}

	return reusableDB, nil
}

func resetCPU(ctx context.Context, db driver.Conn) error {
	// Reset PC
	err := db.Exec(ctx, "INSERT INTO clickv.pc (value) VALUES (0)")
	if err != nil {
		return fmt.Errorf("failed to reset PC: %w", err)
	}

	// Reset registers
	err = db.Exec(ctx, "TRUNCATE TABLE clickv.registers SYNC")
	if err != nil {
		return fmt.Errorf("failed to clear registers: %w", err)
	}
	err = db.Exec(ctx, "INSERT INTO clickv.registers (address, value) SELECT number AS address, 0 AS value FROM numbers(1 + 31)")
	if err != nil {
		return fmt.Errorf("failed to zero registers: %w", err)
	}

	// Reset RAM
	err = db.Exec(ctx, "TRUNCATE TABLE clickv.memory SYNC")
	if err != nil {
		return fmt.Errorf("failed to clear memory: %w", err)
	}
	err = db.Exec(ctx, "INSERT INTO clickv.memory (address, value) SELECT number AS address, 0 AS value FROM numbers(?)", MEM_SIZE)
	if err != nil {
		return fmt.Errorf("failed to zero memory: %w", err)
	}

	return nil
}

func loadProgram(ctx context.Context, db driver.Conn, reversed bool, programHex string) error {
	if reversed {
		programHex = reverseInstructions(programHex)
	}

	err := db.Exec(ctx, "INSERT INTO clickv.load_program (hex) VALUES (?)", programHex)
	if err != nil {
		return fmt.Errorf("failed to loadd program: %w", err)
	}

	return nil
}

func clockCPU(ctx context.Context, db driver.Conn, instructionName string) error {
	start := time.Now()
	err := db.Exec(ctx, "INSERT INTO clickv.clock (_) VALUES ()")
	if err != nil {
		return fmt.Errorf("failed to clock CPU: %w", err)
	}
	dur := time.Since(start)
	instructionPerf[instructionName] = dur

	return nil
}

func getPC(ctx context.Context, db driver.Conn) (uint32, error) {
	var pc uint32
	err := db.QueryRow(ctx, "SELECT value FROM clickv.pc").Scan(&pc)
	if err != nil {
		return 0, fmt.Errorf("failed to get PC: %w", err)
	}

	return pc, nil
}

func getRegister(ctx context.Context, db driver.Conn, reg uint8) (uint32, error) {
	var value uint32
	err := db.QueryRow(ctx, "SELECT value FROM clickv.registers WHERE address = ?", reg).Scan(&value)
	if err != nil {
		return 0, fmt.Errorf("failed to get register: %w", err)
	}

	return value, nil
}

func setRegister(ctx context.Context, db driver.Conn, reg uint8, value uint32) error {
	err := db.Exec(ctx, "INSERT INTO clickv.registers (address, value) VALUES (?, ?)", reg, value)
	if err != nil {
		return fmt.Errorf("failed to set register: %w", err)
	}

	return nil
}

func getMemory(ctx context.Context, db driver.Conn, addr uint32) (byte, error) {
	var value byte
	err := db.QueryRow(ctx, "SELECT value FROM clickv.memory WHERE address = ?", addr).Scan(&value)
	if err != nil {
		return 0, fmt.Errorf("failed to get memory: %w", err)
	}

	return value, nil
}

func setMemory(ctx context.Context, db driver.Conn, addr uint32, value byte) error {
	err := db.Exec(ctx, "INSERT INTO clickv.memory (address, value) VALUES (?, ?)", addr, value)
	if err != nil {
		return fmt.Errorf("failed to set memory: %w", err)
	}

	return nil
}

func setMemoryRange(ctx context.Context, db driver.Conn, addr uint32, values []byte) error {
	for i, value := range values {
		err := setMemory(ctx, db, addr+uint32(i), value)
		if err != nil {
			return fmt.Errorf("failed to set memory range: %w", err)
		}
	}

	return nil
}

// Rust compiler is correct, but the online compiler doesn't output in the correct order
func reverseInstructions(programHex string) string {
	// Reverse the program
	reversed := make([]byte, len(programHex))
	for i := 0; i < len(programHex); i += 2 {
		reversed[len(programHex)-i-2] = programHex[i]
		reversed[len(programHex)-i-1] = programHex[i+1]
	}

	return string(reversed)
}

// regAddr returns the register address for a given name
func regAddr(name string) uint8 {
	switch name {
	case "zero":
		return 0
	case "ra":
		return 1
	case "sp":
		return 2
	case "gp":
		return 3
	case "tp":
		return 4
	case "t0":
		return 5
	case "t1":
		return 6
	case "t2":
		return 7
	case "s0":
		return 8
	case "fp":
		return 8
	case "s1":
		return 9
	case "a0":
		return 10
	case "a1":
		return 11
	case "a2":
		return 12
	case "a3":
		return 13
	case "a4":
		return 14
	case "a5":
		return 15
	case "a6":
		return 16
	case "a7":
		return 17
	case "s2":
		return 18
	case "s3":
		return 19
	case "s4":
		return 20
	case "s5":
		return 21
	case "s6":
		return 22
	case "s7":
		return 23
	case "s8":
		return 24
	case "s9":
		return 25
	case "s10":
		return 26
	case "s11":
		return 27
	case "t3":
		return 28
	case "t4":
		return 29
	case "t5":
		return 30
	case "t6":
		return 31
	default:
		return 0
	}
}

func failErr(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func assertPCEquals(t *testing.T, ctx context.Context, db driver.Conn, expected uint32) {
	pc, err := getPC(ctx, db)
	failErr(t, err)

	if pc != expected {
		t.Fatalf("expected PC to be %d, got %d", expected, pc)
	}
}

func assertPCIncremented(t *testing.T, ctx context.Context, db driver.Conn, previousPC uint32) {
	assertPCEquals(t, ctx, db, previousPC+4)
}

func assertRegisterEquals(t *testing.T, ctx context.Context, db driver.Conn, reg uint8, expected uint32) {
	value, err := getRegister(ctx, db, reg)
	failErr(t, err)

	if value != expected {
		t.Fatalf("expected register %d to be %d, got %d", reg, expected, value)
	}
}

func subUInt32(a, b uint32) uint32 {
	return a + (^b + 1)
}

func TestInstruction_add(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// add t2, t0, t1
	err = loadProgram(ctx, db, true, "006283b3")
	failErr(t, err)

	setRegister(ctx, db, regAddr("t0"), 64)
	setRegister(ctx, db, regAddr("t1"), 128)

	err = clockCPU(ctx, db, "add")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), 64+128)
}

func TestInstruction_add_negative(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// add t2, t0, t1
	err = loadProgram(ctx, db, true, "006283b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 0xFFFFFF80 // -128
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "add_negative")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), a+b)
}

func TestInstruction_sub(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sub t2, t0, t1
	err = loadProgram(ctx, db, true, "406283b3")
	failErr(t, err)

	var a uint32 = 128
	var b uint32 = 64
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "sub")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), a-b)
}

func TestInstruction_sub_negative(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sub t2, t0, t1
	err = loadProgram(ctx, db, true, "406283b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 0xFFFFFF80 // -128
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "sub_negative")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), subUInt32(a, b))
}

func TestInstruction_mul(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// mul t2, t0, t1
	err = loadProgram(ctx, db, true, "026283B3")
	failErr(t, err)

	setRegister(ctx, db, regAddr("t0"), 3)
	setRegister(ctx, db, regAddr("t1"), 5)

	err = clockCPU(ctx, db, "mul")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), 15)
}

func TestInstruction_div(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// div t2, t0, t1

	err = loadProgram(ctx, db, true, "0262c3b3")
	failErr(t, err)

	setRegister(ctx, db, regAddr("t0"), 9)
	setRegister(ctx, db, regAddr("t1"), 3)

	err = clockCPU(ctx, db, "div")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), 3)
}

func TestInstruction_rem(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// rem t2, t0, t1
	err = loadProgram(ctx, db, true, "0262e3b3")
	failErr(t, err)

	setRegister(ctx, db, regAddr("t0"), 8)
	setRegister(ctx, db, regAddr("t1"), 3)

	err = clockCPU(ctx, db, "rem")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), 2)
}

func TestInstruction_xor(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// xor t2, t0, t1
	err = loadProgram(ctx, db, true, "0062c3b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 128
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "xor")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), a^b)
}

func TestInstruction_or(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// or t2, t0, t1
	err = loadProgram(ctx, db, true, "0062e3b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 128
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "or")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), a|b)
}

func TestInstruction_and(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// and t2, t0, t1
	err = loadProgram(ctx, db, true, "0062f3b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 128
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "and")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), a&b)
}

func TestInstruction_sll(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sll t2, t0, t1
	err = loadProgram(ctx, db, true, "006293b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 3
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "sll")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), a<<b)
}

func TestInstruction_srl(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// srl t2, t0, t1
	err = loadProgram(ctx, db, true, "0062d3b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 3
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "srl")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), a>>b)
}

func TestInstruction_sra(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sra t2, t0, t1
	err = loadProgram(ctx, db, true, "4062d3b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 3
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "sra")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t2"), uint32(int32(a)>>b))
}

func TestInstruction_slt(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// slt t2, t0, t1
	err = loadProgram(ctx, db, true, "0062a3b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 128
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "slt")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	lt := int32(a) < int32(b)
	result := uint32(0)
	if lt {
		result = 1
	}
	assertRegisterEquals(t, ctx, db, regAddr("t2"), result)
}

func TestInstruction_sltu(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sltu t2, t0, t1
	err = loadProgram(ctx, db, true, "0062b3b3")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 128
	setRegister(ctx, db, regAddr("t0"), a)
	setRegister(ctx, db, regAddr("t1"), b)

	err = clockCPU(ctx, db, "sltu")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	ltu := a < b
	result := uint32(0)
	if ltu {
		result = 1
	}
	assertRegisterEquals(t, ctx, db, regAddr("t2"), result)
}

func TestInstruction_addi(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// addi t1, t0, 10
	err = loadProgram(ctx, db, true, "00a28313")
	failErr(t, err)

	var a uint32 = 410
	var imm uint32 = 10
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "addi")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), a+imm)
}

func TestInstruction_addi_negative(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// addi t1, t0, -10
	err = loadProgram(ctx, db, true, "ff628313")
	failErr(t, err)

	var a uint32 = 430
	var imm int32 = -10
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "addi_negative")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), uint32(int32(a)+imm))
}

func TestInstruction_xori(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// xori t1, t0, 32
	err = loadProgram(ctx, db, true, "0202c313")
	failErr(t, err)

	var a uint32 = 64
	var imm uint32 = 32
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "xori")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), a^imm)
}

func TestInstruction_ori(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// ori t1, t0, 16
	err = loadProgram(ctx, db, true, "0102e313")
	failErr(t, err)

	var a uint32 = 64
	var imm uint32 = 16
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "ori")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), a|imm)
}

func TestInstruction_andi(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// andi t1, t0, 8
	err = loadProgram(ctx, db, true, "0202f313")
	failErr(t, err)

	var a uint32 = 64
	var imm uint32 = 8
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "andi")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), a&imm)
}

func TestInstruction_slli(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// slli t1, t0, 4
	err = loadProgram(ctx, db, true, "00429313")
	failErr(t, err)

	var a uint32 = 64
	var imm uint32 = 4
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "slli")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), a<<imm)
}

func TestInstruction_srli(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// srli t1, t0, 2
	err = loadProgram(ctx, db, true, "0022d313")
	failErr(t, err)

	var a uint32 = 64
	var imm uint32 = 2
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "srli")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), a>>imm)
}

func TestInstruction_srai(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// srai t1, t0, 3
	err = loadProgram(ctx, db, true, "4032d313")
	failErr(t, err)

	var a uint32 = 64
	var b uint32 = 3
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "srai")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), uint32(int32(a)>>b))
}

func TestInstruction_slti(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// slti t1, t0, -50
	err = loadProgram(ctx, db, true, "fce2a313")
	failErr(t, err)

	var a uint32 = 100
	var imm int32 = -50
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "slti")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	lt := int32(a) < imm
	result := uint32(0)
	if lt {
		result = 1
	}
	assertRegisterEquals(t, ctx, db, regAddr("t1"), result)
}

func TestInstruction_sltiu(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sltiu t1, t0, 50
	err = loadProgram(ctx, db, true, "0322b313")
	failErr(t, err)

	var a uint32 = 100
	var imm uint32 = 50
	setRegister(ctx, db, regAddr("t0"), a)

	err = clockCPU(ctx, db, "sltiu")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	ltu := a < imm
	result := uint32(0)
	if ltu {
		result = 1
	}
	assertRegisterEquals(t, ctx, db, regAddr("t1"), result)
}

func TestInstruction_lui(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// lui t0, 0xBA
	err = loadProgram(ctx, db, true, "000ba2b7")
	failErr(t, err)

	err = clockCPU(ctx, db, "lui")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)

	var imm uint32 = 0xBA
	assertRegisterEquals(t, ctx, db, regAddr("t0"), uint32(int32(imm<<12)))
}

func TestInstruction_auipc(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// auipc t0, 0xBA
	err = loadProgram(ctx, db, true, "000ba297")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	err = clockCPU(ctx, db, "auipc")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, pcBeforeClock)

	var imm int32 = 0xBA
	expected := pcBeforeClock + uint32(imm<<12)
	assertRegisterEquals(t, ctx, db, regAddr("t0"), expected)
}

func TestInstruction_lb(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// lb t1, 2(t0)
	err = loadProgram(ctx, db, true, "00228303")
	failErr(t, err)

	var addr uint32 = 2
	var value byte = 0xBA
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setMemory(ctx, db, ROM_SIZE+addr, value)
	failErr(t, err)

	err = clockCPU(ctx, db, "lb")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), uint32(int8(value)))
}

func TestInstruction_lh(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// lh t1, 4(t0)
	err = loadProgram(ctx, db, true, "00429303")
	failErr(t, err)

	var addr uint32 = 4
	var value uint16 = 0xBEEF
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setMemoryRange(ctx, db, ROM_SIZE+addr, []byte{byte(value), byte(value >> 8)})
	failErr(t, err)

	err = clockCPU(ctx, db, "lh")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), uint32(int16(value)))
}

func TestInstruction_lw(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// lw t1, 8(t0)
	err = loadProgram(ctx, db, true, "0082a303")
	failErr(t, err)

	var addr uint32 = 8
	var value uint32 = 0x12345678
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setMemoryRange(ctx, db, ROM_SIZE+addr, []byte{byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24)})
	failErr(t, err)

	err = clockCPU(ctx, db, "lw")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), value)
}

func TestInstruction_lbu(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// lbu t1, 10(t0)
	err = loadProgram(ctx, db, true, "00a2c303")
	failErr(t, err)

	var addr uint32 = 10
	var value byte = 0xFF
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setMemory(ctx, db, ROM_SIZE+addr, value)
	failErr(t, err)

	err = clockCPU(ctx, db, "lbu")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), uint32(value))
}

func TestInstruction_lhu(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// lhu t1, 12(t0)
	err = loadProgram(ctx, db, true, "00c2d303")
	failErr(t, err)

	var addr uint32 = 12
	var value uint16 = 0xABCD
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setMemoryRange(ctx, db, ROM_SIZE+addr, []byte{byte(value), byte(value >> 8)})
	failErr(t, err)

	err = clockCPU(ctx, db, "lhu")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)
	assertRegisterEquals(t, ctx, db, regAddr("t1"), uint32(value))
}

func TestInstruction_sb(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sb t1, 16(t0)
	err = loadProgram(ctx, db, true, "00628823")
	failErr(t, err)

	var addr uint32 = 16
	var value byte = 0xAB
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), uint32(value))
	failErr(t, err)

	err = clockCPU(ctx, db, "sb")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)

	memoryValue, err := getMemory(ctx, db, ROM_SIZE+addr)
	failErr(t, err)
	if memoryValue != value {
		t.Errorf("Expected memory value at address %d to be %d, got %d", ROM_SIZE+addr, value, memoryValue)
	}
}

func TestInstruction_sh(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sh t1, 20(t0)
	err = loadProgram(ctx, db, true, "00629a23")
	failErr(t, err)

	var addr uint32 = 20
	var value uint16 = 0xFEED
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), uint32(value))
	failErr(t, err)

	err = clockCPU(ctx, db, "sh")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)

	memoryValue1, err := getMemory(ctx, db, ROM_SIZE+addr)
	failErr(t, err)
	memoryValue2, err := getMemory(ctx, db, ROM_SIZE+addr+1)
	failErr(t, err)

	result := (uint16(memoryValue2) << 8) | uint16(memoryValue1)
	if result != value {
		t.Errorf("Expected memory value: %d, Got: %d", value, result)
	}
}

func TestInstruction_sw(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// sw t1, 24(t0)
	err = loadProgram(ctx, db, true, "0062ac23")
	failErr(t, err)

	var addr uint32 = 24
	var value uint32 = 0xABCDEF12
	err = setRegister(ctx, db, regAddr("t0"), ROM_SIZE)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), value)
	failErr(t, err)

	err = clockCPU(ctx, db, "sw")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)

	memoryValue1, err := getMemory(ctx, db, ROM_SIZE+addr)
	failErr(t, err)
	memoryValue2, err := getMemory(ctx, db, ROM_SIZE+addr+1)
	failErr(t, err)
	memoryValue3, err := getMemory(ctx, db, ROM_SIZE+addr+2)
	failErr(t, err)
	memoryValue4, err := getMemory(ctx, db, ROM_SIZE+addr+3)
	failErr(t, err)

	result := (uint32(memoryValue4) << 24) | (uint32(memoryValue3) << 16) | (uint32(memoryValue2) << 8) | uint32(memoryValue1)
	if result != value {
		t.Errorf("Expected memory value: %d, Got: %d", value, result)
	}
}

func TestInstruction_jal(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// jal t0, 0x100
	err = loadProgram(ctx, db, true, "100002ef")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	err = clockCPU(ctx, db, "jal")
	failErr(t, err)

	expectedRegister := pcBeforeClock + 4
	assertRegisterEquals(t, ctx, db, regAddr("t0"), expectedRegister)

	expectedPC := pcBeforeClock + 0x100
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_j(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// j 0x100
	err = loadProgram(ctx, db, true, "1000006f")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	err = clockCPU(ctx, db, "j")
	failErr(t, err)

	// No register should be updated
	assertRegisterEquals(t, ctx, db, regAddr("zero"), 0)

	// PC should be updated to the jump target
	expectedPC := pcBeforeClock + 0x100
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_jalr(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// jalr t0, t1, 0x10
	err = loadProgram(ctx, db, true, "010302e7")
	failErr(t, err)

	var imm int32 = 0x10
	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t1"), uint32(int32(pcBeforeClock)+imm))
	failErr(t, err)

	err = clockCPU(ctx, db, "jalr")
	failErr(t, err)

	expectedRegister := pcBeforeClock + 4
	assertRegisterEquals(t, ctx, db, regAddr("t0"), expectedRegister)

	rs1, err := getRegister(ctx, db, regAddr("t1"))
	failErr(t, err)

	expectedPC := uint32(int32(rs1) + int32(imm))
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_jr(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// jr t1
	err = loadProgram(ctx, db, true, "00030067")
	failErr(t, err)

	var jumpAddress uint32 = 0x100
	err = setRegister(ctx, db, regAddr("t1"), jumpAddress)
	failErr(t, err)

	err = clockCPU(ctx, db, "jr")
	failErr(t, err)

	// PC should be updated to the value in t1
	assertPCEquals(t, ctx, db, jumpAddress)
}

func TestInstruction_beq_true(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// beq t0, t1, 0x20
	err = loadProgram(ctx, db, true, "02628063")
	failErr(t, err)

	var imm int32 = 0x20
	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 2)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 2)
	failErr(t, err)

	err = clockCPU(ctx, db, "beq_true")
	failErr(t, err)

	expectedPC := uint32(int32(pcBeforeClock) + imm)
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_beq_false(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// beq t0, t1, 0x20
	err = loadProgram(ctx, db, true, "02628063")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 1)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 3)
	failErr(t, err)

	err = clockCPU(ctx, db, "beq_false")
	failErr(t, err)

	expectedPC := pcBeforeClock + 4
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bne_true(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bne t0, t1, 0x20
	err = loadProgram(ctx, db, true, "02629063")
	failErr(t, err)

	var imm int32 = 0x20
	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 1)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 3)
	failErr(t, err)

	err = clockCPU(ctx, db, "bne_true")
	failErr(t, err)

	expectedPC := uint32(int32(pcBeforeClock) + imm)
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bne_false(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bne t0, t1, 0x20
	err = loadProgram(ctx, db, true, "02629063")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 2)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 2)
	failErr(t, err)

	err = clockCPU(ctx, db, "bne_false")
	failErr(t, err)

	expectedPC := pcBeforeClock + 4
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_blt_true(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// blt t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262c063")
	failErr(t, err)

	var imm int32 = 0x20
	var pcBeforeClock uint32 = 0
	var a = uint32(0xFFFFFF9C) // -100
	var b = uint32(10)
	err = setRegister(ctx, db, regAddr("t0"), a)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), b)
	failErr(t, err)

	err = clockCPU(ctx, db, "blt_true")
	failErr(t, err)

	expectedPC := uint32(int32(pcBeforeClock) + imm)
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_blt_false(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// blt t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262c063")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	var a = uint32(0xFFFFFF9C) // -100
	var b = uint32(10)
	err = setRegister(ctx, db, regAddr("t0"), b)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), a)
	failErr(t, err)

	err = clockCPU(ctx, db, "blt_false")
	failErr(t, err)

	expectedPC := pcBeforeClock + 4
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bge_true(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bge t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262d063")
	failErr(t, err)

	var imm int32 = 0x20
	var pcBeforeClock uint32 = 0
	var a = uint32(0xFFFFFF9C) // -100
	var b = uint32(10)
	err = setRegister(ctx, db, regAddr("t0"), b)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), a)
	failErr(t, err)

	err = clockCPU(ctx, db, "bge_true")
	failErr(t, err)

	expectedPC := uint32(int32(pcBeforeClock) + imm)
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bge_false(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bge t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262d063")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	var a = uint32(0xFFFFFF9C) // -100
	var b = uint32(10)
	err = setRegister(ctx, db, regAddr("t0"), a)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), b)
	failErr(t, err)

	err = clockCPU(ctx, db, "bge_false")
	failErr(t, err)

	expectedPC := pcBeforeClock + 4
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bltu_true(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bltu t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262e063")
	failErr(t, err)

	var imm int32 = 0x20
	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 1)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 3)
	failErr(t, err)

	err = clockCPU(ctx, db, "bltu_true")
	failErr(t, err)

	expectedPC := uint32(int32(pcBeforeClock) + imm)
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bltu_false(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bltu t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262e063")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 3)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 1)
	failErr(t, err)

	err = clockCPU(ctx, db, "bltu_false")
	failErr(t, err)

	expectedPC := pcBeforeClock + 4
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bgeu_true(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bgeu t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262f063")
	failErr(t, err)

	var imm int32 = 0x20
	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 3)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 1)
	failErr(t, err)

	err = clockCPU(ctx, db, "bgeu_true")
	failErr(t, err)

	expectedPC := uint32(int32(pcBeforeClock) + imm)
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_bgeu_false(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// bgeu t0, t1, 0x20
	err = loadProgram(ctx, db, true, "0262f063")
	failErr(t, err)

	var pcBeforeClock uint32 = 0
	err = setRegister(ctx, db, regAddr("t0"), 1)
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("t1"), 3)
	failErr(t, err)

	err = clockCPU(ctx, db, "bgeu_false")
	failErr(t, err)

	expectedPC := pcBeforeClock + 4
	assertPCEquals(t, ctx, db, expectedPC)
}

func TestInstruction_ecall_print(t *testing.T) {
	ctx := context.Background()
	db, err := getDB()
	failErr(t, err)

	err = resetCPU(ctx, db)
	failErr(t, err)

	// Clear print table
	err = db.Exec(ctx, "TRUNCATE TABLE clickv.print")
	failErr(t, err)

	// ecall
	err = loadProgram(ctx, db, true, "00000073")
	failErr(t, err)

	// Set message inside RAM
	msg := "ClickHouse!"
	msgLen := len(msg)
	err = setMemoryRange(ctx, db, ROM_SIZE, []byte(msg))
	failErr(t, err)

	err = setRegister(ctx, db, regAddr("a0"), ROM_SIZE) // address of msg
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("a1"), uint32(msgLen)) // length of msg
	failErr(t, err)
	err = setRegister(ctx, db, regAddr("a7"), uint32(0x1)) // print syscall
	failErr(t, err)

	err = clockCPU(ctx, db, "ecall_print")
	failErr(t, err)

	assertPCIncremented(t, ctx, db, 0)

	// Check if the message was printed
	var outputMsg string
	err = db.QueryRow(ctx, "SELECT message FROM clickv.print LIMIT 1").Scan(&outputMsg)
	failErr(t, err)

}

func TestMain(m *testing.M) {
	status := m.Run()
	PrintInstructionPerf()
	os.Exit(status)
}

// Fashionably violate the single responsibility principle and use unit tests as a
// performance test for each instruction.
// Run all tests to get an idea of the performance of each instruction.
func PrintInstructionPerf() {
	var totalDuration float64
	var entries []struct {
		instruction string
		duration    time.Duration
	}
	for instruction, duration := range instructionPerf {
		entries = append(entries, struct {
			instruction string
			duration    time.Duration
		}{instruction, duration})
		totalDuration += duration.Seconds()
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].duration > entries[j].duration
	})

	fmt.Println("Instruction performance:")
	for _, entry := range entries {
		fmt.Printf("%s: %s\n", entry.instruction, entry.duration)
	}

	fmt.Printf("%d instructions ran at ~%.2fhz\n", len(entries), float64(len(entries))/totalDuration)
}
