package main

import (
	"log"
	"net/http"

	"FrostAgent/internal/adapter/onebot"
)

func main() {
	//reg reverse ws router
	http.HandleFunc("/ws/frostagent", onebot.HandleWS)

	// start server
	addr := "0.0.0.0:8080"
	log.Printf("FrostAgent 服务已启动，监听 %s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("服务器启动失败: %v\n", err)
	}

	//先硬编码，实际从环境变量读
}
