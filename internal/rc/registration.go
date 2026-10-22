package rc

import (
	"github.com/urfave/cli"
)

// Commands makes the new entry points discoverable in root help and usable from
// the existing interactive shell. The rc namespace also disambiguates local paths.
func Commands(cwd string) []cli.Command {
	descriptions := map[string]string{
		"rc":   "统一本地/网盘命令；运行 rc help 查看完整说明",
		"copy": "增量复制目录内容，保留目标额外文件", "copyto": "复制单文件到精确目标路径",
		"sync": "单向同步，删除目标额外文件", "move": "移动目录内容", "moveto": "移动单文件到精确目标路径",
		"check": "比较两端文件内容", "lsf": "输出路径列表", "lsjson": "输出 JSON 文件列表", "lsd": "列出目录", "size": "统计文件数和总大小",
		"delete": "删除筛选后的文件", "deletefile": "删除单文件", "purge": "删除目录及全部内容", "rmdir": "删除空目录", "rmdirs": "清理空子目录", "listremotes": "列出账号别名", "remote": "添加或移除账号别名",
	}
	commands := make([]cli.Command, 0, len(descriptions))
	for name, description := range descriptions {
		name := name
		commands = append(commands, cli.Command{Name: name, Usage: description, Category: "统一文件操作", SkipFlagParsing: true, Action: func(c *cli.Context) error {
			args := append([]string{name}, []string(c.Args())...)
			if code := Run(args, cwd); code != 0 {
				return cli.NewExitError("", code)
			}
			return nil
		}})
	}
	return commands
}
