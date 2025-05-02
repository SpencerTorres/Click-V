------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Click-V: A RISC-V 32i Emulator in ClickHouse
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------

---------------------------------------
-- Known quirks / bugs
---------------------------------------

-- 1. Redis engine does a full SCAN with EVERY QUERY! Documentation says it will translate to MGETs, but this is broken with allow_experimental__analyzer=1.
--    I have confirmed this with a custom Redis server. Even on old versions, it seems like it was still doing multiple MGETs instead of one MGET.
--    Range queries don't work either, it just scans the whole database and then filters it. I don't know how the dragonfly team didn't see this when writing their blog post.
--    Redis is still fast, but this is a huge hit to clock speed performance.
--
--    Here's my investigation:
--      Example query: SELECT value FROM clickv.memory WHERE address = 0x1;
--    KVStorageUtils.cpp (getFilterKeys -> traverseASTFilter) (used by Redis engine) will traverse the AST to find the primary key.
--    The issue is that Redis engine defines the `primary_key` as "address" while the AST will use `ASTIdentifier->name()`, which returns `__table1.address`.
--    There's no way around this in SQL. I recompiled ClickHouse with a fix (changing `ASTIdentifier->name()` to `ASTIdentifier->shortName()` ), but it only works
--    for direct equals. (WHERE address = 0x0), and if you want to use more than two expressions then you need to do (WHERE (address = 0x0 OR address = 0x1) OR (address = 0x2)).
--    You cannot use (WHERE address IN (0x0, 0x1, 0x2)) because it will not be able to match the primary key. Someone more skilled than I will need to solve this.
--
-- 2. ClickOS syscalls use an executable UDF to operate outside the emulator. Some of these functions are expensive, so I have added layers of materialized views to reduce the
--    number of times these complex queries are evaluated. I've done the same for all instructions. !!!HOWEVER!!!, it seems like the UDF keeps being evaluated???
--    even when it's so deeply nested in matviews on the ecall instruction??? WHY? I don't know, but I changed the input from a constant (10, for example) to just passing
---   `syscall_n`. This seems to have stopped it from being randomly evaluated per-clock with garbage data. Is this happening for all functions/matviews and I don't know it?
--    ClickHouse is really fast if that's the case...
--
---------------------------------------

-- Setup database. Easy for resetting.
SET allow_experimental_analyzer = 1; -- Required for some of these queries, should be on by default
SET allow_experimental_live_view = 1; -- for screen

DROP DATABASE IF EXISTS clickv;
CREATE DATABASE IF NOT EXISTS clickv;

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Helper Functions
------------------------------------------------------------------------------------------------------------------------------------------------------------

-- "Get Instruction" decode function IF NOT EXISTSs
DROP FUNCTION IF EXISTS getins_opcode;
CREATE FUNCTION IF NOT EXISTS getins_opcode AS (ins) -> bitAnd(ins, 0b01111111); -- opcode is bits 0 - 6
DROP FUNCTION IF EXISTS getins_rd;
CREATE FUNCTION IF NOT EXISTS getins_rd AS (ins) -> bitAnd(bitShiftRight(ins, 7), 0b00011111); -- rd is bits 7 - 11 (for R/I/U/J-types, is also imm[4:0] for S-type)
DROP FUNCTION IF EXISTS getins_funct3;
CREATE FUNCTION IF NOT EXISTS getins_funct3 AS (ins) -> bitAnd(bitShiftRight(ins, 12), 0b00000111); -- funct3 is bits 12 - 14 (for R/I/S-types)
DROP FUNCTION IF EXISTS getins_rs1;
CREATE FUNCTION IF NOT EXISTS getins_rs1 AS (ins) -> bitAnd(bitShiftRight(ins, 15), 0b00011111); -- rs1 is bits 15 - 19 (for R/I/S-types)
DROP FUNCTION IF EXISTS getins_rs2;
CREATE FUNCTION IF NOT EXISTS getins_rs2 AS (ins) -> bitAnd(bitShiftRight(ins, 20), 0b00011111); -- rs2 is bits 20 - 24 (for R/S-types)
DROP FUNCTION IF EXISTS getins_r_funct7;
CREATE FUNCTION IF NOT EXISTS getins_r_funct7 AS (ins) -> bitAnd(bitShiftRight(ins, 25), 0b01111111); -- funct7 is bits 25 - 31 (for R-type), can also be imm[11:5] for S-type or imm[12|10:5] for B-type
DROP FUNCTION IF EXISTS sign_extend;
CREATE FUNCTION IF NOT EXISTS sign_extend AS (value, bits) -> bitShiftRight(toInt32(bitShiftLeft(toUInt32(value), 32-bits)), 32-bits);
DROP FUNCTION IF EXISTS getins_i_imm;
CREATE FUNCTION IF NOT EXISTS getins_i_imm AS (ins) -> sign_extend(bitShiftRight(bitAnd(ins, 0xFFF00000), 20), 12); -- imm is bits 20 - 31 (for I-type)
DROP FUNCTION IF EXISTS getins_i_imm_lower;
CREATE FUNCTION IF NOT EXISTS getins_i_imm_lower AS (imm) -> bitAnd(imm, 31); -- lower imm[0:4] (for I-type, SLLI style instructions)
DROP FUNCTION IF EXISTS getins_i_imm_upper;
CREATE FUNCTION IF NOT EXISTS getins_i_imm_upper AS (imm) -> bitAnd(bitShiftRight(imm, 5), 0x7F); -- upper imm = imm[5:11] (for I-type, SLLI style instructions)
DROP FUNCTION IF EXISTS getins_s_imm;
CREATE FUNCTION IF NOT EXISTS getins_s_imm AS (ins) -> sign_extend(bitOr(bitShiftLeft(getins_r_funct7(ins), 5), getins_rd(ins)), 12);
DROP FUNCTION IF EXISTS getins_u_imm;
CREATE FUNCTION IF NOT EXISTS getins_u_imm AS (ins) -> sign_extend(bitShiftRight(bitAnd(ins, 0xFFFFF000), 12), 12); -- imm is bits 12 - 31 (for U-type)
DROP FUNCTION IF EXISTS getins_branch_imm;
CREATE FUNCTION IF NOT EXISTS getins_branch_imm AS (ins) -> if(bitShiftRight(bitShiftLeft(bitAnd(toInt32(getins_r_funct7(ins)), 0b01000000), 6), 12) != 0, bitOr(bitOr(bitOr(bitOr(bitOr(bitShiftLeft(bitAnd(toInt32(getins_r_funct7(ins)), 0b01000000), 6), bitShiftLeft(bitAnd(toInt32(getins_rd(ins)), 0b00000001), 11)), bitShiftLeft(bitAnd(toInt32(getins_r_funct7(ins)), 0b00111111), 5)), bitAnd(toInt32(getins_rd(ins)), 0b00011110)), 0), 0xffffe000), bitOr(bitOr(bitOr(bitOr(bitShiftLeft(bitAnd(toInt32(getins_r_funct7(ins)), 0b01000000), 6), bitShiftLeft(bitAnd(toInt32(getins_rd(ins)), 0b00000001), 11)), bitShiftLeft(bitAnd(toInt32(getins_r_funct7(ins)), 0b00111111), 5)), bitAnd(toInt32(getins_rd(ins)), 0b00011110)), 0));
DROP FUNCTION IF EXISTS getins_jal_imm;
CREATE FUNCTION IF NOT EXISTS getins_jal_imm AS (ins) -> if(bitShiftRight(bitShiftRight(bitAnd(toInt32(ins), toInt32(0x80000000)), 11), 20) != 0, bitOr(bitOr(bitOr(bitOr(bitOr(bitShiftRight(bitAnd(toInt32(ins), toInt32(0x80000000)), 11), bitAnd(toInt32(ins), toInt32(0b00000000000011111111000000000000))), bitShiftRight(bitAnd(toInt32(ins), toInt32(0b00000000000100000000000000000000)), 9)), bitShiftRight(bitAnd(toInt32(ins), toInt32(0b01111111111000000000000000000000)), 20)), toInt32(0)), toInt32(0xfff00000)), bitOr(bitOr(bitOr(bitOr(bitShiftRight(bitAnd(toInt32(ins), toInt32(0x80000000)), 11), bitAnd(toInt32(ins), toInt32(0b00000000000011111111000000000000))), bitShiftRight(bitAnd(toInt32(ins), toInt32(0b00000000000100000000000000000000)), 9)), bitShiftRight(bitAnd(toInt32(ins), toInt32(0b01111111111000000000000000000000)), 20)), toInt32(0)));

