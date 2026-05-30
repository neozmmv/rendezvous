package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/pion/stun"
)

func main() {
	var hostname string
	var session string
	fmt.Print("Enter hostname: ")
	fmt.Scanln(&hostname)
	fmt.Print("Enter session: ")
	fmt.Scanln(&session)

	// remove trailing slash if exists
	if hostname[len(hostname)-1] == '/' {
		hostname = hostname[:len(hostname)-1]
	}

	// open a UDP connection on a random port
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	// google stun server
	serverAddr, err := net.ResolveUDPAddr("udp", "stun.l.google.com:19302")
	if err != nil {
		panic(err)
	}

	// send a binding request to the stun server
	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	conn.WriteToUDP(msg.Raw, serverAddr)

	// read the response from the stun server
	buf := make([]byte, 1024)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		panic(err)
	}

	// decode the response and extract the public IP and port
	m := &stun.Message{Raw: buf[:n]}
	m.Decode()

	var xorAddr stun.XORMappedAddress
	xorAddr.GetFrom(m)

	fmt.Printf("Public addr: %s:%d\n", xorAddr.IP, xorAddr.Port)

	// post body
	body := map[string]string{
		"udp_addr": fmt.Sprintf("%s:%d", xorAddr.IP, xorAddr.Port),
	}

	bodyJson, err := json.Marshal(body)

	resp, err := http.Post(fmt.Sprintf("%s/session/%s", hostname, session), "application/json", bytes.NewBuffer(bodyJson))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	// read response
	var respBody map[string]string
	err = json.NewDecoder(resp.Body).Decode(&respBody)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Listening on %s\n", conn.LocalAddr().String())
	fmt.Printf("Peer address: %v", respBody["peer"])
}
