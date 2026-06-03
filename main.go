package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type Peer struct {
	IP        string `json:"ip"`
	LocalAddr string `json:"local_addr"`
}

type Session struct {
	peers    []Peer
	maxPeers int
}

type SecretSession struct {
	password string
	maxPeers int
	peers    []Peer
}

var Version = "dev"

func main() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(RateLimitMiddleware(NewIPRateLimiter(2, 5)))

	sessions := map[string]Session{}
	secretSessions := map[string]SecretSession{}
	var mu sync.Mutex

	// simple session without password
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
		session.peers = append(session.peers, Peer{IP: body.UdpAddr, LocalAddr: body.LocalAddr})
		sessions[sessionId] = session
		mu.Unlock()
		c.JSON(200, gin.H{"peers": existingPeers})
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

	// join password-protected session
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
		secretSession.peers = append(secretSession.peers, Peer{IP: body.UdpAddr, LocalAddr: body.LocalAddr})
		secretSessions[sessionId] = secretSession
		mu.Unlock()
		c.JSON(200, gin.H{"peers": existingPeers})
	})

	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message":  "Welcome to the Rendezvous Server!",
			"clientIP": c.ClientIP(),
		})
	})

	fmt.Println("Server: http://localhost:8000")
	r.Run(":8000")
}

func killSessionWatcher(sessions map[string]SecretSession, id string, mu *sync.Mutex) {
	time.Sleep(5 * time.Minute)
	mu.Lock()
	_, exists := sessions[id]
	if exists {
		delete(sessions, id)
		fmt.Printf("Session %v expired\n", id)
	}
	mu.Unlock()
}
