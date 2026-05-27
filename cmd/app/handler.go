package main

import (
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
)

func ReceiveRawMsgFromAdapter(msg string) {

}

// handleAgentQuery 处理智能体查询的接口
func handleAgentQuery(c *gin.Context) {
	var req AgentRequest

	// 绑定 JSON 请求体
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("请求绑定失败: %v\n", err)
		c.JSON(http.StatusBadRequest, AgentResponse{
			Error: "无效的请求: " + err.Error(),
		})
		return
	}

	// 检查输入是否为空
	if req.Input == "" {
		c.JSON(http.StatusBadRequest, AgentResponse{
			Error: "输入不能为空",
		})
		return
	}

	log.Printf("【收到用户输入】%s\n", req.Input)

	// 执行智能体
	result := globalEngine.Run(req.Input)

	c.JSON(http.StatusOK, AgentResponse{
		Result: result,
	})
}

// handleHealth 处理健康检查接口
func handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "FrostAgent 智能体服务运行正常",
	})
}
