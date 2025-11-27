package main

import (
	"bufio"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"time"

	"clickhouse.com/clickv/internal/clickos"
)

// Volume bind mounts
// clickos_syscall_function.xml -> /etc/clickhouse-server/clickos_syscall_function.xml
// clickos_client --> /var/lib/clickhouse/user_scripts/clickos_client

const defaultServerAddr = "localhost:9008"

func main() {
	logFile := setupLogging()
	defer logFile.Close()

	serverAddr := defaultServerAddr
	if len(os.Args) > 1 {
		serverAddr = os.Args[1]
	}

	msgIn := make(chan string)
	msgOut := make(chan string)
	done := make(chan struct{})

	go startOSClient(serverAddr, msgIn, msgOut, done)
	go startScanner(msgIn, done)

	for res := range msgOut {
		WriteStringResponse(res)
	}

	log.Println("exiting")
}

// setupLogging configures the logger to write to a file, since STDOUT is used for ClickHouse<->UDF communication.
func setupLogging() *os.File {
	file, err := os.OpenFile("clickos_client.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

	log.SetPrefix(fmt.Sprintf("clickos(id=%d) ", rand.Intn(1000)))
	log.SetOutput(file)

	return file
}

func startOSClient(serverAddr string, msgIn <-chan string, msgOut chan<- string, done <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("client panic:", r)
		}
		close(msgOut)
	}()

	resolvedAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		log.Fatalln("failed to resolve host address:", err)
		return
	}

	conn, err := net.DialUDP("udp", nil, resolvedAddr)
	if err != nil {
		log.Fatalln("failed to dial UDP:", err)
		return
	}
	defer conn.Close()

	log.Println("connected to OS server:", serverAddr)

	for {
		select {
		case line, ok := <-msgIn:
			if !ok {
				return
			}
			log.Printf("received input: %s\n", line)

			err = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err != nil {
				log.Println("failed to set write deadline:", err)
				msgOut <- GetSyscallFailedResponse()
				continue
			}

			_, err := conn.Write([]byte(line))
			if err != nil {
				log.Println("failed to write request to OS:", err)
				msgOut <- GetSyscallFailedResponse()
				continue
			}

			err = conn.SetReadDeadline(time.Time{})
			if err != nil {
				log.Println("failed to reset write deadline:", err)
				msgOut <- GetSyscallFailedResponse()
				continue
			}

			buffer := make([]byte, 64*1024)
			err = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if err != nil {
				log.Println("failed to set read deadline:", err)
				msgOut <- GetSyscallFailedResponse()
				continue
			}

			n, _, err := conn.ReadFromUDP(buffer)
			if err != nil {
				log.Println("failed to read response from OS:", err)
				msgOut <- GetSyscallFailedResponse()
				continue
			}
			err = conn.SetReadDeadline(time.Time{})
			if err != nil {
				log.Println("failed to reset read deadline:", err)
				msgOut <- GetSyscallFailedResponse()
				continue
			}

			response := string(buffer[:n])
			msgOut <- response

		case <-done:
			return
		}
	}
}

func startScanner(msgIn chan<- string, done chan<- struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("scan panic:", r)
		}
		close(msgIn)
		close(done)
	}()

	log.Println("starting scanner")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		msgIn <- line
	}
	if err := scanner.Err(); err != nil {
		log.Println("scan error:", err)
	}

	log.Println("scanner done")
}

func GetSyscallFailedResponse() string {
	res := clickos.SyscallResponse{
		SyscallN: 0xDEAD,
		Status:   -1,
	}
	return string(res.Serialize())
}

func WriteStringResponse(res string) {
	log.Printf("response: %s\n", res)
	fmt.Println(res)
}