DROP FUNCTION IF EXISTS uint32_to_byte_array;
CREATE FUNCTION IF NOT EXISTS uint32_to_byte_array AS (value) -> ([toUInt8(value), toUInt8(bitShiftRight(value, 8)), toUInt8(bitShiftRight(value, 16)), toUInt8(bitShiftRight(value, 24))]);

DROP FUNCTION IF EXISTS byte_array_to_uint32;
CREATE FUNCTION IF NOT EXISTS byte_array_to_uint32 AS (arr) -> (
	bitOr(
		bitShiftLeft(toUInt32(arrayElement(arr, 4)), 24),
		bitOr(bitShiftLeft(toUInt32(arrayElement(arr, 3)), 16),
		bitOr(bitShiftLeft(toUInt32(arrayElement(arr, 2)), 8),
		toUInt32(arrayElement(arr, 1)))))
);

DROP FUNCTION IF EXISTS get_instruction_name;
CREATE FUNCTION IF NOT EXISTS get_instruction_name AS (ins) -> multiIf(
	-- R-type instructions
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x0 AND getins_r_funct7(ins) = 0x00, 'add',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x0 AND getins_r_funct7(ins) = 0x20, 'sub',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x4, 'xor',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x6, 'or',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x7, 'and',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x1, 'sll',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x5 AND getins_r_funct7(ins) = 0x00, 'srl',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x5 AND getins_r_funct7(ins) = 0x20, 'sra',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x2, 'slt',
	getins_opcode(ins) = 0x33 AND getins_funct3(ins) = 0x3, 'sltu',
	-- I-type instructions
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x0, 'addi',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x4, 'xori',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x6, 'ori',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x7, 'andi',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x1 AND getins_i_imm_upper(getins_i_imm(ins)) = 0x00, 'slli',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x5 AND getins_i_imm_upper(getins_i_imm(ins)) = 0x00, 'srli',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x5 AND getins_i_imm_upper(getins_i_imm(ins)) = 0x20, 'srai',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x2, 'slti',
	getins_opcode(ins) = 0x13 AND getins_funct3(ins) = 0x3, 'sltiu',
	-- U-type instructions
	getins_opcode(ins) = 0x37, 'lui',
	getins_opcode(ins) = 0x17, 'auipc',

	-- Load instructions
	getins_opcode(ins) = 0x03 AND getins_funct3(ins) = 0x0, 'lb',
	getins_opcode(ins) = 0x03 AND getins_funct3(ins) = 0x1, 'lh',
	getins_opcode(ins) = 0x03 AND getins_funct3(ins) = 0x2, 'lw',
	getins_opcode(ins) = 0x03 AND getins_funct3(ins) = 0x3, 'lbu',
	getins_opcode(ins) = 0x03 AND getins_funct3(ins) = 0x4, 'lhu',
	-- Store instructions
	getins_opcode(ins) = 0x23 AND getins_funct3(ins) = 0x0, 'sb',
	getins_opcode(ins) = 0x23 AND getins_funct3(ins) = 0x1, 'sh',
	getins_opcode(ins) = 0x23 AND getins_funct3(ins) = 0x2, 'sw',
	-- Jump and Link instructions
	getins_opcode(ins) = 0x6F, 'jal',
	getins_opcode(ins) = 0x67, 'jalr',
	-- Branch instructions
	getins_opcode(ins) = 0x63 AND getins_funct3(ins) = 0x0, 'beq',
	getins_opcode(ins) = 0x63 AND getins_funct3(ins) = 0x1, 'bne',
	getins_opcode(ins) = 0x63 AND getins_funct3(ins) = 0x4, 'blt',
	getins_opcode(ins) = 0x63 AND getins_funct3(ins) = 0x5, 'bge',
	getins_opcode(ins) = 0x63 AND getins_funct3(ins) = 0x6, 'bltu',
	getins_opcode(ins) = 0x63 AND getins_funct3(ins) = 0x7, 'bgeu',
	-- System instructions
	getins_opcode(ins) = 0x73 AND getins_funct3(ins) = 0x0 AND getins_i_imm(ins) = 0x0, 'ecall',
	getins_opcode(ins) = 0x73 AND getins_funct3(ins) = 0x0 AND getins_i_imm(ins) = 0x1, 'ebreak',
	'Unknown'
);

DROP FUNCTION IF EXISTS get_register_name;
CREATE FUNCTION IF NOT EXISTS get_register_name AS (reg) -> multiIf(
	reg = 0x0, 'zero',  -- zero register
	reg = 0x1, 'ra',    -- return address
	reg = 0x2, 'sp',    -- stack pointer
	reg = 0x3, 'gp',    -- global pointer
	reg = 0x4, 'tp',    -- thread pointer
	reg = 0x5, 't0',    -- temporary register 0
	reg = 0x6, 't1',    -- temporary register 1
	reg = 0x7, 't2',    -- temporary register 2
	reg = 0x8, 's0',    -- saved register / frame pointer
	reg = 0x9, 's1',    -- saved register
	reg = 0xa, 'a0',    -- argument register / return value
	reg = 0xb, 'a1',    -- argument register / return value
	reg = 0xc, 'a2',    -- argument register
	reg = 0xd, 'a3',    -- argument register
	reg = 0xe, 'a4',    -- argument register
	reg = 0xf, 'a5',    -- argument register
	reg = 0x10, 'a6',   -- argument register
	reg = 0x11, 'a7',   -- argument register
	reg = 0x12, 's2',   -- saved register
	reg = 0x13, 's3',   -- saved register
	reg = 0x14, 's4',   -- saved register
	reg = 0x15, 's5',   -- saved register
	reg = 0x16, 's6',   -- saved register
	reg = 0x17, 's7',   -- saved register
	reg = 0x18, 's8',   -- saved register
	reg = 0x19, 's9',   -- saved register
	reg = 0x1a, 's10',  -- saved register
	reg = 0x1b, 's11',  -- saved register
	reg = 0x1c, 't3',   -- temporary register
	reg = 0x1d, 't4',   -- temporary register
	reg = 0x1e, 't5',   -- temporary register
	reg = 0x1f, 't6',   -- temporary register
	'Unknown'
);

