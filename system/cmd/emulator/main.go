package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const showAddrWarn = true
const programSpaceAddr = 0x599A0
const framebufferAddr = 0xA3CD4
const showFileSyscalls = false

var exitAfterFrames = 0
var exitAfterCycleCount = 17690560
var emulatorVerify = false

const (
	SYS_OPEN  = 10
	SYS_READ  = 13
	SYS_SEEK  = 12
	SYS_CLOSE = 11
	SYS_PRINT = 1

	SYSCALL_ERROR = 0xFFFFFFFF
)

const DEBUG = false

var regNames = []string{
	"zero", "ra", "sp", "gp", "tp", "t0", "t1", "t2",
	"s0/fp", "s1", "a0", "a1", "a2", "a3", "a4", "a5",
	"a6", "a7", "s2", "s3", "s4", "s5", "s6", "s7",
	"s8", "s9", "s10", "s11", "t3", "t4", "t5", "t6",
}

type CPU struct {
	conn       clickhouse.Conn
	regs       [32]uint32
	pc         uint32
	memory     []byte
	files      map[int]*os.File
	nextFd     int
	frameCount int
	instCount  int
	stop       bool
	stopReason string
}

func NewCPU(memSize int) *CPU {
	return &CPU{
		memory: make([]byte, memSize),
		files:  make(map[int]*os.File),
		nextFd: 3, // Start after stdin/stdout/stderr
	}
}

func (c *CPU) LoadProgram(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	copy(c.memory, data)
	fmt.Printf("Loaded %d bytes from %s\n", len(data), filename)
	return nil
}

func (c *CPU) readMem(addr uint32) uint32 {
	if addr+4 > uint32(len(c.memory)) {
		fmt.Printf("Reading u32 outside of memory addr=0x%08x (%d)\n", addr, addr)
		return 0
	}
	return binary.LittleEndian.Uint32(c.memory[addr:])
}

func (c *CPU) writeMem(addr uint32, val uint32) {
	if addr+4 > uint32(len(c.memory)) {
		fmt.Printf("Writing outside of memory addr=0x%08x (%d) val=0x%08x (%d)\n", addr, addr, val, val)
		return
	}
	binary.LittleEndian.PutUint32(c.memory[addr:], val)
}

func (c *CPU) getReg(r int) uint32 {
	if r == 0 {
		return 0
	}
	return c.regs[r]
}

func (c *CPU) setReg(r int, val uint32) {
	if r != 0 {
		c.regs[r] = val
		if DEBUG {
			fmt.Printf("  [REG] x%d (%s) = 0x%08x (%d)\n", r, regNames[r], val, int32(val))
		}
	}
}

func signExtend(val uint32, bitPos int) uint32 {
	if (val & (1 << bitPos)) != 0 {
		mask := ^uint32(0) << (bitPos + 1)
		return val | mask
	}
	return val
}

