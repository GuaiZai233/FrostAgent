package main

import (
	"FrostAgent/internal/memory"
	"fmt"
	"os"
)

const usage = `FrostAgent 记忆管理工具

用法:
  agent <命令> [参数]

命令:
  export <brain路径> <输出文件>      导出全部记忆到 JSON 文件
  import <brain路径> <输入文件>      从 JSON 文件导入记忆（跳过已存在的 ID）
  import <brain路径> <输入文件> -f   从 JSON 文件导入记忆（覆盖已存在的 ID）
  list   <brain路径> [owner]         列出全部或指定 owner 的记忆
  stats  <brain路径>                  显示记忆统计信息

示例:
  agent export data/brain.json backup.json
  agent import data/brain.json backup.json -f
  agent list data/brain.json alice
  agent stats data/brain.json
`

func main() {
	if len(os.Args) < 3 {
		fmt.Print(usage)
		os.Exit(1)
	}

	cmd := os.Args[1]
	brainPath := os.Args[2]
	store := memory.NewStore(brainPath)

	switch cmd {
	case "export":
		if len(os.Args) < 4 {
			fmt.Println("错误: 缺少输出文件路径")
			os.Exit(1)
		}
		outPath := os.Args[3]
		if err := store.Export(outPath); err != nil {
			fmt.Printf("导出失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ 记忆已导出到 %s\n", outPath)

	case "import":
		if len(os.Args) < 4 {
			fmt.Println("错误: 缺少输入文件路径")
			os.Exit(1)
		}
		inPath := os.Args[3]
		overwrite := len(os.Args) > 4 && os.Args[4] == "-f"
		imported, skipped, err := store.Import(inPath, overwrite)
		if err != nil {
			fmt.Printf("导入失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ 导入完成: %d 条新增, %d 条跳过 (覆盖模式: %v)\n", imported, skipped, overwrite)

	case "list":
		owner := ""
		if len(os.Args) > 3 {
			owner = os.Args[3]
		}
		var entries []memory.MemoryEntry
		var err error
		if owner == "" {
			entries, err = store.ListAll()
		} else {
			entries, err = store.ListByOwner(owner)
		}
		if err != nil {
			fmt.Printf("读取失败: %v\n", err)
			os.Exit(1)
		}
		if len(entries) == 0 {
			fmt.Println("无记忆记录")
			return
		}
		for _, e := range entries {
			vis := "🔒"
			if e.Visibility == memory.VisibilityPublic {
				vis = "🌐"
			}
			fmt.Printf("[%s] %s | owner=%s | imp=%.2f | %s\n", vis, e.ID, e.Owner, e.Importance, e.Content)
		}
		fmt.Printf("\n共 %d 条记忆\n", len(entries))

	case "stats":
		entries, err := store.ListAll()
		if err != nil {
			fmt.Printf("读取失败: %v\n", err)
			os.Exit(1)
		}
		owners := map[string]int{}
		pub, priv := 0, 0
		for _, e := range entries {
			owners[e.Owner]++
			if e.Visibility == memory.VisibilityPublic {
				pub++
			} else {
				priv++
			}
		}
		fmt.Printf("记忆总数: %d\n", len(entries))
		fmt.Printf("  公开: %d\n", pub)
		fmt.Printf("  私有: %d\n", priv)
		fmt.Println("按归属者:")
		for o, n := range owners {
			fmt.Printf("  %s: %d 条\n", o, n)
		}

	default:
		fmt.Printf("未知命令: %s\n\n", cmd)
		fmt.Print(usage)
		os.Exit(1)
	}
}
