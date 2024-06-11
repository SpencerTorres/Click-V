package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/tidwall/redcon"
)

/**
 * This program was an attempt to make register+memory access faster.
 * Redis works fine too.
 */

const serverHost = "0.0.0.0:6379"
const MEM_SIZE = 2048 + 1024 + 800  // ROM, RAM, VRAM
var memory = make([]byte, MEM_SIZE) // no mutex. CPU is single threaded, plus I like the chaos.
const REG_SIZE = 32                 // 32 registers
var registers = make([]uint32, REG_SIZE)

const (
	REGISTER_DB = 0
	MEMORY_DB   = 1
)

func main() {
	go log.Printf("started server at %s", serverHost)

	var connToDB = make(map[string]int, 2)
	err := redcon.ListenAndServe(serverHost,
		func(conn redcon.Conn, cmd redcon.Command) {
			db := connToDB[conn.RemoteAddr()]
			fmt.Printf("user: %s db: %d, cmd: %s args: %d\n", conn.RemoteAddr(), db, string(cmd.Args[0]), len(cmd.Args[1:]))

			switch strings.ToLower(string(cmd.Args[0])) {
			default:
				conn.WriteError("ERR unknown command '" + string(cmd.Args[0]) + "'")
			case "ping":
				conn.WriteString("PONG")
			case "quit":
				conn.WriteString("OK")
				conn.Close()
			case "select":
				dbByte := string(cmd.Args[1])
				db, _ := strconv.ParseInt(dbByte, 10, 32)
				connToDB[conn.RemoteAddr()] = int(db)
				conn.WriteString("OK")
			case "set":
				if len(cmd.Args) != 3 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}

				switch db {
				case REGISTER_DB:
					reg := cmd.Args[1][0]

					// x0 is always 0
					if reg == 0 {
						conn.WriteString("OK")
						return
					} else if reg > 31 {
						conn.WriteError("ERR register address out of range")
						return
					}

					registers[reg] = binary.LittleEndian.Uint32(cmd.Args[2])
				case MEMORY_DB:
					addr := binary.LittleEndian.Uint32(cmd.Args[1])
					if addr > MEM_SIZE {
						conn.WriteError("ERR memory address out of range")
						return
					}

					memory[addr] = cmd.Args[2][0]
				}

				conn.WriteString("OK")
			case "get":
				if len(cmd.Args) != 2 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}

				switch db {
				case REGISTER_DB:
					reg := cmd.Args[1][0]
					value := registers[reg]
					conn.WriteAny(value)
				case MEMORY_DB:
					addr := binary.LittleEndian.Uint32(cmd.Args[1])
					if addr > MEM_SIZE {
						conn.WriteError("ERR memory address out of range")
						return
					}

					value := memory[addr]
					conn.WriteAny(value)
				}
			case "mset":
				if len(cmd.Args) < 3 || len(cmd.Args)%2 != 1 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}

				for i := 1; i < len(cmd.Args); i += 2 {
					switch db {
					case REGISTER_DB:
						reg := cmd.Args[i][0]

						// x0 is always 0
						if reg == 0 {
							continue
						} else if reg > 31 {
							conn.WriteError("ERR register address out of range")
							return
						}

						registers[reg] = binary.LittleEndian.Uint32(cmd.Args[2])
					case MEMORY_DB:
						addr := binary.LittleEndian.Uint32(cmd.Args[i])
						if addr > MEM_SIZE {
							conn.WriteError("ERR memory address out of range")
							return
						}
						memory[addr] = cmd.Args[i+1][0]
					}
				}

				conn.WriteString("OK")
			case "mget":
				if len(cmd.Args) < 2 {
					conn.WriteError("ERR wrong number of arguments for '" + string(cmd.Args[0]) + "' command")
					return
				}

				conn.WriteArray(len(cmd.Args) - 1)
				regOut := make([]byte, 4)
				memOut := make([]byte, 1)
				for i := 1; i < len(cmd.Args); i++ {
					switch db {
					case REGISTER_DB:
						argInt := cmd.Args[i][0]
						value := registers[argInt]
						binary.LittleEndian.PutUint32(regOut, value)
						conn.WriteBulk(regOut)
					case MEMORY_DB:
						argInt := binary.LittleEndian.Uint32(cmd.Args[i])
						value := memory[argInt]

						memOut[0] = uint8(value)
						conn.WriteBulk(memOut)
					}
				}
			case "scan":
				conn.WriteArray(2)
				conn.WriteBulkString("0")

				switch db {
				case REGISTER_DB:
					conn.WriteArray(REG_SIZE)
					out := make([]byte, 1)
					for i := 0; i < REG_SIZE; i++ {
						out[0] = uint8(i)
						conn.WriteBulk(out)
					}
				case MEMORY_DB:
					conn.WriteArray(MEM_SIZE)
					out := make([]byte, 4)
					for i := 0; i < MEM_SIZE; i++ {
						binary.LittleEndian.PutUint32(out, uint32(i))
						conn.WriteBulk(out)
					}
				}

			case "truncate", "flushdb":

				switch db {
				case REGISTER_DB:
					for i := 0; i < 32; i++ {
						registers[i] = 0
					}
				case MEMORY_DB:
					for i := 0; i < MEM_SIZE; i++ {
						memory[i] = 0
					}
				}

				conn.WriteString("OK")
			}
		},
		func(conn redcon.Conn) bool {
			connToDB[conn.RemoteAddr()] = 0
			return true
		},
		func(conn redcon.Conn, err error) {
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}
