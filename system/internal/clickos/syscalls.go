package clickos

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
)

const SYSCALL_FAILED uint32 = 0xDEAD
const SYSCALL_RESET uint32 = 0
const SYSCALL_OPEN uint32 = 10
const SYSCALL_CLOSE uint32 = 11
const SYSCALL_SEEK uint32 = 12
const SYSCALL_READ uint32 = 13
const SYSCALL_WRITE uint32 = 14
const SYSCALL_SOCKET uint32 = 15

func SyscallToName(syscallN uint32) string {
	switch syscallN {
	case SYSCALL_RESET:
		return "RESET"
	case SYSCALL_OPEN:
		return "OPEN"
	case SYSCALL_CLOSE:
		return "CLOSE"
	case SYSCALL_SEEK:
		return "SEEK"
	case SYSCALL_READ:
		return "READ"
	case SYSCALL_WRITE:
		return "WRITE"
	case SYSCALL_SOCKET:
		return "SOCKET"
	case SYSCALL_FAILED:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

type SyscallRequest struct {
	SyscallN uint32
	Bytes    []byte
}

func (r *SyscallRequest) DebugString() string {
	return fmt.Sprintf("syscall: %s (%d), bytes: %v", SyscallToName(r.SyscallN), r.SyscallN, r.Bytes)
}

type SyscallResponse struct {
	SyscallN uint32
	Status   int32
	Bytes    []byte
}

func (r *SyscallResponse) DebugString() string {
	return fmt.Sprintf("syscall: %s (%d), status: %d, bytes(%d): %v", SyscallToName(r.SyscallN), r.SyscallN, r.Status, len(r.Bytes), r.Bytes)
}

func (r *SyscallResponse) Serialize() []byte {
	output := make([]byte, 4+len(r.Bytes)) // status + payload
	binary.LittleEndian.PutUint32(output, uint32(r.Status))
	copy(output[4:], r.Bytes)

	var buffer bytes.Buffer
	buffer.WriteString("[")
	for i, b := range output {
		if i > 0 {
			buffer.WriteString(",")
		}
		buffer.WriteString(strconv.FormatUint(uint64(b), 10))
	}
	buffer.WriteString("]")

	return buffer.Bytes()
}

func MuxCall(req *SyscallRequest) (*SyscallResponse, error) {
	switch req.SyscallN {
	case SYSCALL_RESET:
		return handleResetCall()
	case SYSCALL_OPEN:
		call, err := decodeOpenCall(req.Bytes)
		if err != nil {
			return nil, err
		}
		return handleOpenCall(call)
	case SYSCALL_CLOSE:
		call, err := decodeCloseCall(req.Bytes)
		if err != nil {
			return nil, err
		}
		return handleCloseCall(call)
	case SYSCALL_SEEK:
		call, err := decodeSeekCall(req.Bytes)
		if err != nil {
			return nil, err
		}
		return handleSeekCall(call)
	case SYSCALL_READ:
		call, err := decodeReadCall(req.Bytes)
		if err != nil {
			return nil, err
		}
		return handleReadCall(call)
	case SYSCALL_WRITE:
		call, err := decodeWriteCall(req.Bytes)
		if err != nil {
			return nil, err
		}
		return handleWriteCall(call)
	case SYSCALL_SOCKET:
		call, err := decodeSocketCall(req.Bytes)
		if err != nil {
			return nil, err
		}
		return handleSocketCall(call)
	case SYSCALL_FAILED:
	default:
		return nil, fmt.Errorf("unknown syscall number")
	}

	return nil, nil
}

var fileDescriptors = make(map[int32]*fileDescriptor, 0)

type descriptorType uint8

const FD_FILE descriptorType = 0
const FD_PIPE descriptorType = 1

var fdSequence int32 = 2 // start after stdin/stdout/stderr

type fileDescriptor struct {
	id    int32
	seek  int32
	dType descriptorType
	name  string
	file  *os.File
	pipe  *udpPipe
}

func init() {
	file := os.Stdin

	fileDescriptors[0] = &fileDescriptor{
		id:    int32(file.Fd()),
		seek:  0,
		dType: FD_FILE,
		file:  file,
	}
}

func getNextFdID() int32 {
	fdSequence++
	return fdSequence
}

func handleResetCall() (*SyscallResponse, error) {
	for _, fd := range fileDescriptors {
		fd.file.Close()
	}

	fileDescriptors = make(map[int32]*fileDescriptor, 0)
	fdSequence = 0

	return &SyscallResponse{
		SyscallN: SYSCALL_RESET,
	}, nil
}

type openCall struct {
	pathName string
	flags    int32
}

// When bootstrapping the emulator with existing pc/registers/memory, we must also replicate any open files
func BootstrapOpenFile(pathName string, seekOffset int64) error {
	resp, err := handleOpenCall(openCall{pathName: pathName})
	currentSeek, seekErr := fileDescriptors[resp.Status].file.Seek(seekOffset, 0)
	if seekErr != nil {
		return seekErr
	}

	log.Printf("bootstrapping file open: %s fd: %d requested seek: %d current seek: %d\n", pathName, resp.Status, seekOffset, currentSeek)
	return err
}

func decodeOpenCall(bytes []byte) (openCall, error) {
	offset := 0
	pathName := ReadCString(bytes)
	offset += len(pathName) + 1
	if len(bytes) < offset+4 {
		return openCall{}, fmt.Errorf("invalid open call: payload too short")
	}

	flags := int32(binary.LittleEndian.Uint32(bytes[offset : offset+4]))
	offset += 4

	log.Printf("OPEN: pathName: '%s' flags: %d\n", pathName, flags)
	return openCall{pathName, flags}, nil
}

func handleOpenCall(call openCall) (*SyscallResponse, error) {
	fd := fileDescriptor{
		id:    getNextFdID(),
		seek:  0,
		dType: FD_FILE,
		name:  call.pathName,
	}

	file, err := os.OpenFile(call.pathName, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Println(fmt.Errorf("failed to open file: %w", err))
		return &SyscallResponse{
			SyscallN: SYSCALL_OPEN,
			Status:   -1,
		}, nil
	}

	fd.file = file

	fileDescriptors[fd.id] = &fd
	return &SyscallResponse{
		SyscallN: SYSCALL_OPEN,
		Status:   fd.id,
	}, nil
}

type closeCall struct {
	fd int32
}

func decodeCloseCall(bytes []byte) (closeCall, error) {
	if len(bytes) < 4 {
		return closeCall{}, fmt.Errorf("invalid close call: payload too short")
	}

	offset := 0
	fd := int32(binary.LittleEndian.Uint32(bytes[offset : offset+4]))
	offset += 4

	log.Printf("CLOSE: fd: %d\n", fd)
	return closeCall{fd}, nil
}

func handleCloseCall(call closeCall) (*SyscallResponse, error) {
	fd, ok := fileDescriptors[call.fd]
	if !ok {
		return nil, fmt.Errorf("file descriptor %d not found", call.fd)
	}

	if fd.dType == FD_FILE {
		err := fd.file.Close()
		if err != nil {
			log.Println(fmt.Errorf("failed to close file: %w", err))
			return &SyscallResponse{
				SyscallN: SYSCALL_CLOSE,
				Status:   -1,
			}, nil
		}
	} else if fd.dType == FD_PIPE {
		err := fd.pipe.Close()
		if err != nil {
			log.Println(fmt.Errorf("failed to close UDP pipe: %w", err))
			return &SyscallResponse{
				SyscallN: SYSCALL_CLOSE,
				Status:   -1,
			}, nil
		}
	}

	delete(fileDescriptors, call.fd)
	return &SyscallResponse{
		SyscallN: SYSCALL_CLOSE,
		Status:   0,
	}, nil
}

type seekCall struct {
	fd     int32
	offset int32
	whence int32
}

func decodeSeekCall(bytes []byte) (seekCall, error) {
	if len(bytes) < (4 + 4 + 4) {
		return seekCall{}, fmt.Errorf("invalid seek call: payload too short")
	}

	offset := 0
	fd := int32(binary.LittleEndian.Uint32(bytes[offset : offset+4]))
	offset += 4
	seekOffset := int32(binary.LittleEndian.Uint32(bytes[offset : offset+4]))
	offset += 4
	whence := int32(binary.LittleEndian.Uint32(bytes[offset : offset+4])) // whence...
	offset += 4

	log.Printf("SEEK: fd: %d offset: %d whence: %d\n", fd, seekOffset, whence)
	return seekCall{
		fd:     fd,
		offset: seekOffset,
		whence: whence,
	}, nil
}

func handleSeekCall(call seekCall) (*SyscallResponse, error) {
	fd, ok := fileDescriptors[call.fd]
	if !ok {
		return nil, fmt.Errorf("file descriptor %d not found", call.fd)
	} else if fd.dType != FD_FILE {
		return nil, fmt.Errorf("cannot seek: file descriptor %d is not a file", call.fd)
	}

	current, err := fd.file.Seek(int64(call.offset), int(call.whence))
	if err != nil {
		log.Println(fmt.Errorf("failed to seek file: %w", err))
		return &SyscallResponse{
			SyscallN: SYSCALL_SEEK,
			Status:   -1,
		}, nil

	}

	fd.seek = int32(current)
	return &SyscallResponse{
		SyscallN: SYSCALL_SEEK,
		Status:   int32(current),
	}, nil
}

type readCall struct {
	fd    int32
	count uint32
}

func decodeReadCall(bytes []byte) (readCall, error) {
	if len(bytes) < (4 + 4) {
		return readCall{}, fmt.Errorf("invalid read call: payload too short")
	}

	offset := 0
	fd := int32(binary.LittleEndian.Uint32(bytes[offset : offset+4]))
	offset += 4
	count := binary.LittleEndian.Uint32(bytes[offset : offset+4])
	offset += 4

	log.Printf("READ: fd: %d count: %d\n", fd, count)
	return readCall{fd, count}, nil
}

func handleReadCall(call readCall) (*SyscallResponse, error) {
	fd, ok := fileDescriptors[call.fd]
	if !ok {
		return nil, fmt.Errorf("file descriptor %d not found", call.fd)
	}

	buf := make([]byte, call.count)
	var n int = 0
	var err error
	if fd.dType == FD_FILE {
		n, err = fd.file.Read(buf)
		if err != nil {
			log.Println(fmt.Errorf("failed to read file: %w", err))
			return &SyscallResponse{
				SyscallN: SYSCALL_READ,
				Status:   -1,
			}, nil
		}

		fd.seek += int32(n)
	} else if fd.dType == FD_PIPE {
		n, err = fd.pipe.Read(buf)
		if err != nil {
			log.Println(fmt.Errorf("failed to read UDP: %w", err))
			return &SyscallResponse{
				SyscallN: SYSCALL_READ,
				Status:   -1,
			}, nil
		}
	}

	return &SyscallResponse{
		SyscallN: SYSCALL_READ,
		Status:   int32(n),
		Bytes:    buf[:n],
	}, nil
}

type writeCall struct {
	fd    int32
	bytes []byte
}

func decodeWriteCall(bytes []byte) (writeCall, error) {
	if len(bytes) < 4 {
		return writeCall{}, fmt.Errorf("invalid write call: payload too short")
	}

	offset := 0
	fd := int32(binary.LittleEndian.Uint32(bytes[offset : offset+4]))
	offset += 4
	writeBytes := bytes[offset:]

	log.Printf("WRITE: fd: %d bytes count: %d\n", fd, len(writeBytes))
	return writeCall{
		fd:    fd,
		bytes: writeBytes,
	}, nil
}

func handleWriteCall(call writeCall) (*SyscallResponse, error) {
	fd, ok := fileDescriptors[call.fd]
	if !ok {
		log.Printf("fileDescriptors: %#v\n", fileDescriptors)
		return nil, fmt.Errorf("file descriptor %d not found", call.fd)
	}

	var n int = 0
	var err error
	if fd.dType == FD_FILE {
		n, err = fd.file.Write(call.bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to write file: %w", err)
		}

		fd.seek += int32(n)
	} else if fd.dType == FD_PIPE {
		n, err = fd.pipe.Write(call.bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to write UDP: %w", err)
		}
	}

	return &SyscallResponse{
		SyscallN: SYSCALL_WRITE,
		Status:   int32(n),
	}, nil
}

type socketCall struct {
	address string
}

func decodeSocketCall(bytes []byte) (socketCall, error) {
	offset := 0
	address := ReadCString(bytes)
	offset += len(address) + 1

	return socketCall{address}, nil
}

type udpPipe struct {
	fd      *fileDescriptor
	conn    *net.UDPConn
	packets chan []byte
	done    chan struct{}
	err     error
}

const PIPE_EAGAIN int32 = -64

func newUDPPipe(fd *fileDescriptor, conn *net.UDPConn) *udpPipe {
	return &udpPipe{
		conn:    conn,
		packets: make(chan []byte, 32),
		done:    make(chan struct{}),
	}
}

func (p *udpPipe) backgroundRead() {
	for {
		select {
		case <-p.done:
			return
		default:
			packet := make([]byte, 64*1024)
			n, _, err := p.conn.ReadFromUDP(packet)
			if err != nil {
				log.Printf("failed to read UDP: %v\n", err)
				p.err = err
				break
			}

			p.packets <- packet[:n]
		}
	}
}

func (p *udpPipe) Read(b []byte) (int, error) {
	select {
	case packet := <-p.packets:
		copy(b, packet)
		return len(packet), nil
	case <-p.done:
		return -1, p.err
	default:
		return int(PIPE_EAGAIN), nil
	}
}

func (p *udpPipe) Write(b []byte) (int, error) {
	n, err := p.conn.Write(b)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (p *udpPipe) Close() error {
	close(p.done)
	close(p.packets)
	return p.conn.Close()
}

func handleSocketCall(call socketCall) (*SyscallResponse, error) {
	fd := fileDescriptor{
		id:    getNextFdID(),
		seek:  0,
		dType: FD_PIPE,
		name:  call.address,
	}

	resolvedAddr, err := net.ResolveUDPAddr("udp", call.address)
	if err != nil {
		return nil, fmt.Errorf("failed to socket file: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, resolvedAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial UDP: %w", err)
	}

	fd.pipe = newUDPPipe(&fd, conn)
	go fd.pipe.backgroundRead()

	fileDescriptors[fd.id] = &fd
	return &SyscallResponse{
		SyscallN: SYSCALL_SOCKET,
		Status:   fd.id,
	}, nil
}
