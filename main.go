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
}

func main() {
	r := gin.Default()

	sessions := map[string]Session{}

	r.POST("/session/:id", func(c *gin.Context) {
		var sessionInfo SessionInfo
		sessionId := c.Param("id")
		fmt.Println("Creating session with id: ", sessionId)

		// server receives udp_addr from client (get by stun server)

		c.BindJSON(&sessionInfo)
		session, exists := sessions[sessionId]
		if !exists {
			// [0]
			sessions[sessionId] = Session{
				peers: [2]Peer{
					{IP: sessionInfo.UdpAddr},
				},
			}
		} else {
			// [1]
			session.peers[1] = Peer{IP: sessionInfo.UdpAddr}
			sessions[sessionId] = session
		}
		fmt.Printf("Received session info: %+v\n", sessionInfo)
		c.JSON(200, gin.H{
			"message": "Session created",
			"id":      sessionId,
			"peers":   sessions[sessionId].peers,
		})
	})

	r.GET("/", func(c *gin.Context) {
		clientIP := c.ClientIP()
		c.JSON(200, gin.H{
			"message":  "Test",
			"clientIP": clientIP,
		})
		fmt.Println("Route accessed")
		fmt.Println(clientIP)
	})

	r.Run(":8000")
	fmt.Println("Running on http://localhost:8000")
}
