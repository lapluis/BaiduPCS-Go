# 验证记录

本文件区分两类结果：

- **历史参考验证**：2026-10-14 在参考实现工作区完成，来源为 `rc-reference/master`（tip `797ed02`）。
- **本 fork 当前验证**：2026-10-22 在 `codex/rclone-patch` 工作区对应用补丁后的本 fork 重新执行。

历史结果不能替代当前验证，也不能据此宣称本 fork 已重新完成真实网盘测试。

## 本 fork 当前验证（2026-10-22）

环境：Windows 11 Pro，Go `go1.27.1 windows/amd64`，`CGO_ENABLED=0`。

已执行并通过：

- `go build ./...`：全部包构建成功。
- `go test ./... -timeout 120s`：全部包通过。新增/相关包包括 `baidupcs`、`internal/rc`、`internal/pcsfunctions/pcsupload`、`requester/downloader`、`pcsutil/checksum`、`pcsutil`。
- `go vet ./...`：通过，无输出。
- `scripts/test-rc-local.ps1`：通过，使用临时构建的可执行文件和独立 `BAIDUPCS_GO_CONFIG_DIR`，覆盖本地 `copy`、`copyto`、`sync`、`move`、`moveto`、`check`、`lsf`、`lsjson`、`lsd`、`size`、`delete`、`deletefile`、`purge`、`rmdir`、`rmdirs`、`listremotes`、`rc mkdir/tree`，以及筛选、JSON 日志、预演、删除上限和失败退出码。
- `rc help` 与旧 `--help` 冒烟测试：新命令出现在“统一文件操作”分类，旧命令仍正常列出；测试使用独立配置目录。

当前验证的边界：

- 未访问真实百度账号，未进行任何真实网盘写入、删除或登录操作。
- 未重新进行真实双账号跨账号传输；跨账号中转与失败保留源文件由 `internal/rc/relay_test.go` 的模拟双账号测试覆盖。
- 未运行 `go test -race`；当前环境 `CGO_ENABLED=0`，本 fork 不声称通过 race detector。
- 默认不访问第三方公网 IP 服务；`pcsutil/getip` 的外部测试需 `BAIDUPCS_EXTERNAL_TESTS=1` 才运行。
- 未做大规模目录、长时间断网、真实磁盘耗尽等压力测试。

## 历史参考验证（2026-10-14，未在本 fork 重跑）

以下内容来自参考实现工作区，仅作为历史记录保留。其中的真实网盘结论不能直接套用于当前 fork。

参考实现当时完成：

- `go build -o build/BaiduPCS-Go.exe .` 成功。
- `go test ./... -timeout 90s` 通过。
- `go vet ./...` 通过。
- `scripts/test-rc-local.ps1` 通过：独立配置、本地文件操作、筛选、JSON 输出/日志、预演、删除上限、失败退出码。
- 分页测试覆盖正常跨页、重复 ID、页数上限、网络错误、缺少 list、空条目。
- 模拟两个独立账号验证跨账号下载/上传中转；上传失败不删除源文件，结束时清理临时中转目录。
- 修复既有静态检查问题：复制客户端共享缓存指针、使用标准 `unsafe.Slice`、移除不可达登录分支。
- 下载回归改用本地 HTTP 服务和随机范围读取，不依赖旧外部下载地址；修复初始 HTTP 响应变量遮蔽造成的连接泄漏。

参考实现当时的真实网盘验证（**历史结果，本 fork 未重跑**）：

- 仅在一个新建的 `/Temp/baidupcs-rc-<unique>` 子目录内写入和删除测试文件。
- 覆盖显式账号别名、列表、建目录、上传下载、小文件 SHA-256 往返、重复 copy 跳过、check、服务端复制/移动、sync 预演与删除上限、并发传输、0 字节文件、空目录、同大小不同内容覆盖、5 MiB + 7 字节分片上传下载及最终清理。
- 使用一个现有账号；真实跨账号传输未验证，由模拟双账号测试覆盖中转与失败保护。
- 当时未启用 CGO、未找到 C 编译器，未运行 race detector。
- 清理前后 `/Temp` 原有子目录名称列表一致；测试账号别名和临时目录已清理。

## 复现当前验证

```powershell
go build ./...
go test ./... -timeout 120s
go vet ./...

$binary = Join-Path ([IO.Path]::GetTempPath()) 'BaiduPCS-Go-rc-test.exe'
go build -o $binary .
& .\scripts\test-rc-local.ps1 -Binary $binary
```

本地脚本只使用独立配置目录和唯一临时目录，退出时只清理自身路径。不要使用真实账号或真实网盘进行自动回归。
