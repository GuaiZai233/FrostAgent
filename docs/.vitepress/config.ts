import { defineConfig } from 'vitepress';

export default defineConfig({
  lang: 'zh-CN',
  title: 'FrostAgent',
  description: 'FrostAgent 项目介绍、安装部署、配置参考与使用指南。',
  base: process.env.DOCS_BASE || '/',
  lastUpdated: true,
  markdown: { codeCopyButtonTitle: '复制代码' },
  head: [['meta', { name: 'theme-color', content: '#18181b' }]],
  themeConfig: {
    logo: '/logo.svg',
    nav: [
      { text: '项目介绍', link: '/guide/introduction' },
      { text: '快速开始', link: '/guide/quick-start' },
      { text: '使用指南', link: '/usage/dashboard' },
      { text: '配置参考', link: '/reference/configuration' },
      { text: '开发', link: '/development/' },
    ],
    sidebar: [
      {
        text: '开始使用',
        items: [
          { text: '项目介绍', link: '/guide/introduction' },
          { text: '快速开始', link: '/guide/quick-start' },
          { text: '安装与构建', link: '/guide/installation' },
          { text: '部署与网络', link: '/guide/deployment' },
        ],
      },
      {
        text: '连接平台',
        items: [
          { text: 'OneBot v11', link: '/adapters/onebot' },
          { text: 'AstrBot', link: '/adapters/astrbot' },
        ],
      },
      {
        text: '日常使用',
        items: [
          { text: '控制台与实例', link: '/usage/dashboard' },
          { text: '模型路由器', link: '/usage/model-router' },
          { text: '人格与示例对话', link: '/usage/persona' },
          { text: '群聊与回复策略', link: '/usage/group-chat' },
          { text: '记忆管理', link: '/usage/memory' },
          { text: 'Embedding 语义检索', link: '/usage/embedding' },
          { text: '会话与 Prompt', link: '/usage/sessions' },
          { text: '表情包', link: '/usage/stickers' },
          { text: '直接对话', link: '/usage/direct-chat' },
          { text: '管理员指令', link: '/usage/commands' },
        ],
      },
      {
        text: '扩展与集成',
        collapsed: false,
        items: [
          { text: 'MCP 服务器', link: '/integrations/mcp' },
          { text: '隔离沙箱', link: '/integrations/sandbox' },
          { text: 'ActionsCat', link: '/integrations/actionscat' },
          { text: 'Alcyone 计费', link: '/integrations/billing' },
        ],
      },
      {
        text: '配置与运维',
        items: [
          { text: '配置参考', link: '/reference/configuration' },
          { text: 'API 入口', link: '/reference/api' },
          { text: '数据库与备份', link: '/operations/storage' },
          { text: '安全控制', link: '/operations/security' },
          { text: '日志与故障排查', link: '/operations/troubleshooting' },
          { text: '升级说明', link: '/operations/upgrading' },
        ],
      },
      {
        text: '参与开发',
        items: [
          { text: '开发指南', link: '/development/' },
          { text: '维护文档站', link: '/development/docs' },
        ],
      },
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/GuaiZai233/FrostAgent' },
    ],
    search: {
      provider: 'local',
      options: {
        locales: {
          root: {
            translations: {
              button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
              modal: {
                displayDetails: '显示详细内容',
                resetButtonTitle: '清除搜索',
                backButtonTitle: '返回',
                noResultsText: '没有找到相关内容',
                footer: {
                  selectText: '选择',
                  selectKeyAriaLabel: '回车',
                  navigateText: '切换',
                  navigateUpKeyAriaLabel: '向上',
                  navigateDownKeyAriaLabel: '向下',
                  closeText: '关闭',
                  closeKeyAriaLabel: 'Esc',
                },
              },
            },
          },
        },
      },
    },
    outline: { level: [2, 3], label: '本页目录' },
    docFooter: { prev: '上一页', next: '下一页' },
    editLink: {
      pattern: 'https://github.com/GuaiZai233/FrostAgent/edit/main/docs/:path',
      text: '在 GitHub 上编辑此页',
    },
    lastUpdated: {
      text: '最后更新',
      formatOptions: { dateStyle: 'medium', timeStyle: 'short' },
    },
    darkModeSwitchLabel: '主题',
    lightModeSwitchTitle: '切换至浅色模式',
    darkModeSwitchTitle: '切换至深色模式',
    sidebarMenuLabel: '目录',
    returnToTopLabel: '返回顶部',
    langMenuLabel: '语言',
    notFound: {
      title: '页面未找到',
      quote: '这个地址没有对应的文档，可以返回首页或使用搜索查找。',
      linkLabel: '返回首页',
      linkText: '返回首页',
    },
    footer: { message: '基于 MPL-2.0 许可证发布', copyright: 'FrostAgent' },
  },
});
