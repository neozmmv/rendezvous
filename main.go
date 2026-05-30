package main

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

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
