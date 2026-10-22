# Rclone 风格统一文件操作命令

本仓库在 qjfoidnh/BaiduPCS-Go 的基础上，以补丁系列增加 rclone 风格的本地/百度网盘操作语义。原来的 `ls`、`upload`、`download`、`cp`、`mv`、`rm`、登录和切换账号等命令继续保留。它不是 rclone 参数和 JSON 格式的完整替代品。

补丁来源、上游同步、补丁分组与导出流程见 [patch-series.md](patch-series.md)；实现边界见 [rc-implementation.md](rc-implementation.md)；历史验证与当前验证的区别见 [rc-validation.md](rc-validation.md)。

## 构建与开始使用

```powershell
go build -o build/BaiduPCS-Go.exe .
& .\build\BaiduPCS-Go.exe rc help
& .\build\BaiduPCS-Go.exe who
# 将 who 显示的 UID 填入下一条命令；不需要重新提供 Cookie。
& .\build\BaiduPCS-Go.exe remote add bd --uid <UID>
& .\build\BaiduPCS-Go.exe listremotes
& .\build\BaiduPCS-Go.exe lsjson bd:/Temp
```

账号别名保存在现有配置目录的 `rc_remotes.json`，仅保存“别名 → UID”。凭据仍使用原 `pcs_config.json`，不复制到源码仓库。别名必须至少两个字符，以免与 Windows 盘符冲突。删除别名使用 `remote remove bd`，不会注销账号或删除网盘文件。

本地绝对路径、UNC 路径和相对路径均可使用；相对路径以启动程序的目录为基准。拒绝 `D:relative` 这类含义不明确的盘符相对路径。网盘路径采用 `bd:/目录`，以网盘根目录为基准，与旧 `cd` 的工作目录无关。

## 指令

| 命令 | 行为 |
| --- | --- |
| `copy SOURCE DEST` | 复制目录内容或将源文件放入目标目录；保留目标额外文件 |
| `copyto FILE TARGET` | 单文件复制，目标是精确文件名 |
| `sync SOURCE DEST` | 目录单向同步，删除目标端多余文件及符合条件的空目录 |
| `move SOURCE DEST` | 移动目录内容或将源文件放入目标目录 |
| `moveto FILE TARGET` | 单文件移动，目标是精确文件名 |
| `check SOURCE DEST` | 比较目录中的文件，发现差异时非零退出 |
| `lsf PATH` | 路径列表，目录以 `/` 结尾 |
| `lsjson PATH` | JSON 数组，包含 Path、Name、Size、ModTime、IsDir |
| `lsd PATH` | 目录列表 |
| `size PATH` | 递归文件数量及字节数，JSON 输出 |
| `delete PATH` | 删除匹配的文件，保留目录 |
| `deletefile FILE` | 删除指定文件 |
| `purge PATH` | 删除目录及其全部内容；不接受筛选参数 |
| `rmdir PATH` | 删除一个空目录 |
| `rmdirs PATH` | 自底向上删除空子目录，保留指定根目录 |
| `listremotes` | 列出账号别名 |
| `remote add NAME --uid UID` | 绑定已经登录的账号 |
| `remote remove NAME` | 删除别名 |
| `rc mkdir PATH` | 建立本地或网盘目录，包含缺少的父目录 |
| `rc tree PATH` | 目录树，支持 `--depth` |
| `rc quota REMOTE:` | 指定账号容量，JSON 输出 |

`mkdir bd:/目录`、`tree bd:/目录`、`quota bd:` 可直接使用。为避免与旧命令的隐式网盘路径语义冲突，本地相对路径使用 `rc mkdir ./目录`、`rc tree ./目录`。所有新增命令也可放在 `rc` 后面，并注册到原来的交互界面中。

`copy D:\Backup bd:/Backup` 的结果是 `bd:/Backup/a.txt`，不会额外生成 `bd:/Backup/Backup/a.txt`；尾斜线不改变这一规则。

## 公共选项

选项可放在路径前后；不适用于当前命令的选项会报错，避免静默忽略。

