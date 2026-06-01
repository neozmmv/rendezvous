package main

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

type SessionInfo struct {
	UdpAddr string `json:"udp_addr"`
}

type Peer struct {
	IP string `json:"ip"`
}

type Session struct {
	peers [2]Peer
	ready chan struct{}
}

func main() {
	r := gin.Default()
	sessions := map[string]Session{}

	r.POST("/session/:id", func(c *gin.Context) {
		var sessionInfo SessionInfo
		sessionId := c.Param("id")
		fmt.Println("Creating session with id: ", sessionId)

		c.BindJSON(&sessionInfo)

		session, exists := sessions[sessionId]
		if !exists {
			sessions[sessionId] = Session{
				peers: [2]Peer{
					{IP: sessionInfo.UdpAddr},
				},
				ready: make(chan struct{}),
			}
			<-sessions[sessionId].ready
			updatedSession := sessions[sessionId]
			delete(sessions, sessionId)
			c.JSON(200, gin.H{"peer": updatedSession.peers[1].IP})
			return
		}

		if session.peers[1].IP != "" {
			c.JSON(400, gin.H{"error": "session already full"})
			return
		}

		session.peers[1] = Peer{IP: sessionInfo.UdpAddr}
		sessions[sessionId] = session
		close(session.ready)

		fmt.Printf("Session completed: %+v\n", session)
		c.JSON(200, gin.H{"peer": session.peers[0].IP})
	})

	r.GET("/", func(c *gin.Context) {
		clientIP := c.ClientIP()
		c.JSON(200, gin.H{
			"message":  "Test",
			"clientIP": clientIP,
		})
	})

	fmt.Println("Server: http://localhost:8000")
	r.Run(":8000")
}
