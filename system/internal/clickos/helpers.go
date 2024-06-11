package clickos

import (
	"fmt"
	"strconv"
	"strings"
)

func ReadCString(input []byte) string {
	n := 0
	for n < len(input) && input[n] != 0 {
		n++
	}
	return string(input[:n])
}

func ParseInputTSV(input string) (*SyscallRequest, error) {
	parts := strings.Split(input, "\t")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid args")
	}

	syscallNum64, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid number format for syscall num: %v", err)
	}
	syscallNum := uint32(syscallNum64)

	byteArrayStr := strings.Trim(parts[1], "[]")
	byteStrArray := strings.Split(byteArrayStr, ",")
	bytes := make([]byte, len(byteStrArray))

	for i, byteStr := range byteStrArray {
		if byteStr == "" {
			continue
		}

		byte64, err := strconv.ParseUint(strings.TrimSpace(byteStr), 10, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid byte array element format: %v", err)
		}
		bytes[i] = byte(byte64)
	}

	return &SyscallRequest{
		SyscallN: syscallNum,
		Bytes:    bytes,
	}, nil
}