func (c *CPU) handleSyscall() {
	syscallNum := c.getReg(17) // a7
	arg := c.getReg(10)        // a0

	if DEBUG {
		fmt.Printf("  [SYSCALL] num=%d, a0=0x%08x\n", syscallNum, arg)
	}

	switch syscallNum {
	case SYS_OPEN:
		// a0 = buffer pointer, a1 = length
		bufAddr := arg
		length := c.getReg(11)

		filename := make([]byte, length)
		for i := uint32(0); i < length; i++ {
			if bufAddr+i < uint32(len(c.memory)) {
				filename[i] = c.memory[bufAddr+i]
			}
		}

		if showFileSyscalls {
			fmt.Printf("[SYS|OPEN]: %s\n", string(filename))
		}
		file, err := os.Open(string(filename))
		if err != nil {
			c.setReg(10, SYSCALL_ERROR)
		} else {
			fd := c.nextFd
			c.files[fd] = file
			c.nextFd++
			c.setReg(10, uint32(fd))
		}

	case SYS_READ:
		// a0 = fd, a1 = buffer, a2 = size
		fd := int(arg)
		bufAddr := c.getReg(11)
		size := c.getReg(12)

		if showFileSyscalls {
			fmt.Printf("[SYS|READ]: fd: %d buf: %d size: %d ins: %d\n", fd, bufAddr, size, c.instCount)
		}
		if file, ok := c.files[fd]; ok {
			buf := make([]byte, size)
			n, err := file.Read(buf)
			if err != nil && err != io.EOF {
				c.setReg(10, SYSCALL_ERROR)
			} else {
				for i := 0; i < n; i++ {
					if bufAddr+uint32(i) < uint32(len(c.memory)) {
						c.memory[bufAddr+uint32(i)] = buf[i]
					}
				}
				c.setReg(10, uint32(n))
			}
		} else {
			c.setReg(10, SYSCALL_ERROR)
		}

	case SYS_SEEK:
		// a0 = fd, a1 = offset, a2 = whence
		fd := int(arg)
		offset := int64(c.getReg(11))
		whence := int(c.getReg(12))

		if showFileSyscalls {
			fmt.Printf("[SYS|SEEK]: fd: %d offset: %d whence: %d ins: %d\n", fd, offset, whence, c.instCount)
		}
		if file, ok := c.files[fd]; ok {
			pos, err := file.Seek(offset, whence)
			if err != nil {
				c.setReg(10, SYSCALL_ERROR)
			} else {
				c.setReg(10, uint32(pos))
			}
		} else {
			c.setReg(10, SYSCALL_ERROR)
		}

	case SYS_CLOSE:
		// a0 = fd
		fd := int(arg)
		if showFileSyscalls {
			fmt.Printf("[SYS|CLOSE]: fd: %d\n", fd)
		}
		if file, ok := c.files[fd]; ok {
			err := file.Close()
			delete(c.files, fd)
			if err != nil {
				c.setReg(10, SYSCALL_ERROR)
			} else {
				c.setReg(10, 0)
			}
		} else {
			c.setReg(10, SYSCALL_ERROR)
		}

	case SYS_PRINT:
		// a0 = buffer pointer, a1 = length
		bufAddr := arg
		length := c.getReg(11)

		str := make([]byte, length)
		for i := uint32(0); i < length; i++ {
			if bufAddr+i < uint32(len(c.memory)) {
				str[i] = c.memory[bufAddr+i]
			}
		}

		if strings.Contains(string(str), "=== EXIT ===") {
			c.stop = true
		} else if strings.Contains(string(str), "DRAW FRAME") {
			c.frameCount++
			fmt.Printf("DRAW FRAME: %d ins: %d\n", c.frameCount, c.instCount)
			if exitAfterFrames > 0 && c.frameCount == exitAfterFrames {
				c.stop = true
			}
		} else if strings.Contains(string(str), "[Z_Malloc]") {
			fmt.Printf("%s ins: %d\n", string(str), c.instCount)
		} else {
			fmt.Printf("[SYS|PRINT]: %s\n", string(str))
		}
		c.setReg(10, 0)
	}
}