DROP FUNCTION IF EXISTS uses_rd;
CREATE FUNCTION IF NOT EXISTS uses_rd AS (ins) -> multiIf(
	getins_opcode(ins) = 0x33, 1,  -- R-type
	getins_opcode(ins) = 0x13, 1,  -- I-type
	getins_opcode(ins) = 0x37 OR getins_opcode(ins) = 0x17, 1,  -- U-type
	0
);

DROP FUNCTION IF EXISTS uses_rs1_rs2;
CREATE FUNCTION IF NOT EXISTS uses_rs1_rs2 AS (ins) -> multiIf(
	getins_opcode(ins) = 0x33, 1,  -- R-type
	getins_opcode(ins) = 0x23, 1,  -- S-type
	getins_opcode(ins) = 0x63, 1,  -- B-type
	0
);

DROP FUNCTION IF EXISTS is_type_I;
CREATE FUNCTION IF NOT EXISTS is_type_I AS (ins) -> multiIf(
	getins_opcode(ins) = 0x13, 1,  -- I-type
	0
);

DROP FUNCTION IF EXISTS is_type_S;
CREATE FUNCTION IF NOT EXISTS is_type_S AS (ins) -> multiIf(
	getins_opcode(ins) = 0x23, 1,  -- S-type
	0
);

DROP FUNCTION IF EXISTS is_type_B;
CREATE FUNCTION IF NOT EXISTS is_type_B AS (ins) -> multiIf(
	getins_opcode(ins) = 0x63, 1,  -- B-type
	0
);

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- / End of helper functions
------------------------------------------------------------------------------------------------------------------------------------------------------------


------------------------------------------------------------------------------------------------------------------------------------------------------------
-- CLOCK / PROGRAM COUNTER
------------------------------------------------------------------------------------------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS clickv.clock (_ UInt8 DEFAULT 0) ENGINE = Null;

-- Program Counter Register
CREATE TABLE IF NOT EXISTS clickv.pc (value UInt32) ENGINE = Memory SETTINGS min_rows_to_keep = 1, max_rows_to_keep = 1;
INSERT INTO clickv.pc (value) VALUES (0); -- initialize to 0

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- / End of clock / program counter / program ROM
------------------------------------------------------------------------------------------------------------------------------------------------------------

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- REGISTERS
------------------------------------------------------------------------------------------------------------------------------------------------------------

-- Redis In-Memory Registers
-- CREATE TABLE IF NOT EXISTS clickv.registers (address UInt8, value UInt32)
-- ENGINE = Redis('localhost:6379')
-- PRIMARY KEY (address);
-- TRUNCATE TABLE clickv.registers SYNC;

-- ClickHouse Keeper In-Memory Registers
CREATE TABLE IF NOT EXISTS clickv.registers (address UInt8, value UInt32)
ENGINE = KeeperMap('clickv_registers', 32)
PRIMARY KEY (address);
TRUNCATE TABLE clickv.registers SYNC;

-- zero register + 31 general-purpose registers. Initialize to 0.
INSERT INTO clickv.registers (address, value) SELECT number AS address, 0 AS value FROM numbers(1 + 31);

--CREATE VIEW IF NOT EXISTS clickv.display_registers AS SELECT * FROM clickv.registers ORDER BY time DESC, address ASC LIMIT 32;
CREATE VIEW IF NOT EXISTS clickv.display_registers AS SELECT address, get_register_name(address) AS name, r.value AS value_dec, toInt32(r.value) AS value_s_dec, hex(r.value) AS value_hex FROM clickv.registers r ORDER BY r.address ASC;

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- / End of registers
------------------------------------------------------------------------------------------------------------------------------------------------------------

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- MEMORY (RAM) + FLASH PROGRAM
------------------------------------------------------------------------------------------------------------------------------------------------------------

-- Redis In-Memory RAM
CREATE TABLE IF NOT EXISTS clickv.memory (address UInt32, value UInt8)
ENGINE = Redis('localhost:6379', 1)
PRIMARY KEY (address);
TRUNCATE TABLE clickv.memory SYNC;

-- ClickHouse Keeper In-Memory RAM
CREATE TABLE IF NOT EXISTS clickv.memory (address UInt32, value UInt8)
ENGINE = KeeperMap('clickv_registers', 3872)
PRIMARY KEY (address);
TRUNCATE TABLE clickv.memory SYNC;

CREATE VIEW IF NOT EXISTS clickv.display_memory
AS
SELECT
	hex(address) AS address,
	m.value AS value_dec,
	toInt32(m.value) AS value_s_dec,
	hex(m.value) AS value_hex,
	char(m.value) AS value_char
FROM clickv.memory m
WHERE m.address >= {o:UInt32} AND m.address < {o:UInt32} + 32
ORDER BY m.address ASC LIMIT 32;

CREATE TABLE IF NOT EXISTS clickv.load_program (hex String)
ENGINE = Null;

-- Convert hex (whitespace ignored) to memory table
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.decode_and_load_program
TO clickv.memory
AS
WITH
	lp.hex AS raw_program_hex,
	replaceAll(replaceAll(replaceAll(replaceAll(raw_program_hex, ' ', ''), '\r', ''), '\n', ''), '\t', '') AS program_hex,
	toUInt32(length(program_hex)) AS byte_count,
	range(0, byte_count, 2) AS byte_iter,
	0x0 AS program_offset
SELECT
(arrayJoin(arrayMap((x) -> (x / 2, reinterpretAsUInt8((unhex(substring(program_hex, x+1, 2))))), byte_iter)) AS byte_tuple).1 + program_offset AS address,
byte_tuple.2 AS value
FROM clickv.load_program lp;

-- View for seeing flashed program
CREATE VIEW IF NOT EXISTS clickv.program
AS
WITH
	(SELECT groupArray(toUInt32(value)) FROM (SELECT address, value FROM clickv.memory WHERE address >= 0 AND address < 2048 ORDER BY address ASC)) AS program_bytes,
	toUInt32(length(program_bytes) / 4) AS ins_count,
	range(0, ins_count, 4) AS ins_iter
SELECT
(arrayJoin(arrayMap((x) -> (toUInt32(x), bitOr(bitShiftLeft(arrayElement(program_bytes, x+4), 24), bitOr(bitShiftLeft(arrayElement(program_bytes, x+3), 16), bitOr(bitShiftLeft(arrayElement(program_bytes, x+2), 8), arrayElement(program_bytes, x+1))))), ins_iter)) AS ins_tuple).1 AS address,
ins_tuple.2 AS instruction
WHERE instruction != 0;

