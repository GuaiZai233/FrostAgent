package main

import (
	"log"
	"net/http"
	"os"

	"FrostAgent/internal/adapter/onebot"
)

func main() {
	//reg reverse ws router
	http.HandleFunc("/ws/frostagent", onebot.HandleWS)

	// start server
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8080"
	}

	log.Printf("FrostAgent 服务已启动，监听 %s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("服务器启动失败: %v\n", err)
	}
}