func (c *CPU) Execute(exit chan os.Signal) {
	for !c.stop {
		select {
		case <-exit:
			c.stopReason = "stop requested"
			return
		default:
		}

		if exitAfterCycleCount > 0 && c.instCount == exitAfterCycleCount {
			c.stop = true
			continue
		}

		if emulatorVerify {
			chPC, err := ReadClickHousePC(c.conn)
			if err != nil {
				log.Fatalln(err)
			}

			if c.pc != chPC {
				c.stopReason = fmt.Sprintf("Mismatched PC! A: (0x%08x) B: (0x%08x)", c.pc, chPC)
				return
			}

			chRegisters, err := ReadClickHouseRegisters(c.conn)
			if err != nil {
				log.Fatalln(err)
			}

			ok := compareRegisters(c.regs, chRegisters)
			if !ok {
				c.stopReason = "Register mismatch!"
				return
			}
		}

		if c.pc >= uint32(len(c.memory)-4) {
			c.stopReason = fmt.Sprintf("PC (0x%08x) reached end of memory", c.pc)
			break
		}

		inst := c.readMem(c.pc)

		if inst == 0 {
			c.stopReason = fmt.Sprintf("Hit zero instruction at PC 0x%08x (likely end of program)", c.pc)
			break
		}

		c.instCount++
		opcode := inst & 0x7F

		if DEBUG {
			fmt.Printf("[%d] PC=0x%08x INST=0x%08x OP=0x%02x: ", c.instCount, c.pc, inst, opcode)
		}

		switch opcode {
		case 0x37: // LUI
			rd := int((inst >> 7) & 0x1F)
			imm := inst & 0xFFFFF000
			if DEBUG {
				fmt.Printf("LUI x%d, 0x%05x\n", rd, imm>>12)
			}
			c.setReg(rd, imm)

		case 0x17: // AUIPC
			rd := int((inst >> 7) & 0x1F)
			imm := inst & 0xFFFFF000
			if DEBUG {
				fmt.Printf("AUIPC x%d, 0x%05x\n", rd, imm>>12)
			}
			c.setReg(rd, c.pc+imm)

		case 0x6F: // JAL
			rd := int((inst >> 7) & 0x1F)
			// Fixed JAL immediate decoding
			imm := ((inst & 0x80000000) >> 11) | // bit 20 (sign)
				(inst & 0x000FF000) | // bits 19:12
				((inst & 0x00100000) >> 9) | // bit 11
				((inst & 0x7FE00000) >> 20) // bits 10:1
			imm = signExtend(imm, 20)
			if DEBUG {
				fmt.Printf("JAL x%d, 0x%x (target=0x%08x)\n", rd, int32(imm), c.pc+imm)
			}
			c.setReg(rd, c.pc+4)
			c.pc = c.pc + imm - 4 // Subtract 4 because we'll add 4 at the end

		case 0x67: // JALR
			rd := int((inst >> 7) & 0x1F)
			rs1 := int((inst >> 15) & 0x1F)
			imm := inst >> 20
			imm = signExtend(imm, 11)
			target := (c.getReg(rs1) + imm) &^ 1 // Clear LSB
			if DEBUG {
				fmt.Printf("JALR x%d, x%d, %d (target=0x%08x)\n", rd, rs1, int32(imm), target)
			}
			c.setReg(rd, c.pc+4)
			c.pc = target - 4 // Subtract 4 because we'll add 4 at the end

		case 0x63: // Branch
			funct3 := (inst >> 12) & 0x7
			rs1 := int((inst >> 15) & 0x1F)
			rs2 := int((inst >> 20) & 0x1F)
			// Fixed branch immediate decoding
			imm := ((inst & 0x80000000) >> 19) | // bit 12 (sign)
				((inst & 0x00000080) << 4) | // bit 11
				((inst & 0x7E000000) >> 20) | // bits 10:5
				((inst & 0x00000F00) >> 7) // bits 4:1
			imm = signExtend(imm, 12)

			var takeBranch bool
			var branchType string
			switch funct3 {
			case 0x0: // BEQ
				branchType = "BEQ"
				takeBranch = c.getReg(rs1) == c.getReg(rs2)
			case 0x1: // BNE
				branchType = "BNE"
				takeBranch = c.getReg(rs1) != c.getReg(rs2)
			case 0x4: // BLT
				branchType = "BLT"
				takeBranch = int32(c.getReg(rs1)) < int32(c.getReg(rs2))
			case 0x5: // BGE
				branchType = "BGE"
				takeBranch = int32(c.getReg(rs1)) >= int32(c.getReg(rs2))
			case 0x6: // BLTU
				branchType = "BLTU"
				takeBranch = c.getReg(rs1) < c.getReg(rs2)
			case 0x7: // BGEU
				branchType = "BGEU"
				takeBranch = c.getReg(rs1) >= c.getReg(rs2)
			}

			if DEBUG {
				fmt.Printf("%s x%d, x%d, 0x%x (taken=%v)\n", branchType, rs1, rs2, int32(imm), takeBranch)
			}

			if takeBranch {
				c.pc = c.pc + imm - 4
			}

		case 0x03: // Load
			rd := int((inst >> 7) & 0x1F)
			funct3 := (inst >> 12) & 0x7
			rs1 := int((inst >> 15) & 0x1F)
			imm := inst >> 20
			imm = signExtend(imm, 11)
			addr := c.getReg(rs1) + imm

			var loadType string
			switch funct3 {
			case 0x0: // LB
				loadType = "LB"
				val := uint32(c.memory[addr])
				if addr > uint32(len(c.memory)) {
					fmt.Printf("Reading i8 outside of memory addr=0x%08x (%d)\n", addr, addr)
				}
				c.setReg(rd, signExtend(val, 7))
			case 0x1: // LH
				loadType = "LH"
				val := uint32(binary.LittleEndian.Uint16(c.memory[addr:]))
				if addr+2 > uint32(len(c.memory)) {
					fmt.Printf("Reading i16 outside of memory addr=0x%08x (%d)\n", addr, addr)
				}
				c.setReg(rd, signExtend(val, 15))
			case 0x2: // LW
				loadType = "LW"
				c.setReg(rd, c.readMem(addr))
			case 0x4: // LBU
				loadType = "LBU"
				if addr > uint32(len(c.memory)) {
					fmt.Printf("Reading u8 outside of memory addr=0x%08x (%d)\n", addr, addr)
				}
				c.setReg(rd, uint32(c.memory[addr]))
			case 0x5: // LHU
				loadType = "LHU"
				if addr+2 > uint32(len(c.memory)) {
					fmt.Printf("Reading u16 outside of memory addr=0x%08x (%d)\n", addr, addr)
				}
				c.setReg(rd, uint32(binary.LittleEndian.Uint16(c.memory[addr:])))
			}

			if DEBUG {
				fmt.Printf("%s x%d, %d(x%d) [addr=0x%08x]\n", loadType, rd, int32(imm), rs1, addr)
			}

		case 0x23: // Store
			funct3 := (inst >> 12) & 0x7
			rs1 := int((inst >> 15) & 0x1F)
			rs2 := int((inst >> 20) & 0x1F)
			imm := ((inst>>25)&0x7F)<<5 | ((inst >> 7) & 0x1F)
			imm = signExtend(imm, 11)
			addr := c.getReg(rs1) + imm

			if showAddrWarn && addr < programSpaceAddr {
				fmt.Printf("WARNING: Writing to program code at 0x%x, value=0x%x, PC=0x%x, SP=0x%x\n", addr, c.getReg(rs2), c.pc, c.getReg(2))
			}
			// if addr > framebufferAddr && c.getReg(rs2) != 0 {
			// 	fmt.Printf("Writing to frame buffer at 0x%x, value=0x%x, PC=0x%x, SP=0x%x\n", addr, c.getReg(rs2), c.pc, c.getReg(2))
			// }

			var storeType string
			switch funct3 {
			case 0x0: // SB
				storeType = "SB"
				c.memory[addr] = byte(c.getReg(rs2))
			case 0x1: // SH
				storeType = "SH"
				binary.LittleEndian.PutUint16(c.memory[addr:], uint16(c.getReg(rs2)))
			case 0x2: // SW
				storeType = "SW"
				c.writeMem(addr, c.getReg(rs2))
			}

			if DEBUG {
				fmt.Printf("%s x%d, %d(x%d) [addr=0x%08x, val=0x%08x]\n",
					storeType, rs2, int32(imm), rs1, addr, c.getReg(rs2))
			}

		case 0x13: // Immediate arithmetic
			rd := int((inst >> 7) & 0x1F)
			funct3 := (inst >> 12) & 0x7
			rs1 := int((inst >> 15) & 0x1F)
			imm := inst >> 20
			imm = signExtend(imm, 11)

			var instType string
			switch funct3 {
			case 0x0: // ADDI
				instType = "ADDI"
				c.setReg(rd, c.getReg(rs1)+imm)
			case 0x2: // SLTI
				instType = "SLTI"
				if int32(c.getReg(rs1)) < int32(imm) {
					c.setReg(rd, 1)
				} else {
					c.setReg(rd, 0)
				}
			case 0x3: // SLTIU
				instType = "SLTIU"
				if c.getReg(rs1) < imm {
					c.setReg(rd, 1)
				} else {
					c.setReg(rd, 0)
				}
			case 0x4: // XORI
				instType = "XORI"
				c.setReg(rd, c.getReg(rs1)^imm)
			case 0x6: // ORI
				instType = "ORI"
				c.setReg(rd, c.getReg(rs1)|imm)
			case 0x7: // ANDI
				instType = "ANDI"
				c.setReg(rd, c.getReg(rs1)&imm)
			case 0x1: // SLLI
				shamt := imm & 0x1F
				instType = fmt.Sprintf("SLLI (shamt=%d)", shamt)
				c.setReg(rd, c.getReg(rs1)<<shamt)
			case 0x5: // SRLI/SRAI
				shamt := imm & 0x1F
				if (imm & 0x400) != 0 { // SRAI
					instType = fmt.Sprintf("SRAI (shamt=%d)", shamt)
					c.setReg(rd, uint32(int32(c.getReg(rs1))>>shamt))
				} else { // SRLI
					instType = fmt.Sprintf("SRLI (shamt=%d)", shamt)
					c.setReg(rd, c.getReg(rs1)>>shamt)
				}
			}

			if DEBUG {
				fmt.Printf("%s x%d, x%d, %d\n", instType, rd, rs1, int32(imm))
			}

		case 0x33: // Register arithmetic
			rd := int((inst >> 7) & 0x1F)
			funct3 := (inst >> 12) & 0x7
			rs1 := int((inst >> 15) & 0x1F)
			rs2 := int((inst >> 20) & 0x1F)
			funct7 := inst >> 25

			var instType string
			if funct7 == 1 { // M extension
				switch funct3 {
				case 0x0: // MUL
					instType = "MUL"
					c.setReg(rd, c.getReg(rs1)*c.getReg(rs2))
				case 0x1: // MULH
					instType = "MULH"
					result := int64(int32(c.getReg(rs1))) * int64(int32(c.getReg(rs2)))
					c.setReg(rd, uint32(result>>32))
				case 0x2: // MULHSU
					instType = "MULHSU"
					result := int64(int32(c.getReg(rs1))) * int64(c.getReg(rs2))
					c.setReg(rd, uint32(result>>32))
				case 0x3: // MULHU
					instType = "MULHU"
					result := uint64(c.getReg(rs1)) * uint64(c.getReg(rs2))
					c.setReg(rd, uint32(result>>32))
				case 0x4: // DIV
					instType = "DIV"
					if c.getReg(rs2) != 0 {
						c.setReg(rd, uint32(int32(c.getReg(rs1))/int32(c.getReg(rs2))))
					} else {
						c.setReg(rd, 0xFFFFFFFF)
					}
				case 0x5: // DIVU
					instType = "DIVU"
					if c.getReg(rs2) != 0 {
						c.setReg(rd, c.getReg(rs1)/c.getReg(rs2))
					} else {
						c.setReg(rd, 0xFFFFFFFF)
					}
				case 0x6: // REM
					instType = "REM"
					if c.getReg(rs2) != 0 {
						c.setReg(rd, uint32(int32(c.getReg(rs1))%int32(c.getReg(rs2))))
					} else {
						c.setReg(rd, c.getReg(rs1))
					}
				case 0x7: // REMU
					instType = "REMU"
					if c.getReg(rs2) != 0 {
						c.setReg(rd, c.getReg(rs1)%c.getReg(rs2))
					} else {
						c.setReg(rd, c.getReg(rs1))
					}
				}
			} else {
				switch funct3 {
				case 0x0: // ADD/SUB
					if funct7 == 0x20 {
						instType = "SUB"
						c.setReg(rd, c.getReg(rs1)-c.getReg(rs2))
					} else {
						instType = "ADD"
						c.setReg(rd, c.getReg(rs1)+c.getReg(rs2))
					}
				case 0x1: // SLL
					instType = "SLL"
					c.setReg(rd, c.getReg(rs1)<<(c.getReg(rs2)&0x1F))
				case 0x2: // SLT
					instType = "SLT"
					if int32(c.getReg(rs1)) < int32(c.getReg(rs2)) {
						c.setReg(rd, 1)
					} else {
						c.setReg(rd, 0)
					}
				case 0x3: // SLTU
					instType = "SLTU"
					if c.getReg(rs1) < c.getReg(rs2) {
						c.setReg(rd, 1)
					} else {
						c.setReg(rd, 0)
					}
				case 0x4: // XOR
					instType = "XOR"
					c.setReg(rd, c.getReg(rs1)^c.getReg(rs2))
				case 0x5: // SRL/SRA
					if funct7 == 0x20 {
						instType = "SRA"
						c.setReg(rd, uint32(int32(c.getReg(rs1))>>(c.getReg(rs2)&0x1F)))
					} else {
						instType = "SRL"
						c.setReg(rd, c.getReg(rs1)>>(c.getReg(rs2)&0x1F))
					}
				case 0x6: // OR
					instType = "OR"
					c.setReg(rd, c.getReg(rs1)|c.getReg(rs2))
				case 0x7: // AND
					instType = "AND"
					c.setReg(rd, c.getReg(rs1)&c.getReg(rs2))
				}
			}

			if DEBUG {
				fmt.Printf("%s x%d, x%d, x%d\n", instType, rd, rs1, rs2)
			}

		case 0x73: // System
			funct3 := (inst >> 12) & 0x7
			if funct3 == 0 {
				imm := inst >> 20
				if imm == 0 { // ECALL
					if DEBUG {
						fmt.Printf("ECALL\n")
					}
					c.handleSyscall()
				} else if imm == 1 { // EBREAK
					if DEBUG {
						fmt.Printf("EBREAK\n")
					}
					c.stopReason = "EBREAK instruction"
					return
				}
			}

		default:
			c.stopReason = fmt.Sprintf("Unknown opcode 0x%02x at PC %d (0x%08x)", opcode, c.pc, c.pc)
			return
		}

		c.pc += 4

		if emulatorVerify {
			err := RunClickHouseClockCycle(c.conn)
			if err != nil {
				log.Fatalln(err)
			}
			fmt.Printf("Total instructions executed: %d\n", c.instCount)
		}
	}
}

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	filename := "doom.bin"

	cpu := NewCPU((1536 * 1024))

	var err error
	cpu.conn, err = ConnectClickHouse()
	if err != nil {
		log.Fatal(err)
	}
	defer cpu.conn.Close()

	if err := cpu.LoadProgram(filename); err != nil {
		log.Fatal("Failed to load program:", err)
	}

	fmt.Println("Starting execution...")
	fmt.Println("----------------------------------------")

	// cpu.Execute(sigs)

	// err = BootstrapCPU(cpu.conn, cpu)
	// if err != nil {
	// 	log.Fatal("Failed to bootstrap program:", err)
	// }

	// emulatorVerify = true
	// exitAfterFrames = 0
	// exitAfterCycleCount = 0
	// cpu.stop = false
	// cpu.stopReason = ""
	// cpu.Execute(sigs)

	serverBuffer, err := ReadClickHouseMemoryRange(cpu.conn, framebufferAddr, framebufferAddr+DOOMGENERIC_RESX*DOOMGENERIC_RESY*4)
	if err != nil {
		log.Fatal("Failed to read memory from server program:", err)
	}
	SaveDoomFrame(serverBuffer, "doom_test.png")

	// SaveDoomFrame(cpu.memory[framebufferAddr:], "doom_test.png")

	fmt.Println("----------------------------------------")
	fmt.Printf("Execution stopped: %s\n", cpu.stopReason)
	fmt.Printf("PC=0x%x, SP=0x%x\n", cpu.pc, cpu.getReg(2))
	fmt.Printf("Total instructions executed: %d\n", cpu.instCount)
	for _, openFile := range cpu.files {
		fileName := openFile.Name()
		currentSeek, _ := openFile.Seek(0, 1)
		fmt.Printf("Open file: name='%s', seek=%d\n", fileName, currentSeek)
	}
	fmt.Println("----------------------------------------")
	// for i := 0; i < 32; i++ {
	// 	if cpu.regs[i] != 0 || i == 2 {
	// 		fmt.Printf("x%-2d (%-6s): 0x%08x (%d)\n",
	// 			i, regNames[i], cpu.regs[i], int32(cpu.regs[i]))
	// 	}
	// }

}
