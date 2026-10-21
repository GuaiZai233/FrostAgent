---
layout: home
title: 项目文档
hero:
  name: FrostAgent
  text: 有记忆，能行动
  tagline: 基于 Go 的 AI 角色扮演与智能体调度框架。连接即时通信平台，通过统一控制台管理模型、人格、记忆和工具。
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/quick-start
    - theme: alt
      text: 了解项目
      link: /guide/introduction
    - theme: alt
      text: GitHub
      link: https://github.com/GuaiZai233/FrostAgent
features:
  - title: 多实例运行
    details: 每个实例独立管理人格、会话、记忆与工具配置，共享模型供应商目录。
    link: /usage/dashboard
  - title: 灵活的模型路由
    details: 为对话、视觉、子智能体、反思、记忆提取、群聊压缩与 Embedding 分别配置模型，支持群级覆盖。
    link: /usage/model-router
  - title: 接入聊天平台
    details: 通过 OneBot v11 或 AstrBot 连接即时通信平台，支持群聊唤醒、引用追溯与主动回复。
    link: /adapters/onebot
  - title: 记忆与人格
    details: 结合系统提示词、示例对话、长期记忆、群档案与滚动摘要，保持角色与对话连续性。
    link: /usage/memory
  - title: 工具与自动化
    details: 导入 Skills 技能包，结合 MCP 工具、隔离沙箱和 ActionsCat 扩展智能体能力。
    link: /integrations/skills
  - title: 可观测与可维护
    details: 查看 Prompt、日志和安全记录，使用 SQL 与实例存档保存数据，按 Docker / Cloudflare 指南部署。
    link: /guide/docker
---
