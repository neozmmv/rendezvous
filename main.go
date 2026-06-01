package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type SessionInfo struct {
	UdpAddr string `json:"udp_addr"`
}

type SessionInfoWithPassword struct {
	UdpAddr  string `json:"udp_addr"`
	Password string `json:"password"`
}

type Peer struct {
	IP string `json:"ip"`
}

type Session struct {
	peers [2]Peer
	ready chan struct{}
}

type SecretSession struct {
	password string
	session  Session
}

func main() {
	r := gin.Default()
	sessions := map[string]Session{}
	secretSessions := map[string]SecretSession{}

	var mu sync.Mutex

	r.POST("/session/:id", func(c *gin.Context) {
		var sessionInfo SessionInfo
		sessionId := c.Param("id")
		fmt.Println("Creating session with id: ", sessionId)

		c.BindJSON(&sessionInfo)

		mu.Lock()
		session, exists := sessions[sessionId]
		if !exists {
			sessions[sessionId] = Session{
				peers: [2]Peer{
					{IP: sessionInfo.UdpAddr},
				},
				ready: make(chan struct{}),
			}
			mu.Unlock()
			<-sessions[sessionId].ready
			updatedSession := sessions[sessionId]
			delete(sessions, sessionId)
			c.JSON(200, gin.H{"peer": updatedSession.peers[1].IP})
			return
		}

		if session.peers[1].IP != "" {
			mu.Unlock()
			c.JSON(400, gin.H{"error": "session already full"})
			return
		}

		session.peers[1] = Peer{IP: sessionInfo.UdpAddr}
		sessions[sessionId] = session
		close(session.ready)
		mu.Unlock()
		fmt.Printf("Session completed: %+v\n", session)
		c.JSON(200, gin.H{"peer": session.peers[0].IP})
	})

	r.POST("/create_session", func(c *gin.Context) {
		var body struct {
			Password string `json:"password"`
			Id       string `json:"id"`
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
			session: Session{
				ready: make(chan struct{}),
			},
		}
		mu.Unlock()
		go killSessionWatcher(secretSessions, body.Id, &mu)
		c.JSON(200, gin.H{"message": "session created"})
	})

	r.POST("/join_session/:id", func(c *gin.Context) {
		var body struct {
			Password string `json:"password"`
			UdpAddr  string `json:"udp_addr"`
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
		if secretSession.session.peers[0].IP == "" {
			// first peer
			secretSession.session.peers[0] = Peer{IP: body.UdpAddr}
			secretSessions[sessionId] = secretSession
			mu.Unlock()
			<-secretSessions[sessionId].session.ready
			updatedSession := secretSessions[sessionId]
			delete(secretSessions, sessionId)
			c.JSON(200, gin.H{"peer": updatedSession.session.peers[1].IP})
			return
		}
		if secretSession.session.peers[1].IP != "" {
			mu.Unlock()
			c.JSON(400, gin.H{"error": "session already full"})
			return
		}
		// second peer
		secretSession.session.peers[1] = Peer{IP: body.UdpAddr}
		secretSessions[sessionId] = secretSession
		close(secretSession.session.ready)
		mu.Unlock()
		c.JSON(200, gin.H{"peer": secretSession.session.peers[0].IP})
	})

	r.GET("/", func(c *gin.Context) {
		clientIP := c.ClientIP()
		c.JSON(200, gin.H{
			"message":  "Welcome to the Rendezvous Server!",
			"clientIP": clientIP,
		})
	})

	fmt.Println("Server: http://localhost:8000")
	r.Run(":8000")
}

func killSessionWatcher(sessions map[string]SecretSession, id string, mu *sync.Mutex) {
	time.Sleep(5 * time.Minute)
	mu.Lock()
	secret, exists := sessions[id]
	if exists && secret.session.peers[1].IP == "" {
		close(secret.session.ready)
		delete(sessions, id)
		fmt.Printf("Session %v expired\n", id)
	}
	mu.Unlock()
}
