package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pion/stun"
)

var lastSeen time.Time

var Version = "dev"

func main() {
	var hostname string
	var session string
	var passwordInput string
	var createOrJoin string
	fmt.Print("Enter hostname (blank for default): ")
	fmt.Scanln(&hostname)
	fmt.Print("Create or join session? (c/j): ")
	fmt.Scanln(&createOrJoin)
	fmt.Print("Enter session: ")
	fmt.Scanln(&session)
	fmt.Print("Does the session require a password? (y/n): ")
	fmt.Scanln(&passwordInput)
	requiresPassword := strings.ToLower(passwordInput) == "y"
	create := strings.ToLower(createOrJoin) == "c"

	var password string
	if requiresPassword {
		fmt.Print("Enter password: ")
		fmt.Scanln(&password)
	}

	// remove trailing slash if exists
	if hostname != "" && hostname[len(hostname)-1] == '/' {
		hostname = hostname[:len(hostname)-1]
	}

	if hostname == "" {
		// fallback to default hostname if none provided
		hostname = "https://rendezvous.enzogp.dev"
	} else if !strings.HasPrefix(hostname, "http://") && !strings.HasPrefix(hostname, "https://") {
		hostname = "https://" + hostname
	}

	// open a UDP connection on a random port
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	if create && requiresPassword {
		createBody := map[string]string{
			"id":       session,
			"password": password,
		}
		createJson, _ := json.Marshal(createBody)
		createResp, err := http.Post(
			fmt.Sprintf("%s/create_session", hostname),
			"application/json",
			bytes.NewBuffer(createJson),
		)
		if err != nil {
			panic(err)
		}
		defer createResp.Body.Close()
		var createRespBody map[string]string
		json.NewDecoder(createResp.Body).Decode(&createRespBody)
		if createRespBody["error"] != "" {
			fmt.Println("Error creating session:", createRespBody["error"])
			os.Exit(1)
		}
		fmt.Println("Session created! Waiting for peer...")
	}

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
	var body map[string]string
	body = map[string]string{
		"udp_addr": fmt.Sprintf("%s:%d", xorAddr.IP, xorAddr.Port),
	}

	var endpoint string

	if requiresPassword {
		body = map[string]string{
			"udp_addr": fmt.Sprintf("%s:%d", xorAddr.IP, xorAddr.Port),
			"password": password,
		}
		endpoint = fmt.Sprintf("%s/join_session/%s", hostname, session)
	} else {
		endpoint = fmt.Sprintf("%s/session/%s", hostname, session)
	}
	bodyJson, err := json.Marshal(body)

	resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(bodyJson))
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

	if respBody["error"] != "" {
		fmt.Println("Error: ", respBody["error"])
		os.Exit(1)
	}

	peerAddr, err := net.ResolveUDPAddr("udp", respBody["peer"])
	if err != nil {
		panic(err)
	}

	fmt.Printf("Listening on %s\n", conn.LocalAddr().String())
	fmt.Printf("Peer address: %v\n", respBody["peer"])

	connected := make(chan struct{})

	go punchHole(conn, peerAddr)
	go waitForPunch(conn, connected)
	<-connected
	go readFromPeer(conn)
	go sendToPeer(conn, peerAddr)
	go keepAlive(conn, peerAddr)
	go watchConnection()
	select {} // keeps main from exiting
}

func punchHole(conn *net.UDPConn, peerAddr *net.UDPAddr) {
	for i := 0; i < 50; i++ {
		conn.WriteToUDP([]byte("punch"), peerAddr)
		time.Sleep(100 * time.Millisecond)
	}
}

func readFromPeer(conn *net.UDPConn) {
	buf := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Println("Error reading from peer: ", err)
			continue
		}
		if string(buf[:n]) == "punch" {
			continue
		}
		if string(buf[:n]) == "keepalive" {
			lastSeen = time.Now()
			continue
		}
		fmt.Printf("Received message from %s: %s\n", addr.String(), string(buf[:n]))
	}
}

func sendToPeer(conn *net.UDPConn, peerAddr *net.UDPAddr) {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Err() != nil {
		fmt.Println("Error reading from stdin: ", scanner.Err())
		return
	}
	for scanner.Scan() {
		conn.WriteToUDP([]byte(scanner.Text()), peerAddr)
	}
}

func waitForPunch(conn *net.UDPConn, connected chan struct{}) {
	// waits for punch and closes the connected channel when it receives a punch
	buf := make([]byte, 1024)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Println("Error reading from peer: ", err)
			continue
		}
		if string(buf[:n]) == "punch" {
			close(connected)
			return
		}
	}
}

func keepAlive(conn *net.UDPConn, peerAddr *net.UDPAddr) {
	interval := 10 * time.Second
	for {
		time.Sleep(interval)
		conn.WriteToUDP([]byte("keepalive"), peerAddr)
	}
}

func watchConnection() {
	// if no keepalive received for 30 seconds, assume connection is dead and exit
	for {
		time.Sleep(30 * time.Second)
		if time.Since(lastSeen) > 30*time.Second {
			fmt.Println("Connection lost, exiting...")
			os.Exit(0)
		}
	}
}
