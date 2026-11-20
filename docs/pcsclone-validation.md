# pcsclone 验证记录

本文件汇总 `pcsclone` 独立入口、`internal/rc` 引擎及本 fork 维护补丁的当前验证状态、实盘测试数据与发布前待办。

- 2026-10-23 至 2026-11-15 期间的 700 余条逐轮审计与单元/集成回归明细已完整归档至：[pcsclone-validation-archive.md](pcsclone-validation-archive.md)
- 使用、迁移与构建指南：[pcsclone.md](pcsclone.md)
- 命令契约：[pcsclone-command-contract.md](pcsclone-command-contract.md)

## 当前验证总览（截至 2026-11-20）

| 维度 | 当前状态 | 关键结果与说明 |
| --- | --- | --- |
| 发布工具链与漏洞基线 | 已通过 | 固定于 `.go-version`（Go 1.27.4，修复 GO-2026-6859）；`govulncheck` 源码与保留符号二进制扫描均为 0 个可达漏洞（依赖版本层未调用提示持续保留审查） |
| 全仓构建、静态检查与竞态测试 | 已通过 | `go build ./...`、`go vet ./...`、`git diff --check`、Windows Go 1.27.4 原生 `go test -race`、Go 1.23 Windows 386 兼容测试及本地回归脚本 `scripts/test-pcsclone-local.ps1` 均通过 |
| 单账号实盘核心功能（`/Temp`） | 已通过 | 2026-11-20 实测 `about`、`mkdir`、`copy` 上传/过滤、`ls`/`lsf`/`lsd`/`lsjson`、`stat --hash`、`size`、`copyto` 下载校验、`check`、`sync`、`move`、`deletefile`、`purge`、`baidu link`/`search`/`share create|list|cancel`/`offline list`/`recycle list` |
| 大文件并发下载与慢尾重连 | 已通过 | 2026-11-15 至 16 日完成 9 轮 4 × 1 GiB 实盘并发下载；前 8 轮自然下载 58.05–79.90 秒（均速约 60.1 MiB/s），第 9 轮受控限速触发 1 次自动断点重连并恢复至 25.5–38.7 MiB/s；36 份副本 SHA256 全部一致 |
| 断点中断与加解密安全发布 | 本地已验证 | Windows 独立控制台回环 HTTP 中断/续传（Ctrl-C 退出码 130、无残留 worker）与 `tool enc`/`dec` 独占临时文件原子发布均通过回归 |
| 剩余待验证项 | 待执行 | 见文末[待验证事项](#待验证事项)：外部/跨账号 `baidu transfer`、双账号跨账号中转、真实网盘大文件 Ctrl-C 终端续传、线上 GitHub Actions 首次触发 |

---

## 2026-11-20：旧版命令收尾、配置注入重构与单账号实盘验证

- **旧版 CLI 错误传播（`803f092`）**：修复旧版 `setastoken`、`match` 与 `tool getip` 报错仍退出 0 的问题，`setastoken` 改为显式保存并在保存失败时回滚；新增 `TestRunTestShellPatternWithError` 与 `TestLegacyRemainingUtilityExitStatus`。
- **依赖注入式账号/配置访问（`d1dafaf`）**：新增 `Baidu.BaiduPCSWithConfig`、`rc.AccountStore`、`rc.NewResolverWithStore`、`rc.RunWithConfig`、`rc.RunWorkerWithConfig`，并将 `ValidateCommandLogPath` 与 `pcsclone.App` 切换为基于注入配置隔离运行，不再改写全局 `pcsconfig.Config` 或全局配置目录；新增 `TestResolverAndCLIWithInjectedStoreAreIsolatedFromGlobalConfig` 与 `TestAppEnsureRuntimeDoesNotMutateGlobalConfig`。
- **本地门禁**：`go build ./...`、`go vet ./...`、`git diff --check`、`scripts/test-pcsclone-local.ps1` 及受影响五包（`internal/pcsconfig`、`internal/pcscommand`、`internal/rc`、`internal/pcsclone`、`internal/cliintegration`）完整测试全部通过。
- **单账号实盘验证（`/Temp/pcsclone-live-<id>`，`build/pcsclone.exe` `v4.0.2-dev+d1dafaf`）**：
  - **已通过**：`about --json`、`mkdir`、`copy`（上传与 `--exclude "*.tmp"` 过滤）、`ls`/`lsf`/`lsd`/`lsjson -R`、`stat --hash`（大小 65,536 字节与本地 MD5 一致）、`size --json`（count=2、bytes=327,680）、`copyto`（下载 262,144 字节并核对 SHA256 逐字节一致）、`check`、`sync --dry-run`（确认远端未改动）、`sync --max-delete 5` + 二次 `check`（新增与删除均生效）、`move --delete-empty-src-dirs`（文件成功迁移至目标目录，源根按契约保留）、`baidu link`（默认 locate 与 `--direct --json`）、`baidu search --recursive`、`baidu share create`/`list`/`cancel`（创建后立即可查并立即取消，无残留分享）、`baidu offline list`、`baidu recycle list`、`deletefile`。
  - **清理确认**：执行 `purge` 删除远端隔离目录并通过 `stat` 确认已不存在，本地临时目录同步清理；未在日志或仓库中记录任何凭据、Cookie 或分享链接。
  - **边界说明**：同账号对自身刚创建的分享执行 `baidu transfer` 返回退出 1（需使用外部或另一账号分享链接复测）；本轮样本为小文件功能验证，未统计吞吐速率。

---

## 2026-11-15 至 16 日：四文件并发下载九轮实测

使用 Windows 版 `build/pcsclone.exe`（`v4.0.2-dev+9990808`）从度盘 `/Temp/pcsclone-c884aa4a8305444b9e81fe0ab37b83e2` 下载同一批 4 个 1 GiB 随机文件（总计 4 GiB）。每轮使用新的本地目标目录，在 Windows 伪终端运行 `copy -P --transfers 4 --download-threads 4 --max-connections 16`。九轮都在终端观察到 4 个活动文件。

| 轮次 | 下载条件 | 耗时（秒） | 自动重连次数 | SHA256 匹配 |
| --- | --- | ---: | ---: | ---: |
| 1 | 自然下载 | 62.26 | 0 | 4/4 |
| 2 | 自然下载 | 76.83 | 0 | 4/4 |
| 3 | 自然下载 | 66.93 | 0 | 4/4 |
| 4 | 自然下载 | 79.90 | 0 | 4/4 |
| 5 | 自然下载 | 73.03 | 0 | 4/4 |
| 6 | 自然下载 | 65.32 | 0 | 4/4 |
| 7 | 自然下载 | 58.05 | 0 | 4/4 |
| 8 | 自然下载 | 63.37 | 0 | 4/4 |
| 9 | 原连接受控降速 | 149.85 | 1 | 4/4 |

前八轮保持自然网速（约 51.3–70.6 MiB/s，均速约 60.1 MiB/s），均未触发自动重连。第九轮由本地转发器仅对 `sample-4.bin` 原下载进程的下行连接施加限速；其余三个文件完成后，`pcsclone` 检测到尾文件持续低速并从断点自动重连一次，新连接恢复正常速度。

另在提交 `355c515` 上执行的两轮 4 × 1 GiB 实盘测试中，第一轮用时 65.07 秒（重连 0 次），第二轮尾文件降至约 65–194 KiB/s 后自动重连一次并恢复至约 34–39 MiB/s（用时 130.17 秒），两轮均校验 SHA256 一致并已清理测试目录。

### 第九轮的 `-P` 显示

下表以终端打印 `reconnect: sample-4.bin` 的时刻为零点。进度和已传量取自尾文件行，速度和 ETA 取自总进度行，此时只有尾文件在传输。

| 相对时间 | 尾文件进度 | 尾文件已传量 | 速度 | ETA |
| ---: | ---: | ---: | ---: | ---: |
| −10.4 秒 | 2.5% | 25.2 MiB | 185.8 KiB/s | 1h31m43s |
| −1.4 秒 | 2.8% | 28.8 MiB | 193.9 KiB/s | 1h27m35s |
| +0.1 秒 | 2.8% | 29.1 MiB | 0.0 B/s | `--` |
| +3.6 秒 | 5.1% | 51.9 MiB | 25.5 MiB/s | 38s |
| +10.1 秒 | 28.2% | 288.3 MiB | 38.7 MiB/s | 19s |

重连期间连续 7 帧显示 `reconnecting`，已传量保持在 29.1 MiB：

```text
75.7% | 3.028 GiB / 4.000 GiB | 0.0 B/s | ETA --
files 3/4 | active 1 | failed 0 | elapsed 1m49s
* sample-4.bin  2.8%  29.1 MiB / 1.000 GiB  reconnecting
```

### 文件完整性与清理

36 份下载副本均为 1,073,741,824 字节，且与各自源文件 SHA256 一致；九轮终态均为 `copied=4 failed=0 canceled=0 pending=0`。

| 文件 | 源文件 SHA256 |
| --- | --- |
| `sample-1.bin` | `c71b98da466952878ce41c7d6ff70d8ac077e5d83cff01e54ec18b608aaacbb4` |
| `sample-2.bin` | `c7b49854a6d5ff9533b7179cb15c378098e4dab1a2fef11a83537c181ca46a58` |
| `sample-3.bin` | `299ff733898aca8d22cb529ae246680519c6290ae575a111a4ff91f196a2ef53` |
| `sample-4.bin` | `bc4e22a50486c07946fd1d8b48fb7bbcda83eec6035e62473f75ecb0e4d869c9` |

测试结束后已删除度盘上的 4 个测试文件与目录并通过 `stat` 确认不存在，本地测试源文件、下载副本和临时状态目录均已清理。本机保留逐轮校验结果 `build/terminal-cloud-c884aa4a/results.json`、第九轮逐帧记录 `round-9-frames.jsonl`、重连摘录 `round-9-excerpt.json` 及同目录下的清理记录 `cleanup.txt`。

---

## 2026-11-15：加解密安全发布与回环下载中断复测

- **全仓门禁**：冻结代码后的 Windows Go 1.27.4 全仓构建、`go vet` 与原生 race（`go test -race -p 1 ./... -parallel 4 -shuffle=on -count=1 -timeout 600s`）均退出 0。
- **加解密原子发布**：`tool enc` 与 `tool dec` 在目标目录内创建独占随机临时文件，转换、同步、关闭成功后才发布，已有目标及预检查后出现的同名目标均保留；Windows 使用带写穿标志的同卷移动，Linux 优先 `renameat2` 禁止替换，其他平台使用硬链接。九种 AES 方法（含 gzip 开关、空文件、跨缓冲区数据及故障注入）在 Go 1.27.4 Windows amd64、Go 1.23.12 Windows 386 和 Go 1.26.10 WSL Linux amd64 下验证通过。
- **下载中断与恢复复现**：在仓库根目录运行 `go test -v ./internal/cliintegration -run '^TestWindowsConsoleDownloadInterruptAndResume$' -count=1`。Windows 独立控制台使用回环 HTTP 服务，活动文件停在 1.0 MiB 时日志显示 `12.5%、0.0 B/s、ETA --、waiting=1`；Ctrl-C 返回 `130`、`canceled=1、failed=0`，旧目标与检查点保留且无残留 worker；恢复后仅传输剩余 7,372,817 字节，最终 8,388,625 字节逐字节一致；替换远端 ETag 后重新下载并匹配新内容。另覆盖分片截断、429/503/509 `Retry-After` 等待与等待期间取消。

---

## 2026-11-06：进度、取消与下载续传核心回归

该轮集中加固了断点续传、HTTP 实体校验、落盘顺序与回调生命周期，主要验证覆盖：

- **完成记录与实体版本校验**：发布前计算下载临时文件摘要；弱 ETag（`W/"…"`）退回单流下载且不复用旧分片断点；初始探测与分片的 403/404/429/509/500 错误不覆盖实体信息或误报实体变化；多镜像探测校验 ETag / Last-Modified。
- **数据与断点持久化**：EOF 完整性校验防止前缀填满缓冲区误报成功；数据落盘先于断点落盘，断点先写临时文件同步后再替换；读取断点限制 16 MiB 并拒绝损坏/重叠区间；已完成断点支持直接收尾并清理。
- **并发、取消与进度输出**：降低 `--download-threads` 恢复旧断点时多余区间排队执行；取消时 2 个运行任务保存断点退出、未启动任务保留为 pending；终端 `-P` 原地刷新、Unicode 截断、重定向与 JSON 日志无 ANSI 控制码且 5 秒节流。

详细逐轮测试记录（含 2026-10-23 至 2026-11-15 期间的 `baidupcs`、`requester/downloader`、`requester/uploader`、`internal/rc`、`internal/pcsclone` 与旧版 CLI 错误传播回归）请查阅 [pcsclone-validation-archive.md](pcsclone-validation-archive.md)。

---

## 2026-10-23：独立入口初始验证与合并审查回归

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 全仓构建、单测与静态检查 | `go build ./...` / `go test ./...` / `go vet ./...` | 通过 |
| 交叉编译冒烟 | `linux/amd64`、`windows/386`、`darwin/arm64`（`CGO_ENABLED=0`） | 通过 |
| pcsclone 本地回归 | `scripts/test-pcsclone-local.ps1 -Binary build/pcsclone.exe` | PASS |
| 旧 rc 本地回归 | `scripts/test-rc-local.ps1 -Binary <legacy-build>` | PASS |
| 独立分发与配置导入 | 仓库外临时目录运行 `pcsclone.exe`；`config import-pcsgo` 只读导入与敏感值脱敏 | 通过 |

合并审查回归（`internal/pcsclone/review_test.go`）另确认：独立入口禁用 `BAIDUPCS_GO_VERBOSE` 继承、初始化前拒绝与旧配置共用存储、批量离线操作取消后立即停止后续请求。

---

## 待验证事项

以下项目尚未在对应外部环境中完成实测，发布前按需补充：

1. **外部/跨账号 `baidu transfer`**：需使用其他账号或外部公开分享链接（含提取码、`--collect` 与目标目录自动创建）实盘验证。
2. **双账号跨账号中转**：当前测试机仅配置单远端账号；双账号中转逻辑已由模拟双账号单元测试覆盖，实盘需准备两个独立测试账号并在临时目录验证。
3. **真实网盘大文件 Ctrl-C 中断与续传**：当前已通过 Windows 独立控制台回环 HTTP 验证；如需对真实百度网盘大文件复测，可使用以下模板（替换为隔离测试目录）：

```powershell
$pcs = 'D:\src\BaiduPCS-Go\build\pcsclone.exe'
$remote = 'Lapluis:/Temp/pcsclone-test-<ID>/fixture.bin'
$testDir = New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("pcsclone-check-" + [guid]::NewGuid()))
$target = Join-Path $testDir.FullName 'fixture.bin'
$copyArgs = @('copyto', $remote, $target, '-P', '--transfers', '1', '--download-threads', '1', '--max-connections', '1', '--use-json-log')
& $pcs @copyArgs --log-file (Join-Path $testDir.FullName 'first.jsonl')
$LASTEXITCODE
& $pcs @copyArgs --log-file (Join-Path $testDir.FullName 'resume.jsonl')
$LASTEXITCODE
Get-FileHash -Algorithm SHA256 -LiteralPath $target
```

4. **线上 GitHub Actions 发布流水线**：[`.github/workflows/main.yml`](../.github/workflows/main.yml) 已按 `.go-version`（Go 1.27.4）、Windows Go 1.23 兼容性门禁与二进制 `govulncheck` 扫描配置完成；需在 fork 仓库的 GitHub Actions 页面启用工作流后观察首次线上完整运行，并持续审查第三方依赖版本告警。