| 选项 | 说明 |
| --- | --- |
| `--dry-run` / `-n` | 展示相同计划，不进行文件内容写入或删除 |
| `--include PATTERN`、`--exclude PATTERN` | 可重复指定；排除优先；支持 `*`、`?`、`**`、字符组 |
| `--files-from FILE` | UTF-8 相对路径列表，一行一个；空行和 `#` 注释忽略 |
| `--checksum` | 传输后也验证内容 MD5 |
| `--size-only` | 差异判断只比较大小，与 `--checksum` 互斥 |
| `--ignore-existing` | 已存在的目标文件直接跳过 |
| `--update` | 跳过修改时间晚于源的目标文件 |
| `--transfers N` | 并发文件传输数，默认 4，范围 1–64 |
| `--checkers N` | 目录复制/同步/移动的并发比较数，默认 4，范围 1–64 |
| `--retries N` | 失败后的额外传输尝试次数，默认 2 |
| `--bwlimit RATE` | 传输带宽预算，支持字节/秒及 K、M、G 二进制单位；并发任务均分预算 |
| `--max-delete N` | 文件删除上限，默认 -1 表示不限制；超限在执行前失败 |
| `--delete-excluded` | 仅用于 sync，也删除被筛选规则排除的目标文件 |
| `--create-empty-src-dirs` | 复制空源目录 |
| `--delete-empty-src-dirs` | move 后清理空源子目录，保留源根目录 |
| `--recursive` / `-R` | 递归列举 |
| `--files-only`、`--dirs-only` | 限定 lsf/lsjson 输出类型 |
| `--depth N` | tree 深度，默认 -1 不限制 |
| `--log-level LEVEL` | DEBUG、INFO、ERROR、OFF；默认 INFO |
| `--log-file FILE` | 将日志写入指定文件，默认 stderr |
| `--use-json-log` | JSON Lines 日志 |
| `--progress` / `-P` | 每秒显示活动任务及已完成文件/字节统计，结束时汇总 |

筛选以源根目录下的相对路径为基准。无斜线的模式匹配任意层级的名称；`/` 开头的模式从根匹配；`**` 可跨目录。目录排除作用于其全部后代。文件路径本身不展开通配符，选择文件应使用筛选选项。

## 内容比较与执行保证

- 默认对同大小文件读取内容并计算 MD5；不直接相信百度列表的 MD5 或服务端修改时间。**网盘比较和 `check` 因此会下载文件内容**，每个并发比较可能需要一个文件大小的临时磁盘空间。
- `--size-only` 更快，但不能检测同大小的内容变化。即使使用它，跨后端 move 在删除源文件前仍比较真实内容。
- `--update` 使用接口提供的修改时间；百度的服务端时间不等于本地原始修改时间。
- 同账号目标不存在时优先服务端复制/移动；服务端移动通过目标 ID、大小及源路径消失复核。覆盖已有远端目标通过上传处理，不先删除目标。
- 跨账号复制通过本地临时文件中转；下载成功后上传，失败保留源，结束清理中转文件。
- 本地目标先写入同目录临时文件，完成后重命名，减少失败时损坏已有目标的机会。
- sync 在扫描完整、比较成功、传输成功后才执行删除；删除前复核源目录清单及目标候选文件。分页重复、上限、缺失响应字段和遍历失败都算错误。
- 源目标相同或互为子目录时拒绝操作；文件/目录冲突、Windows 大小写冲突和不支持的文件名会报错。
- 符号链接、特殊文件不跟随、不复制；当前不提供 ACL、所有者或完整文件系统元数据同步。
- 新入口的下载采用临时文件，跨命令重启的下载续传尚未提供；直接上传复用按账号、源和目标隔离的上传状态。旧 `download` 命令仍可使用其原有续传方式。
- 预演可能进行读取和临时哈希下载，但不会写入目标内容或删除源/目标文件。
- 标准输出用于列表和 JSON，日志写入标准错误或指定日志文件。新入口不启用旧式原始请求 verbose 日志。

退出码：0 成功；1 操作失败或 check 存在差异；2 参数错误；130 收到取消。并非 rclone 的完整退出码映射。

## 验证

当前 fork 的本地验证：

```powershell
go build ./...
go test ./... -timeout 120s
go vet ./...

# 建议构建到临时路径，避免覆盖已有的 build/ 产物
$binary = Join-Path ([IO.Path]::GetTempPath()) 'BaiduPCS-Go-rc-test.exe'
go build -o $binary .
& .\scripts\test-rc-local.ps1 -Binary $binary
```

本地脚本使用独立配置和唯一临时目录，退出时仅清理自身目录，不访问真实账号或网盘。单元测试包括扫描失败、分页异常、删除阈值、源/目标变化、过滤、大小写冲突、移动失败保留源以及模拟跨账号中转。

真实网盘验证只在参考实现的历史环境中进行过（2026-10-14，仅限新建的 `/Temp/baidupcs-rc-<随机ID>` 子目录），本 fork 当前**未重新执行**，详见 [rc-validation.md](rc-validation.md)。测试凭据与测试数据不提交到 Git。真实跨账号验证需要两个独立账号；当前以模拟两个账号的传输边界覆盖其逻辑。

旧外部公网 IP 服务测试需设置 `BAIDUPCS_EXTERNAL_TESTS=1` 才运行；默认测试不依赖这些第三方服务。

继承的 reflect.SliceHeader、CopyPCS 锁值复制和旧登录流程不可达代码告警已修复；本 fork 当前已通过 `go vet ./...`。当前环境 `CGO_ENABLED=0`，未运行 race detector。