-- Pretty print the flashed program + current instruction
CREATE VIEW IF NOT EXISTS clickv.display_program
AS
(WITH 
	(SELECT value FROM clickv.pc) AS _pc
SELECT
	if(p.address = _pc, '->', '  ') as pc,
	hex(address) AS address,
	hex(p.instruction) AS instruction,
	get_instruction_name(p.instruction) AS name,
	if(uses_rd(p.instruction), get_register_name(getins_rd(p.instruction)), '  ') AS rd,
	if(uses_rs1_rs2(p.instruction), get_register_name(getins_rs1(p.instruction)), '  ') AS rs1,
	if(uses_rs1_rs2(p.instruction), get_register_name(getins_rs2(p.instruction)), '  ') AS rs2,
	if(is_type_I(p.instruction), toString(getins_i_imm(p.instruction)), '  ') AS type_i_imm,
	if(is_type_S(p.instruction), toString(getins_s_imm(p.instruction)), '  ') AS type_s_imm,
	if(is_type_B(p.instruction), hex(p.address + getins_branch_imm(p.instruction)), '  ') AS branch_to,
	if(get_instruction_name(p.instruction) = 'jal', hex(p.address + getins_jal_imm(p.instruction)), '  ') AS jump_to
FROM clickv.program p);

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- / End of memory
------------------------------------------------------------------------------------------------------------------------------------------------------------

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Syscall Tables
------------------------------------------------------------------------------------------------------------------------------------------------------------

-- print table for printing messages
CREATE TABLE IF NOT EXISTS clickv.print (message String, time DateTime64(9) DEFAULT now64(9))
ENGINE = Memory
SETTINGS min_rows_to_keep = 100, max_rows_to_keep = 1000;

-- View for displaying print messages
CREATE VIEW IF NOT EXISTS clickv.display_console AS SELECT message FROM clickv.print ORDER BY time ASC;

-- Screen for drawing pixels
CREATE TABLE clickv.frame (t DateTime64(3) default now64(3), d String)
ENGINE = Memory
SETTINGS min_rows_to_keep = 1, max_rows_to_keep = 100;
CREATE LIVE VIEW clickv.display_frame AS SELECT d FROM clickv.frame WHERE t > (now64(3) - INTERVAL 100 MILLISECOND) ORDER BY t ASC LIMIT 100;

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- / End of system tables
------------------------------------------------------------------------------------------------------------------------------------------------------------

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- INSTRUCTION FILTERING
--
-- Optimized materialized view for instruction checking. This is the equivalent of the binary decoder for the instruction set.
-- Instead of putting all instruction matviews in one big (conceptual) array, we can split them into separate matviews by opcode.
-- Thus reducing the number of sub-queries and function calls needed to check for the next instruction.
-- Instructions are partitioned mainly by opcode.
------------------------------------------------------------------------------------------------------------------------------------------------------------

