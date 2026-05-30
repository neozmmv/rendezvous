package main

import (
	"fmt"
	"net"

	"github.com/pion/stun"
)

func main() {
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

	fmt.Printf("Listening on %s\n", conn.LocalAddr().String())
}
