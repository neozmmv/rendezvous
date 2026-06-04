package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type Peer struct {
	IP        string `json:"ip"`
	LocalAddr string `json:"local_addr"`
}

type Session struct {
	peers     []Peer
	maxPeers  int
	notifiers []chan Peer // one channel per active SSE stream
}

type SecretSession struct {
	password  string
	maxPeers  int
	peers     []Peer
	notifiers []chan Peer
}

var Version = "dev"

const peerTTL = 10 * time.Minute

func removeNotifier(notifiers []chan Peer, ch chan Peer) []chan Peer {
	for i, n := range notifiers {
		if n == ch {
			return append(notifiers[:i], notifiers[i+1:]...)
		}
	}
	return notifiers
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(RateLimitMiddleware(NewIPRateLimiter(2, 5)))

	sessions := map[string]Session{}
	secretSessions := map[string]SecretSession{}
	var mu sync.Mutex

	// join simple session — returns peers already present, notifies existing streamers
	r.POST("/session/:id", func(c *gin.Context) {
		var body struct {
			UdpAddr   string `json:"udp_addr"`
			LocalAddr string `json:"local_addr"`
		}
		sessionId := c.Param("id")
		if err := c.ShouldBindJSON(&body); err != nil || body.UdpAddr == "" {
			c.JSON(400, gin.H{"error": "udp_addr required"})
			return
		}
		mu.Lock()
		session := sessions[sessionId]
		existingPeers := make([]Peer, len(session.peers))
		copy(existingPeers, session.peers)
		newPeer := Peer{IP: body.UdpAddr, LocalAddr: body.LocalAddr}
		// notify all clients currently streaming this session
		for _, ch := range session.notifiers {
			select {
			case ch <- newPeer:
			default:
			}
		}
		session.peers = append(session.peers, newPeer)
		sessions[sessionId] = session
		mu.Unlock()
		go peerTTLWatcher(sessions, sessionId, body.UdpAddr, &mu)
		c.JSON(200, gin.H{"peers": existingPeers})
	})

	r.GET("/version", func(c *gin.Context) {
		c.JSON(200, gin.H{"version": Version}) // works best if ran by the published binary
	})

	// SSE stream: sends peers already in session as initial events, then pushes new ones as they join.
	// ?udp_addr= is used to filter the caller's own address out of the stream.
	r.GET("/session/:id/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		sessionId := c.Param("id")
		myAddr := c.Query("udp_addr")

		mu.Lock()
		session, exists := sessions[sessionId]
		if !exists {
			mu.Unlock()
			c.JSON(404, gin.H{"error": "session not found"})
			return
		}
		// Buffer sized for current peers + room for incoming ones.
		// Pre-filled here (under the lock) so we don't miss peers that join
		// between reading the list and registering the notifier.
		ch := make(chan Peer, len(session.peers)+10)
		for _, peer := range session.peers {
			if peer.IP != myAddr {
				ch <- peer
			}
		}
		session.notifiers = append(session.notifiers, ch)
		sessions[sessionId] = session
		mu.Unlock()

		defer func() {
			mu.Lock()
			s := sessions[sessionId]
			s.notifiers = removeNotifier(s.notifiers, ch)
			sessions[sessionId] = s
			mu.Unlock()
		}()

		c.Stream(func(w io.Writer) bool {
			select {
			case peer, ok := <-ch:
				if !ok {
					return false
				}
				c.SSEvent("peer", peer)
				return true
			case <-time.After(30 * time.Second):
				fmt.Fprintf(w, ": keepalive\n\n")
				return true
			case <-c.Request.Context().Done():
				return false
			}
		})
	})

	// leave simple session
	r.POST("/session/:id/leave", func(c *gin.Context) {
		var body struct {
			UdpAddr string `json:"udp_addr"`
		}
		sessionId := c.Param("id")
		if err := c.ShouldBindJSON(&body); err != nil || body.UdpAddr == "" {
			c.JSON(400, gin.H{"error": "udp_addr required"})
			return
		}
		mu.Lock()
		session, exists := sessions[sessionId]
		if !exists {
			mu.Unlock()
			c.JSON(404, gin.H{"error": "session not found"})
			return
		}
		for i, peer := range session.peers {
			if peer.IP == body.UdpAddr {
				session.peers = append(session.peers[:i], session.peers[i+1:]...)
				break
			}
		}
		if len(session.peers) == 0 {
			delete(sessions, sessionId)
			fmt.Printf("Session %v deleted\n", sessionId)
		} else {
			sessions[sessionId] = session
		}
		mu.Unlock()
		c.JSON(200, gin.H{"message": "left session"})
	})

	// create password-protected session
	r.POST("/create_session", func(c *gin.Context) {
		var body struct {
			Password string `json:"password"`
			Id       string `json:"id"`
			MaxPeers int    `json:"max_peers"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Password == "" || body.Id == "" {
			c.JSON(400, gin.H{"error": "password and id required"})
			return
		}
		mu.Lock()
		_, exists := secretSessions[body.Id]
		if exists {
			mu.Unlock()
			c.JSON(400, gin.H{"error": "session already exists"})
			return
		}
		secretSessions[body.Id] = SecretSession{
			password: body.Password,
			maxPeers: body.MaxPeers,
			peers:    []Peer{},
		}
		mu.Unlock()
		go killSessionWatcher(secretSessions, body.Id, &mu)
		c.JSON(200, gin.H{"message": "session created"})
	})

	// join password-protected session — returns peers already present, notifies existing streamers
	r.POST("/join_session/:id", func(c *gin.Context) {
		var body struct {
			Password  string `json:"password"`
			UdpAddr   string `json:"udp_addr"`
			LocalAddr string `json:"local_addr"`
		}
		sessionId := c.Param("id")
		if err := c.ShouldBindJSON(&body); err != nil || body.UdpAddr == "" || body.Password == "" {
			c.JSON(400, gin.H{"error": "password and udp_addr required"})
			return
		}
		mu.Lock()
		secretSession, exists := secretSessions[sessionId]
		if !exists {
			mu.Unlock()
			c.JSON(404, gin.H{"error": "session not found"})
			return
		}
		if secretSession.password != body.Password {
			mu.Unlock()
			c.JSON(401, gin.H{"error": "incorrect password"})
			return
		}
		if secretSession.maxPeers > 0 && len(secretSession.peers) >= secretSession.maxPeers {
			mu.Unlock()
			c.JSON(400, gin.H{"error": "session is full"})
			return
		}
		existingPeers := make([]Peer, len(secretSession.peers))
		copy(existingPeers, secretSession.peers)
		newPeer := Peer{IP: body.UdpAddr, LocalAddr: body.LocalAddr}
		for _, ch := range secretSession.notifiers {
			select {
			case ch <- newPeer:
			default:
			}
		}
		secretSession.peers = append(secretSession.peers, newPeer)
		secretSessions[sessionId] = secretSession
		mu.Unlock()
		go secretPeerTTLWatcher(secretSessions, sessionId, body.UdpAddr, &mu)
		c.JSON(200, gin.H{"peers": existingPeers})
	})

	// SSE stream for password-protected session
	r.GET("/join_session/:id/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		sessionId := c.Param("id")
		password := c.Query("password")
		myAddr := c.Query("udp_addr")

		mu.Lock()
		secretSession, exists := secretSessions[sessionId]
		if !exists {
			mu.Unlock()
			c.JSON(404, gin.H{"error": "session not found"})
			return
		}
		if secretSession.password != password {
			mu.Unlock()
			c.JSON(401, gin.H{"error": "incorrect password"})
			return
		}
		ch := make(chan Peer, len(secretSession.peers)+10)
		for _, peer := range secretSession.peers {
			if peer.IP != myAddr {
				ch <- peer
			}
		}
		secretSession.notifiers = append(secretSession.notifiers, ch)
		secretSessions[sessionId] = secretSession
		mu.Unlock()

		defer func() {
			mu.Lock()
			s := secretSessions[sessionId]
			s.notifiers = removeNotifier(s.notifiers, ch)
			secretSessions[sessionId] = s
			mu.Unlock()
		}()

		c.Stream(func(w io.Writer) bool {
			select {
			case peer, ok := <-ch:
				if !ok {
					return false
				}
				c.SSEvent("peer", peer)
				return true
			case <-time.After(30 * time.Second):
				fmt.Fprintf(w, ": keepalive\n\n")
				return true
			case <-c.Request.Context().Done():
				return false
			}
		})
	})

	// leave password-protected session — no password required, match on udp_addr only
	r.POST("/join_session/:id/leave", func(c *gin.Context) {
		var body struct {
			UdpAddr string `json:"udp_addr"`
		}
		sessionId := c.Param("id")
		if err := c.ShouldBindJSON(&body); err != nil || body.UdpAddr == "" {
			c.JSON(400, gin.H{"error": "udp_addr required"})
			return
		}
		mu.Lock()
		secretSession, exists := secretSessions[sessionId]
		if !exists {
			mu.Unlock()
			c.JSON(404, gin.H{"error": "session not found"})
			return
		}
		for i, peer := range secretSession.peers {
			if peer.IP == body.UdpAddr {
				secretSession.peers = append(secretSession.peers[:i], secretSession.peers[i+1:]...)
				break
			}
		}
		if len(secretSession.peers) == 0 {
			delete(secretSessions, sessionId)
			fmt.Printf("Session %v deleted\n", sessionId)
		} else {
			secretSessions[sessionId] = secretSession
		}
		mu.Unlock()
		c.JSON(200, gin.H{"message": "left session"})
	})

	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message":  "Welcome to the Rendezvous Server!",
			"clientIP": c.ClientIP(),
			"version":  Version,
		})
	})

	fmt.Println("Server: http://localhost:8000")
	r.Run(":8000")
}

func killSessionWatcher(sessions map[string]SecretSession, id string, mu *sync.Mutex) {
	time.Sleep(5 * time.Minute)
	mu.Lock()
	defer mu.Unlock()
	s, exists := sessions[id]
	if exists && len(s.peers) == 0 {
		delete(sessions, id)
		fmt.Printf("Session %v expired (no peers joined)\n", id)
	}
}

func peerTTLWatcher(sessions map[string]Session, sessionId, udpAddr string, mu *sync.Mutex) {
	time.Sleep(peerTTL)
	mu.Lock()
	defer mu.Unlock()
	s, exists := sessions[sessionId]
	if !exists {
		return
	}
	for i, p := range s.peers {
		if p.IP == udpAddr {
			s.peers = append(s.peers[:i], s.peers[i+1:]...)
			fmt.Printf("Peer %v removed from session %v (TTL expired)\n", udpAddr, sessionId)
			break
		}
	}
	if len(s.peers) == 0 {
		delete(sessions, sessionId)
		fmt.Printf("Session %v deleted (empty after peer TTL)\n", sessionId)
	} else {
		sessions[sessionId] = s
	}
}

func secretPeerTTLWatcher(sessions map[string]SecretSession, sessionId, udpAddr string, mu *sync.Mutex) {
	time.Sleep(peerTTL)
	mu.Lock()
	defer mu.Unlock()
	s, exists := sessions[sessionId]
	if !exists {
		return
	}
	for i, p := range s.peers {
		if p.IP == udpAddr {
			s.peers = append(s.peers[:i], s.peers[i+1:]...)
			fmt.Printf("Peer %v removed from session %v (TTL expired)\n", udpAddr, sessionId)
			break
		}
	}
	if len(s.peers) == 0 {
		delete(sessions, sessionId)
		fmt.Printf("Session %v deleted (empty after peer TTL)\n", sessionId)
	} else {
		sessions[sessionId] = s
	}
}