-- Materialized view for next instruction
CREATE TABLE IF NOT EXISTS clickv.next_instruction (pc UInt32, instruction UInt32, opcode UInt8, funct3 UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction
TO clickv.next_instruction
AS
WITH
	(SELECT value FROM clickv.pc) AS pc,
	(SELECT groupArray(toUInt32(value)) FROM (SELECT address, value FROM clickv.memory WHERE address IN (pc, pc + 0x1, pc + 0x2, pc + 0x3) ORDER BY address ASC)) AS ins_bytes
SELECT
	pc,
	bitOr(bitShiftLeft(arrayElement(ins_bytes, 4), 24), bitOr(bitShiftLeft(arrayElement(ins_bytes, 3), 16), bitOr(bitShiftLeft(arrayElement(ins_bytes, 2), 8), arrayElement(ins_bytes, 1)))) AS instruction,
	getins_opcode(instruction) AS opcode,
	getins_funct3(instruction) AS funct3
FROM clickv.clock;

-- R-type instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_r_type (pc UInt32, instruction UInt32, funct3 UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_r_type
TO clickv.next_instruction_of_r_type
AS SELECT pc, instruction, funct3 FROM clickv.next_instruction
WHERE opcode = 0x33;

-- I-type bit instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_i_type_bit (pc UInt32, instruction UInt32, funct3 UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_i_type_bit
TO clickv.next_instruction_of_i_type_bit
AS SELECT pc, instruction, funct3 FROM clickv.next_instruction
WHERE opcode = 0b00010011;

-- I-type load instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_i_type_load (pc UInt32, instruction UInt32, funct3 UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_i_type_load
TO clickv.next_instruction_of_i_type_load
AS SELECT pc, instruction, funct3 FROM clickv.next_instruction
WHERE opcode = 0b00000011;

-- S-Type instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_s_type (pc UInt32, instruction UInt32, funct3 UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_s_type
TO clickv.next_instruction_of_s_type
AS SELECT pc, instruction, funct3 FROM clickv.next_instruction
WHERE opcode = 0x23;

-- U-Type instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_u_type (pc UInt32, instruction UInt32, opcode UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_u_type
TO clickv.next_instruction_of_u_type
AS SELECT pc, instruction, opcode FROM clickv.next_instruction
WHERE opcode = 0x37 OR opcode = 0x17;

-- B-Type instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_b_type (pc UInt32, instruction UInt32, funct3 UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_b_type
TO clickv.next_instruction_of_b_type
AS SELECT pc, instruction, funct3 FROM clickv.next_instruction
WHERE opcode = 0x63;

-- J-Type instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_j_type (pc UInt32, instruction UInt32, opcode UInt8) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_j_type
TO clickv.next_instruction_of_j_type
AS SELECT pc, instruction, opcode FROM clickv.next_instruction
WHERE opcode = 0b01101111 OR opcode = 0b01100111;

-- System instructions
CREATE TABLE IF NOT EXISTS clickv.next_instruction_of_system_type (pc UInt32, instruction UInt32) ENGINE = Null;
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.get_next_instruction_of_system_type
TO clickv.next_instruction_of_system_type
AS SELECT pc, instruction FROM clickv.next_instruction
WHERE opcode = 0b01110011 AND funct3 = 0x0;

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- / End of instruction filtering
------------------------------------------------------------------------------------------------------------------------------------------------------------

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- INSTRUCTIONS
------------------------------------------------------------------------------------------------------------------------------------------------------------

----------------------------------------------------------------------------
-- "add" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_add_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_add_filter TO clickv.ins_add_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x0 AND getins_r_funct7(instruction) = 0x00;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_add
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	rs1.value + rs2.value AS value
FROM clickv.ins_add_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_add_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_add_null;


----------------------------------------------------------------------------
-- "sub" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sub_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sub_filter TO clickv.ins_sub_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x0 AND getins_r_funct7(instruction) = 0x20;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sub
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	rs1.value - rs2.value AS value
FROM clickv.ins_sub_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sub_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sub_null;

----------------------------------------------------------------------------
-- "xor" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_xor_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_xor_filter TO clickv.ins_xor_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x4 AND getins_r_funct7(instruction) = 0x00;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_xor
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitXor(rs1.value, rs2.value) AS value
FROM clickv.ins_xor_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_xor_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_xor_null;

----------------------------------------------------------------------------
-- "or" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_or_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_or_filter TO clickv.ins_or_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x6 AND getins_r_funct7(instruction) = 0x00;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_or
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitOr(rs1.value, rs2.value) AS value
FROM clickv.ins_or_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_or_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_or_null;

----------------------------------------------------------------------------
-- "and" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_and_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_and_filter TO clickv.ins_and_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x7 AND getins_r_funct7(instruction) = 0x00;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_and
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitAnd(rs1.value, rs2.value) AS value
FROM clickv.ins_and_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_and_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_and_null;

----------------------------------------------------------------------------
-- "sll" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sll_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sll_filter TO clickv.ins_sll_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x1 AND getins_r_funct7(instruction) = 0x00;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sll
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitShiftLeft(rs1.value, rs2.value) AS value
FROM clickv.ins_sll_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sll_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sll_null;

----------------------------------------------------------------------------
-- "srl" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_srl_null (pc UInt32, instruction UInt32) ENGINE = Null;


-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srl_filter TO clickv.ins_srl_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x5 AND getins_r_funct7(instruction) = 0x00;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srl
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitShiftRight(rs1.value, rs2.value) AS value
FROM clickv.ins_srl_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srl_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_srl_null;

----------------------------------------------------------------------------
-- "sra" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sra_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sra_filter TO clickv.ins_sra_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x5 AND getins_r_funct7(instruction) = 0x20;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sra
TO clickv.registers
AS
WITH
	bitAnd(rs2.value, 0x1F) AS shift_by,
	bitAnd(bitShiftRight(rs1.value, 31), 1) AS msb
SELECT
	getins_rd(instruction) AS address,
	if(shift_by = 0, rs1.value, bitOr(bitShiftRight(rs1.value, shift_by), bitShiftLeft(msb, 32 - shift_by)) ) AS value
FROM clickv.ins_sra_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sra_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sra_null;

----------------------------------------------------------------------------
-- "slt" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_slt_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slt_filter TO clickv.ins_slt_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x2 AND getins_r_funct7(instruction) = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slt
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	if(toInt32(rs1.value) < toInt32(rs2.value), 1, 0) AS value
FROM clickv.ins_slt_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slt_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_slt_null;

----------------------------------------------------------------------------
-- "sltu" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sltu_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sltu_filter TO clickv.ins_sltu_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_r_type
WHERE funct3 = 0x3 AND getins_r_funct7(instruction) = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sltu
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	if(rs1.value < rs2.value, 1, 0) AS value
FROM clickv.ins_sltu_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sltu_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sltu_null;

----------------------------------------------------------------------------
-- "addi" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_addi_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_addi_filter TO clickv.ins_addi_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_addi
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	rs1.value + getins_i_imm(instruction) AS value
FROM clickv.ins_addi_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_addi_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_addi_null;

----------------------------------------------------------------------------
-- "xori" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_xori_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_xori_filter TO clickv.ins_xori_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x4;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_xori
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitXor(rs1.value, getins_i_imm(instruction)) AS value
FROM clickv.ins_xori_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_xori_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_xori_null;

----------------------------------------------------------------------------
-- "ori" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_ori_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ori_filter TO clickv.ins_ori_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x6;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ori
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitOr(rs1.value, getins_i_imm(instruction)) AS value
FROM clickv.ins_ori_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ori_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_ori_null;

----------------------------------------------------------------------------
-- "andi" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_andi_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_andi_filter TO clickv.ins_andi_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x7;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_andi
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitAnd(rs1.value, getins_i_imm(instruction)) AS value
FROM clickv.ins_andi_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_andi_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_andi_null;

----------------------------------------------------------------------------
-- "slli" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_slli_null (pc UInt32, instruction UInt32, imm UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slli_filter TO clickv.ins_slli_null
AS SELECT pc, instruction, getins_i_imm(instruction) AS imm FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x1 AND getins_i_imm_upper(imm) = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slli
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitShiftLeft(rs1.value, toUInt32(getins_i_imm_lower(imm))) AS value
FROM clickv.ins_slli_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slli_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_slli_null;

----------------------------------------------------------------------------
-- "srli" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_srli_null (pc UInt32, instruction UInt32, imm UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srli_filter TO clickv.ins_srli_null
AS SELECT pc, instruction, getins_i_imm(instruction) AS imm FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x5 AND getins_i_imm_upper(imm) = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srli
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitShiftRight(rs1.value, toUInt32(getins_i_imm_lower(imm))) AS value
FROM clickv.ins_srli_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srli_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_srli_null;

----------------------------------------------------------------------------
-- "srai" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_srai_null (pc UInt32, instruction UInt32, imm UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srai_filter TO clickv.ins_srai_null
AS SELECT pc, instruction, getins_i_imm(instruction) AS imm FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x5 AND getins_i_imm_upper(imm) = 0x20;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srai
TO clickv.registers
AS
WITH
	toUInt32(getins_i_imm_lower(imm)) AS shift_by,
	bitAnd(bitShiftRight(rs1.value, 31), 1) AS msb
SELECT
	getins_rd(instruction) AS address,
	if(shift_by = 0, rs1.value, bitOr(bitShiftRight(rs1.value, shift_by), bitShiftLeft(msb, 32 - shift_by)) ) AS value
FROM clickv.ins_srai_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_srai_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_srai_null;

----------------------------------------------------------------------------
-- "slti" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_slti_null (pc UInt32, instruction UInt32, imm UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slti_filter TO clickv.ins_slti_null
AS SELECT pc, instruction, getins_i_imm(instruction) AS imm FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x2;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slti
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	if(toInt32(rs1.value) < getins_i_imm_lower(imm), 1, 0) AS value
FROM clickv.ins_slti_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_slti_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_slti_null;

----------------------------------------------------------------------------
-- "sltiu" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sltiu_null (pc UInt32, instruction UInt32, imm UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sltiu_filter TO clickv.ins_sltiu_null
AS SELECT pc, instruction, getins_i_imm(instruction) AS imm FROM clickv.next_instruction_of_i_type_bit
WHERE funct3 = 0x3;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sltiu
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	if(rs1.value < toUInt32(getins_i_imm_lower(imm)), 1, 0) AS value
FROM clickv.ins_sltiu_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sltiu_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sltiu_null;

----------------------------------------------------------------------------
-- "lui" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_lui_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lui_filter TO clickv.ins_lui_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_u_type
WHERE opcode = 0x37;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lui
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	bitShiftLeft(getins_u_imm(instruction), 12) AS value
FROM clickv.ins_lui_null
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lui_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_lui_null;

----------------------------------------------------------------------------
-- "auipc" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_auipc_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_auipc_filter TO clickv.ins_auipc_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_u_type
WHERE opcode = 0x17;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_auipc
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	pc + bitShiftLeft(getins_u_imm(instruction), 12) AS value
FROM clickv.ins_auipc_null
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_auipc_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_auipc_null;

----------------------------------------------------------------------------
-- "lb" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_lb_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lb_filter TO clickv.ins_lb_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_load
WHERE funct3 = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lb
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	toUInt32(toInt32(toInt8(m.value))) AS value
FROM clickv.ins_lb_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.memory m ON m.address::UInt32 = (rs1.value + getins_i_imm(instruction))::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lb_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_lb_null;

----------------------------------------------------------------------------
-- "lh" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_lh_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lh_filter TO clickv.ins_lh_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_load
WHERE funct3 = 0x1;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lh
TO clickv.registers
AS
WITH
	rs1.value + getins_i_imm(instruction) AS offset
SELECT
	getins_rd(instruction) AS address,
	toUInt32(toInt32(toInt16(bitOr(bitShiftLeft(toUInt16(m1.value), 8), m0.value)))) AS value
FROM clickv.ins_lh_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.memory m0 ON m0.address::UInt32 = offset::UInt32
JOIN clickv.memory m1 ON m1.address::UInt32 = (offset + 0x1)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lh_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_lh_null;

----------------------------------------------------------------------------
-- "lw" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_lw_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lw_filter TO clickv.ins_lw_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_load
WHERE funct3 = 0x2;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lw
TO clickv.registers
AS
WITH
	rs1.value + getins_i_imm(instruction) AS offset
SELECT
	getins_rd(instruction) AS address,
	bitOr(bitShiftLeft(toUInt32(m3.value), 24), bitOr(bitShiftLeft(toUInt32(m2.value), 16), bitOr(bitShiftLeft(toUInt32(m1.value), 8), toUInt32(m0.value)))) AS value
FROM clickv.ins_lw_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.memory m0 ON m0.address::UInt32 = offset::UInt32
JOIN clickv.memory m1 ON m1.address::UInt32 = (offset + 0x1)::UInt32
JOIN clickv.memory m2 ON m2.address::UInt32 = (offset + 0x2)::UInt32
JOIN clickv.memory m3 ON m3.address::UInt32 = (offset + 0x3)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lw_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_lw_null;

----------------------------------------------------------------------------
-- "lbu" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_lbu_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lbu_filter TO clickv.ins_lbu_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_load
WHERE funct3 = 0x4;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lbu
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	toUInt32(m.value) AS value
FROM clickv.ins_lbu_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.memory m ON m.address::UInt32 = (rs1.value + getins_i_imm(instruction))::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lbu_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_lbu_null;

----------------------------------------------------------------------------
-- "lhu" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_lhu_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lhu_filter TO clickv.ins_lhu_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_i_type_load
WHERE funct3 = 0x5;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lhu
TO clickv.registers
AS
WITH
	rs1.value + getins_i_imm(instruction) AS offset
SELECT
	getins_rd(instruction) AS address,
	bitOr(bitShiftLeft(toUInt16(m1.value), 8), m0.value) AS value
FROM clickv.ins_lhu_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.memory m0 ON m0.address::UInt32 = offset::UInt32
JOIN clickv.memory m1 ON m1.address::UInt32 = (offset + 0x1)::UInt32
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_lhu_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_lhu_null;

----------------------------------------------------------------------------
-- "sb" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sb_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sb_filter TO clickv.ins_sb_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_s_type
WHERE funct3 = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sb
TO clickv.memory
AS
SELECT
	rs1.value + getins_s_imm(instruction) AS address,
	toUInt8(rs2.value) AS value
FROM clickv.ins_sb_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sb_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sb_null;

----------------------------------------------------------------------------
-- "sh" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sh_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sh_filter TO clickv.ins_sh_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_s_type
WHERE funct3 = 0x1;

-- store 2 bytes
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sh_0
TO clickv.memory
AS
WITH
	[0, 1] AS byte_iter,
	rs1.value + getins_s_imm(instruction) AS start_address
SELECT
	(arrayJoin(arrayMap((i) -> (start_address + i, toUInt8(bitShiftRight(rs2.value, i * 8))), byte_iter)) AS out).1 AS address,
	out.2 AS value
FROM clickv.ins_sh_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sh_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sh_null;

----------------------------------------------------------------------------
-- "sw" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_sw_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sw_filter TO clickv.ins_sw_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_s_type
WHERE funct3 = 0x2;

-- store 4 bytes
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sw_0
TO clickv.memory
AS
WITH
	[0, 1, 2, 3] AS byte_iter,
	rs1.value + getins_s_imm(instruction) AS start_address
SELECT
	(arrayJoin(arrayMap((i) -> (start_address + i, toUInt8(bitShiftRight(rs2.value, i * 8))), byte_iter)) AS out).1 AS address,
	out.2 AS value
FROM clickv.ins_sw_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_sw_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_sw_null;

----------------------------------------------------------------------------
-- "jal" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_jal_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_jal_filter TO clickv.ins_jal_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_j_type
WHERE opcode = 0b01101111;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_jal
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	pc + 4 as value
FROM clickv.ins_jal_null
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_jal_incr_pc TO clickv.pc AS
SELECT pc + getins_jal_imm(instruction) AS value
FROM clickv.ins_jal_null;

----------------------------------------------------------------------------
-- "jalr" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_jalr_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_jalr_filter TO clickv.ins_jalr_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_j_type
WHERE opcode = 0b01100111 AND getins_funct3(instruction) = 0x0;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_jalr
TO clickv.registers
AS
SELECT
	getins_rd(instruction) AS address,
	pc + 4 as value
FROM clickv.ins_jalr_null
WHERE address != 0;

-- increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_jalr_incr_pc TO clickv.pc AS
SELECT rs1.value + getins_i_imm(instruction) AS value
FROM clickv.ins_jalr_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32;

----------------------------------------------------------------------------
-- "beq" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_beq_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_beq_filter TO clickv.ins_beq_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_b_type
WHERE funct3 = 0x0;

-- branch or skip
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_beq_incr_pc TO clickv.pc AS
WITH
	rs1.value = rs2.value AS branch
SELECT
	pc + if(branch, getins_branch_imm(instruction), 4) AS value
FROM clickv.ins_beq_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

----------------------------------------------------------------------------
-- "bne" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_bne_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bne_filter TO clickv.ins_bne_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_b_type
WHERE funct3 = 0x1;

-- branch or skip
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bne_incr_pc TO clickv.pc AS
WITH
	rs1.value != rs2.value AS branch
SELECT
	pc + if(branch, getins_branch_imm(instruction), 4) AS value
FROM clickv.ins_bne_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

----------------------------------------------------------------------------
-- "blt" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_blt_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_blt_filter TO clickv.ins_blt_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_b_type
WHERE funct3 = 0x4;

-- branch or skip
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_blt_incr_pc TO clickv.pc AS
WITH
	toInt32(rs1.value) < toInt32(rs2.value) AS branch
SELECT
	pc + if(branch, getins_branch_imm(instruction), 4) AS value
FROM clickv.ins_blt_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

----------------------------------------------------------------------------
-- "bge" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_bge_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bge_filter TO clickv.ins_bge_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_b_type
WHERE funct3 = 0x5;

-- branch or skip
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bge_incr_pc TO clickv.pc AS
WITH
	toInt32(rs1.value) >= toInt32(rs2.value) AS branch
SELECT
	pc + if(branch, getins_branch_imm(instruction), 4) AS value
FROM clickv.ins_bge_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

----------------------------------------------------------------------------
-- "bltu" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_bltu_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bltu_filter TO clickv.ins_bltu_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_b_type
WHERE funct3 = 0x6;

-- branch or skip
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bltu_incr_pc TO clickv.pc AS
WITH
	rs1.value < rs2.value AS branch
SELECT
	pc + if(branch, getins_branch_imm(instruction), 4) AS value
FROM clickv.ins_bltu_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

----------------------------------------------------------------------------
-- "bgeu" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_bgeu_null (pc UInt32, instruction UInt32) ENGINE = Null;

-- instruction filter
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bgeu_filter TO clickv.ins_bgeu_null
AS SELECT pc, instruction FROM clickv.next_instruction_of_b_type
WHERE funct3 = 0x7;

-- branch or skip
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_bgeu_incr_pc TO clickv.pc AS
WITH
	rs1.value >= rs2.value AS branch
SELECT
	pc + if(branch, getins_branch_imm(instruction), 4) AS value
FROM clickv.ins_bgeu_null
JOIN clickv.registers rs1 ON rs1.address::UInt32 = getins_rs1(instruction)::UInt32
JOIN clickv.registers rs2 ON rs2.address::UInt32 = getins_rs2(instruction)::UInt32;

----------------------------------------------------------------------------
-- "ecall" instruction
----------------------------------------------------------------------------

-- trigger instruction execution
CREATE TABLE IF NOT EXISTS clickv.ins_ecall_null (pc UInt32, instruction UInt32, syscall_n UInt32) ENGINE = Null;

-- instruction filter, reads syscall number from a7 register
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_filter TO clickv.ins_ecall_null
AS SELECT pc, instruction, (SELECT value FROM clickv.registers WHERE address = 0x11) AS syscall_n FROM clickv.next_instruction_of_system_type
WHERE getins_i_imm(instruction) = 0x0 AND syscall_n > 0;

---------------------------
-- syscall PRINT (0x1)
---------------------------

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_print
TO clickv.print
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xA) AS text_ptr, -- a0
	(SELECT value FROM clickv.registers WHERE address = 0xB) AS text_len, -- a1
	(SELECT groupArray(value) FROM (SELECT address, value FROM clickv.memory WHERE address >= text_ptr AND address < (text_ptr + text_len) ORDER BY address ASC)) AS text_bytes
SELECT
	arrayStringConcat(arrayMap(x -> char(x), text_bytes)) AS message
FROM clickv.ins_ecall_null
WHERE syscall_n = 0x1;


---------------------------
-- syscall DRAW (0x2)
---------------------------

CREATE TABLE IF NOT EXISTS clickv.ins_ecall_draw_null (_ UInt8) ENGINE = Null;

-- Drawing is expensive. Check the syscall here before actually processing drawing.
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_draw_filter
TO clickv.ins_ecall_draw_null
AS
SELECT 0 AS _ FROM clickv.ins_ecall_null
WHERE syscall_n = 0x2;


CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_draw
TO clickv.frame
AS
WITH
	-- add 1 to end to fix end of frame.
	range(1, 800 + 1, 1) AS cell_iter,
	(SELECT groupArray(value) FROM (SELECT address, value FROM clickv.memory WHERE address >= 0x00000C00 AND address < (0x00000C00 + 800) ORDER BY address ASC)) AS cells
SELECT
	concat(
		concat(char(27), '[2J', char(27), '[1;1H'),
		arrayStringConcat(arrayMap(i -> concat(char(27), '[', toString(arrayElement(cells, i)), 'm', '█', if(i % 40 = 0, '\n', '')), cell_iter))
	 ) AS d
FROM clickv.ins_ecall_draw_null;

---------------------------
-- ClickOS OPEN
---------------------------

CREATE TABLE IF NOT EXISTS clickv.ins_ecall_clickos_open_null (syscall_n UInt32) ENGINE = Null;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_open_filter
TO clickv.ins_ecall_clickos_open_null
AS
SELECT syscall_n FROM clickv.ins_ecall_null
WHERE syscall_n = 10;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_open
TO clickv.registers
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xA) AS path_name_ptr, -- a0
	(SELECT value FROM clickv.registers WHERE address = 0xB) AS path_name_len, -- a1
	(SELECT value FROM clickv.registers WHERE address = 0xC) AS flags, -- a2
	(SELECT groupArray(value) FROM (SELECT address, value FROM clickv.memory WHERE address >= path_name_ptr AND address < (path_name_ptr + path_name_len) ORDER BY address ASC)) AS path_name_bytes,
	arrayConcat(path_name_bytes, [0], uint32_to_byte_array(flags)) AS request_bytes,
	clickos_syscall(syscall_n, request_bytes) AS response_bytes
SELECT
	0xA AS address, -- a0
	byte_array_to_uint32(response_bytes) AS value -- fd
FROM clickv.ins_ecall_clickos_open_null;

---------------------------
-- ClickOS CLOSE
---------------------------

CREATE TABLE IF NOT EXISTS clickv.ins_ecall_clickos_close_null (syscall_n UInt32) ENGINE = Null;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_close_filter
TO clickv.ins_ecall_clickos_close_null
AS
SELECT syscall_n FROM clickv.ins_ecall_null
WHERE syscall_n = 11;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_close
TO clickv.registers
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xA) AS fd, -- a0
	clickos_syscall(syscall_n, arrayConcat(uint32_to_byte_array(fd))) AS response_bytes
SELECT
	0xA AS address, -- a0
	byte_array_to_uint32(response_bytes) AS value -- status
FROM clickv.ins_ecall_clickos_close_null;

---------------------------
-- ClickOS SEEK
---------------------------

CREATE TABLE IF NOT EXISTS clickv.ins_ecall_clickos_seek_null (syscall_n UInt32) ENGINE = Null;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_seek_filter
TO clickv.ins_ecall_clickos_seek_null
AS
SELECT syscall_n FROM clickv.ins_ecall_null
WHERE syscall_n = 12;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_seek
TO clickv.registers
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xA) AS fd, -- a0
	(SELECT value FROM clickv.registers WHERE address = 0xB) AS offset, -- a1
	(SELECT value FROM clickv.registers WHERE address = 0xC) AS whence, -- a2
	clickos_syscall(syscall_n, arrayConcat(uint32_to_byte_array(fd), uint32_to_byte_array(offset), uint32_to_byte_array(whence))) AS response_bytes
SELECT
	0xA AS address, -- a0
	byte_array_to_uint32(response_bytes) AS value -- current_seek
FROM clickv.ins_ecall_clickos_seek_null;

---------------------------
-- ClickOS READ
---------------------------

CREATE TABLE IF NOT EXISTS clickv.ins_ecall_clickos_read_null (syscall_n UInt32) ENGINE = Null;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_read_filter
TO clickv.ins_ecall_clickos_read_null
AS
SELECT syscall_n FROM clickv.ins_ecall_null
WHERE syscall_n = 13;

-- perform the actual file read via UDF
CREATE TABLE IF NOT EXISTS clickv.ins_ecall_clickos_read_output_null (bytes_read UInt32, bytes Array(UInt8)) ENGINE = Null;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_read
TO clickv.ins_ecall_clickos_read_output_null
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xA) AS fd, -- a0
	(SELECT value FROM clickv.registers WHERE address = 0xC) AS count, -- a2
	clickos_syscall(syscall_n, arrayConcat(uint32_to_byte_array(fd), uint32_to_byte_array(count))) AS response_bytes
SELECT
	byte_array_to_uint32(response_bytes) AS bytes_read,
	arraySlice(response_bytes, 5) AS bytes -- trim first 4 bytes
FROM clickv.ins_ecall_clickos_read_null;

-- set bytes in memory
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_read_output_memory
TO clickv.memory
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xB) AS buffer_ptr, -- a1
	-- safe iterator range
	range(0, if(bytes_read < 0 OR bytes_read > 0x10000, 0, bytes_read), 1) AS byte_iter
SELECT
	(arrayJoin(arrayMap((i) -> (buffer_ptr + i, arrayElement(bytes, i+1)), byte_iter)) AS out).1 AS address,
	out.2 AS value
FROM clickv.ins_ecall_clickos_read_output_null
WHERE bytes_read > 0; -- only write if bytes were read

-- set bytes_read in a0 register. Could also represent error if negative.
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_read_output_register
TO clickv.registers
AS
SELECT
	0xA AS address, -- a0
	bytes_read AS value -- bytes read, or error
FROM clickv.ins_ecall_clickos_read_output_null;

---------------------------
-- ClickOS WRITE
---------------------------

CREATE TABLE IF NOT EXISTS clickv.ins_ecall_clickos_write_null (syscall_n UInt32) ENGINE = Null;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_write_filter
TO clickv.ins_ecall_clickos_write_null
AS
SELECT syscall_n FROM clickv.ins_ecall_null
WHERE syscall_n = 14;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_write
TO clickv.registers
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xA) AS fd, -- a0
	(SELECT value FROM clickv.registers WHERE address = 0xB) AS buffer_ptr, -- a1
	(SELECT value FROM clickv.registers WHERE address = 0xC) AS count, -- a2
	(SELECT groupArray(value) FROM (SELECT address, value FROM clickv.memory WHERE address >= buffer_ptr AND address < (buffer_ptr + count) ORDER BY address ASC)) AS buffer_bytes,
	clickos_syscall(syscall_n, arrayConcat(uint32_to_byte_array(fd), buffer_bytes)) AS response_bytes
SELECT
	0xA AS address, -- a0
	byte_array_to_uint32(response_bytes) AS value -- bytes written, or error
FROM clickv.ins_ecall_clickos_write_null;

---------------------------
-- ClickOS SOCKET
---------------------------

CREATE TABLE IF NOT EXISTS clickv.ins_ecall_clickos_socket_null (syscall_n UInt32) ENGINE = Null;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_socket_filter
TO clickv.ins_ecall_clickos_socket_null
AS
SELECT syscall_n FROM clickv.ins_ecall_null
WHERE syscall_n = 15;

CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_clickos_socket
TO clickv.registers
AS
WITH
	(SELECT value FROM clickv.registers WHERE address = 0xA) AS address_ptr, -- a0
	(SELECT value FROM clickv.registers WHERE address = 0xB) AS address_len, -- a1
	(SELECT groupArray(value) FROM (SELECT address, value FROM clickv.memory WHERE address >= address_ptr AND address < (address_ptr + address_len) ORDER BY address ASC)) AS address_bytes,
	arrayConcat(address_bytes, [0]) AS request_bytes,
	clickos_syscall(syscall_n, request_bytes) AS response_bytes
SELECT
	0xA AS address, -- a0
	byte_array_to_uint32(response_bytes) AS value -- socket fd
FROM clickv.ins_ecall_clickos_socket_null;

-- after any/all syscalls, increment PC
CREATE MATERIALIZED VIEW IF NOT EXISTS clickv.ins_ecall_incr_pc TO clickv.pc AS SELECT pc + 4 AS value FROM clickv.ins_ecall_null;

------------------------------------------------------------------------------------------------------------------------------------------------------------
-- / End of instructions
------------------------------------------------------------------------------------------------------------------------------------------------------------

------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Run + Control Click-V
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------

---------------------------------------
-- Click-V Cheat Sheet
---------------------------------------

-- Step clock
-- INSERT INTO clickv.clock (_) VALUES ();

-- Show program
-- SELECT * FROM clickv.display_program;

-- Show registers
-- SELECT * FROM clickv.display_registers;

-- Show memory (with o parameter for offset)
-- SELECT * FROM clickv.display_memory(o=1024);

-- Show Console / Printed messages
-- SELECT * FROM clickv.display_console FORMAT RawBLOB;

-- Frame
-- SET allow_experimental_live_view = 1;
	-- Show Frame
		-- SELECT * FROM clickv.display_frame FORMAT RawBLOB;
	-- Live-update frame
		-- WATCH clickv.display_frame FORMAT RawBLOB;

-- Other option to show frame:
	-- WITH
	-- 	-- add 1 to end to fix end of frame.
	-- 	range(1, 800 + 1, 1) AS cell_iter,
	-- 	(SELECT groupArray(value) FROM (SELECT address, value FROM clickv.memory WHERE address >= 0x00000C00 AND address < (0x00000C00 + 800) ORDER BY address ASC)) AS cells
	-- SELECT
	-- 	concat(
	-- 		concat(char(27), '[2J', char(27), '[1;1H'),
	-- 		arrayStringConcat(arrayMap(i -> concat(char(27), '[', toString(arrayElement(cells, i)), 'm', '█', if(i % 40 = 0, '\n', '')), cell_iter))
	-- 	 ) AS d FORMAT RawBLOB;

---------------------------------------
-- Reset + Load Program for Click-V
---------------------------------------

-- Reset PC to 0
INSERT INTO clickv.pc (value) VALUES (0);

-- Clear registers, reset to 0
TRUNCATE TABLE clickv.registers SYNC;
INSERT INTO clickv.registers (address, value) SELECT number AS address, 0 AS value FROM numbers(1 + 31);

-- Clear memory, define new memory layout
-- 2Kib ROM, 1Kib RAM. 800b VRAM. Initialize to 0.
TRUNCATE TABLE clickv.memory SYNC;
INSERT INTO clickv.memory (address, value) SELECT number AS address, 0 AS value FROM numbers(2048 + 1024 + 800);

-- Clear console
TRUNCATE TABLE clickv.print;

-- Reset ClickOS (Optional)
-- SELECT clickos_syscall(0, []);

-- Paste actual program here. Whitespace is removed automatically.
INSERT INTO clickv.load_program (hex) VALUES ('

');
