# Click-V
A RISC-V emulator built with ClickHouse SQL.

This emulator makes ClickHouse truly Turing complete. We are one step closer to running ClickHouse in ClickHouse.

This project/repository isn't dev-friendly yet, I'm just uploading it here as a backup in case my PC catches fire.

## How it works

The system will react to the following insert command:

```sql
INSERT INTO clickv.clock (_) VALUES ()
```

This command will trigger a large set of branched materialized views and `Null` tables that filter out the program's instructions to simulate reading/writing from registers and memory.

External host machine access works via a single UDF with a custom binary format that gets read/written as an `Array(UInt8)`.

The program is able to perform any logic. Printing to a console table and drawing are built-in.
It can also open/close/read/write/seek files and sockets via the ClickOS UDF.

For more details, see the [architecture](#architecture) section.

### Performance

Using the `EmbeddedRocksDB` table engine for registers/memory, the performance degrades as the memory grows because some parts of the emulator must scan through memory multiple times.

For smaller binaries, it can run close to `60hz`, but for complex binaries (such as Doom) it is closer to `12hz`.

## How to run

Steps:
- Set up a ClickHouse v25.10+ image
- Run all SQL statements in `/sql/click-v.sql` (exclude the syscalls at the bottom if you don't have the ClickOS UDF setup. See `docker-compose.yml` for clues.)
- Load your own RISC-V 32im program into `INSERT INTO clickv.load_program (hex) VALUES ('FFFFFFFF')` (make sure your hex instructions are in the correct direction). For larger binaries use `./system/loadprogram`.
- Either clock the system via `INSERT INTO clickv.clock (_) VALUES ()`, or use the auto-clock in `/system/clock`.
- To reset the CPU, run the commands in `/sql/reset.sql`. You can pre-allocate memory here too.

You can now monitor the program with the following commands:
- Show program instructions + current instruction: `SELECT * FROM clickv.display_program;`
- Show all 32 registers: `SELECT * FROM clickv.display_registers;`
- Show memory (with o parameter for offset): `SELECT * FROM clickv.display_memory(o=1024);`
- Show console: `SELECT * FROM clickv.display_console FORMAT TSV;`
- Setup live view (optional): `SET allow_experimental_live_view = 1;`
- (After frame setup) Show current drawn frame: `SELECT * FROM clickv.display_frame FORMAT RawBLOB;`
- (After frame setup) Show live-updating frame: `WATCH clickv.display_frame FORMAT RawBLOB;`

For more help/commands, see the bottom of `/sql/click-v.sql` file.
ROM/RAM/Graphics Memory is configurable.

## Components

### ClickHouse

Depends on ClickHouse v25.10+.
No other setup is required for basic emulator.
For handling syscalls, you will need to set up the ClickOS UDF, but this is optional.

### Clock

*path: `/system/cmd/clock`*

This program simply runs the clock for you, as fast as possible.
Will output clock speed and total cycles to console.

### ClickOS

*path: `/system/cmd/clickos-server`*
*path: `/system/cmd/clickos-client`*

Optional program to give the emulated program access to the host system/network.

This is a client/server application.
The client runs as a ClickHouse executable UDF, and then forwards requests to the server.
The server will then handle all syscalls (such as reading/writing to a file, opening a UDP socket, etc.)

You will need to set up the UDF in your ClickHouse server. Easiest way is to make two Docker volume binds: one to the UDF XML, and the other to built binary (you must `go build` for your docker env/arch)

Run the server to listen/handle syscalls. File paths are relative to the working directory of the ClickOS server process.

### rs-demo

*path: `/rs-demo`*

This is a demo rust program that can be compiled to run in the emulator.
I have some boilerplate for syscalls, with some OS abstractions for `read`, `write`, `seek`, `socket`, `open`, `close`, etc.
I also have some code that handles drawing to the screen.

To get the program hex, I made a script called `gethex.sh`.
You can copy/paste this directly into the program input for the emulator.

This program contains a linker script that defines the memory ranges for ROM, RAM, Stack size, and VRAM.


### RISC-V Instruction Test suite

*path: `/system/test/instruction_test.go`*

How do we know any of these instructions do what they're supposed to do?
To answer this, I made a unit test for each instruction.
It is now much easier to see if the instructions are compliant with the specification when isolated.

This file will run a test for each instruction, some with different test cases.
It also prints out the performance of each instruction. You'll notice some instructions are more costly than others.


# Architecture

I will simplify this into several components:
- Clock
- Program Counter (PC)
- Memory
- Registers
- Instructions
- Syscalls

## Clock

*Schema: no schema*

As the name suggests, this is the clock for the emulated CPU.
This is implemented as a `Null` table. When you insert into this, it will cascade down a set of materialized views.

## Program Counter (PC)

*Schema: `value UInt32`*

This is a `Memory` table with limits to store exactly `1` row.
It stores a single `UInt32`, which represents the current instruction.

## Memory
*Schema: `address UInt32, value UInt8`*

Memory contains the program instructions (ROM), as well as RAM and VRAM (for the display).

#### Engine choice

While I originally had this implemented as a `Memory` table, it was clear that this would not
work for larger programs.
When writing to memory, it would push out the oldest row.
It would also require adding a `timestamp` field of some kind to each row, since it could contain duplicates. `ReplacingMergeTree` was also considered, but this writes to disk, and would have duplicates before the parts are processed (which is likely in a high-speed emulator environment).

It can be done, but it would require having a lot of duplicated rows, with enough space so that old memory would have a low probability of falling out of the table. Too much memory usage.

So I then switched to a `EmbeddedRocksDB` table engine. This is the optimal structure, since it operates as a fast in-memory KV store with no duplicates.
This works perfectly, but gets slower as the allocated memory gets larger. The ClickHouse query analyzer might be able to optimize this better in the future.

Memory can be read via a `JOIN` or sub-query, even in multiple bytes.
Memory can be written in multiple bytes using `arrayJoin` into the `memory` table.

## Registers
*Schema: `address UInt8, value UInt32`*

Registers are implemented the same as memory, but with 32 fixed registers.

## Instructions

The first materialized view hit by the `clock` table is `get_next_instruction`.
This will parse the `pc`, `instruction`, `opcode`, and `funct3` and send it to the next layer of materialized views. The idea with these layers is to reduce the number of function calls and queries for parsing the instruction.

The next layer will then split by instruction type. For example: **R-type**, **I-type**, **S-type**, **jump**, **ecall**, etc.
These views have a `WHERE` condition that blocks them from inserting into the next layer of `Null` tables, which again reduces the number of queries/function calls.

Within each of these types (such as **R-type**) is the materialized views for the individual instruction. At this point it will do the final check to see which instruction it is, and then forward to another `Null` table for executing the instruction. By this point, there's no other path for that instruction, and all the expensive queries can be made.

Each instruction (with the exception of jumps and branches) will have another materialized view at the end that increments the `pc` by `4`. Materialized views are executed in the order they are created, so this works flawlessly for executing sequential logic.

Depending on the instruction, the output will either write to the main `registers` or `memory` table. Instructions can also read from these table via a `JOIN`.

With the layers of filtering, it keeps the execution path short for the ClickHouse server.
This also offers an easy way to measure performance per-instruction, since the original `clock` insert will not return until the last materialized view is finished.

## Syscalls (`ecall`)

RISC-V has a special instruction for returning control to the operating system: `ecall`.
The Click-V emulator is able to make use of this special instruction for 3 major features:
1. writing to a `print` table, to replicate `stdout`
2. writing to a `frame` table, trigger rendering the data within VRAM into a terminal-displayed frame.
3. making external calls to the host system via ClickOS (read/write files, communicate over UDP socket, anything else you can imagine)

`ecall` is implemented same as the other instructions, but due to the expensive nature of these calls, they are hidden behind another layer of materialized views to prevent unnecessary sub-queries from being triggered.

The syscall number is read from register `a7`, and the arguments are passed in the other `aX` registers. Depending on the call, the result/status code will be returned back in `a0`.

All syscalls have been implemented in the `rs-demo` program.

### print

This call is really simple, it just reads from memory using `text_ptr` and `text_len`, and then inserts the result into the `print` table.

### draw

This call will read from video memory and split up the bytes into a terminal-based image with ANSI colors. You can use the `LIVE VIEW` / `WATCH` API to get this to update in real time.

### ClickOS

External system access is managed by ClickOS. These calls are able to read/write to/from emulator memory in order to implement file descriptors for interacting with the host system.

Access to the host system is implemented via a ClickHouse executable UDF. The memory gets inserted/returned as an `Array(UInt8)`.

With a similar API to the Linux kernel, these usually rely on a `buffer_ptr` and `buffer_len` for exposing program memory.
