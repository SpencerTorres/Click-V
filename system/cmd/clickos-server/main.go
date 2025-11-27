package main

import (
	"fmt"
	"log"
	"net"

	"clickhouse.com/clickv/internal/clickos"
)

const hostAddress = "0.0.0.0:9008"

func main() {
	resolvedAddr, err := net.ResolveUDPAddr("udp", hostAddress)
	if err != nil {
		log.Fatalf("failed to resolve UDP host address: %v", err)
	}

	conn, err := net.ListenUDP("udp", resolvedAddr)
	if err != nil {
		log.Fatalf("failed to listen on UDP: %v", err)
	}
	defer conn.Close()
	log.Println("ClickOS listening on", hostAddress)

	// bootstrap fd for doom
	// err = clickos.BootstrapOpenFile("doom1.wad", 3991051)
	// if err != nil {
	// 	log.Fatalf("failed to bootstrap file: %v", err)
	// }

	buffer := make([]byte, 64*1024)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("failed to read from UDP: %v", err)
			continue
		}

		payload := buffer[:n]
		log.Printf("received from %s: %v\n", clientAddr.String(), payload)
		resp, err := handlePacket(clientAddr.String(), payload)
		if err != nil {
			log.Printf("failed to handle packet: %v", err)
			errResp := &clickos.SyscallResponse{Status: -1}
			resp = errResp.Serialize()
		}

		_, err = conn.WriteToUDP(resp, clientAddr)
		if err != nil {
			log.Printf("failed to send response: %v", err)
		}
	}
}

func handlePacket(clientAddr string, payload []byte) ([]byte, error) {
	req, err := clickos.ParseInputTSV(string(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to parse input: %w", err)
	}

	log.Printf("client: %s %s\n", clientAddr, req.DebugString())
	resp, err := clickos.MuxCall(req)
	if err != nil {
		return nil, fmt.Errorf("syscall %s (%d) failed: %w", clickos.SyscallToName(req.SyscallN), req.SyscallN, err)
	}

	respBytes := resp.Serialize()
	log.Printf("response(%d): %s\n", len(respBytes), resp.DebugString())
	return respBytes, nil
}
