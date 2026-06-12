package main

import (
	"context"
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
	peers       []Peer
	maxPeers    int
	notifiers   []chan Peer
	peerCancels map[string]context.CancelFunc
}

type SecretSession struct {
	password    string
	maxPeers    int
	peers       []Peer
	notifiers   []chan Peer
	peerCancels map[string]context.CancelFunc
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

// closeSession closes all SSE notifier channels and cancels all pending TTL goroutines.
// Must be called with mu held; the caller is responsible for deleting the session from the map.
func closeSession(notifiers []chan Peer, cancels map[string]context.CancelFunc) {
	for _, ch := range notifiers {
		close(ch)
	}
	for _, cancel := range cancels {
		cancel()
	}
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(RateLimitMiddleware(NewIPRateLimiter(2, 5)))

	sessions := map[string]Session{}
	secretSessions := map[string]SecretSession{}
	var mu sync.Mutex

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
		if session.peerCancels == nil {
			session.peerCancels = map[string]context.CancelFunc{}
		}
		// Cancel any existing TTL for this peer (heartbeat / re-registration).
		if cancel, exists := session.peerCancels[body.UdpAddr]; exists {
			cancel()
		}
		// Build existingPeers excluding the registering peer, and remove it from the list.
		var existingPeers []Peer
		for _, p := range session.peers {
			if p.IP != body.UdpAddr {
				existingPeers = append(existingPeers, p)
			}
		}
		newPeer := Peer{IP: body.UdpAddr, LocalAddr: body.LocalAddr}
		for _, ch := range session.notifiers {
			select {
			case ch <- newPeer:
			default:
			}
		}
		session.peers = append(existingPeers, newPeer)
		ctx, cancel := context.WithCancel(context.Background())
		session.peerCancels[body.UdpAddr] = cancel
		sessions[sessionId] = session
		mu.Unlock()
		go peerTTLWatcher(sessions, sessionId, body.UdpAddr, &mu, ctx)
		c.JSON(200, gin.H{"peers": existingPeers})
	})

	r.GET("/version", func(c *gin.Context) {
		c.JSON(200, gin.H{"version": Version})
	})

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
			if s, exists := sessions[sessionId]; exists {
				s.notifiers = removeNotifier(s.notifiers, ch)
				sessions[sessionId] = s
			}
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
				if cancel, ok := session.peerCancels[body.UdpAddr]; ok {
					cancel()
					delete(session.peerCancels, body.UdpAddr)
				}
				break
			}
		}
		if len(session.peers) == 0 {
			closeSession(session.notifiers, session.peerCancels)
			delete(sessions, sessionId)
			fmt.Printf("Session %v deleted\n", sessionId)
		} else {
			sessions[sessionId] = session
		}
		mu.Unlock()
		c.JSON(200, gin.H{"message": "left session"})
	})

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
			password:    body.Password,
			maxPeers:    body.MaxPeers,
			peers:       []Peer{},
			peerCancels: map[string]context.CancelFunc{},
		}
		mu.Unlock()
		go killSessionWatcher(secretSessions, body.Id, &mu)
		c.JSON(200, gin.H{"message": "session created"})
	})

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
		if secretSession.peerCancels == nil {
			secretSession.peerCancels = map[string]context.CancelFunc{}
		}
		// Cancel any existing TTL for this peer (heartbeat / re-registration).
		if cancel, exists := secretSession.peerCancels[body.UdpAddr]; exists {
			cancel()
		}
		// Build existingPeers excluding the registering peer, and remove it from the list.
		var existingPeers []Peer
		for _, p := range secretSession.peers {
			if p.IP != body.UdpAddr {
				existingPeers = append(existingPeers, p)
			}
		}
		if secretSession.maxPeers > 0 && len(existingPeers) >= secretSession.maxPeers {
			mu.Unlock()
			c.JSON(400, gin.H{"error": "session is full"})
			return
		}
		newPeer := Peer{IP: body.UdpAddr, LocalAddr: body.LocalAddr}
		for _, ch := range secretSession.notifiers {
			select {
			case ch <- newPeer:
			default:
			}
		}
		secretSession.peers = append(existingPeers, newPeer)
		ctx, cancel := context.WithCancel(context.Background())
		secretSession.peerCancels[body.UdpAddr] = cancel
		secretSessions[sessionId] = secretSession
		mu.Unlock()
		go secretPeerTTLWatcher(secretSessions, sessionId, body.UdpAddr, &mu, ctx)
		c.JSON(200, gin.H{"peers": existingPeers})
	})

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
			if s, exists := secretSessions[sessionId]; exists {
				s.notifiers = removeNotifier(s.notifiers, ch)
				secretSessions[sessionId] = s
			}
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
				if cancel, ok := secretSession.peerCancels[body.UdpAddr]; ok {
					cancel()
					delete(secretSession.peerCancels, body.UdpAddr)
				}
				break
			}
		}
		if len(secretSession.peers) == 0 {
			closeSession(secretSession.notifiers, secretSession.peerCancels)
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
		closeSession(s.notifiers, s.peerCancels)
		delete(sessions, id)
		fmt.Printf("Session %v expired (no peers joined)\n", id)
	}
}

func peerTTLWatcher(sessions map[string]Session, sessionId, udpAddr string, mu *sync.Mutex, ctx context.Context) {
	select {
	case <-time.After(peerTTL):
	case <-ctx.Done():
		return
	}
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
	delete(s.peerCancels, udpAddr)
	if len(s.peers) == 0 {
		closeSession(s.notifiers, s.peerCancels)
		delete(sessions, sessionId)
		fmt.Printf("Session %v deleted (empty after peer TTL)\n", sessionId)
	} else {
		sessions[sessionId] = s
	}
}

func secretPeerTTLWatcher(sessions map[string]SecretSession, sessionId, udpAddr string, mu *sync.Mutex, ctx context.Context) {
	select {
	case <-time.After(peerTTL):
	case <-ctx.Done():
		return
	}
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
	delete(s.peerCancels, udpAddr)
	if len(s.peers) == 0 {
		closeSession(s.notifiers, s.peerCancels)
		delete(sessions, sessionId)
		fmt.Printf("Session %v deleted (empty after peer TTL)\n", sessionId)
	} else {
		sessions[sessionId] = s
	}
}
