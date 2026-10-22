# 补丁系列、上游同步与导出

本仓库是 [qjfoidnh/BaiduPCS-Go](https://github.com/qjfoidnh/BaiduPCS-Go) 的 fork，在保留原有命令的前提下，以可维护的补丁系列增加 rclone 风格的统一文件操作命令。

## 仓库角色

| 名称 | 地址/位置 | 用途 |
| --- | --- | --- |
| `upstream` | `git@github.com:qjfoidnh/BaiduPCS-Go.git` | 原始上游，只读同步来源 |
| `origin` | `git@github.com:lapluis/BaiduPCS-Go.git` | 本 fork 的发布远程 |
| `main` | `origin/main` | fork 集成分支，当前补丁基线为 `1b91318` |
| `codex/rclone-patch` | 本地/发布分支 | 在 `main` 之上按阶段组织的补丁系列 |
| `rc-reference/master` | 仅本机导入的引用分支 | 历史参考实现，孤儿历史，**不要 merge** |

`rc-reference/master` 的基线提交 `bc05e23` 与 `main` 的 `1b91318` 树内容基本一致，仅 `.gitignore` 和 `build.sh` 文件模式不同。本 fork 有意不导入这两处基线差异：`.gitignore` 仍由本 fork 维护，`build.sh` 保持原来的可执行位。参考实现的后续提交通过 `git diff`/选择性复制源码引入，而不是合并其无关历史。

## 补丁分组

补丁按“核心修复 → 传输支撑 → rc 引擎 → CLI 接入 → 测试/文档”排列，确保每一组都能独立审查，并且不把内部 rc 实现与既有命令的修复混在一起。

1. **核心可靠性修复**
   - 目录分页失败传播与完整性校验：`baidupcs/file_directory.go`、`baidupcs/listing_test.go`
   - 复制/移动请求错误传播：`baidupcs/cp_mv_rename.go`
   - 下载首响应关闭与 Content-Length 修正：`requester/downloader/download_firstinfo.go`、`requester/downloader/downloader.go`、`requester/downloader/firstinfo_test.go`
2. **客户端与静态检查修复**
   - 复制客户端共享缓存指针并惰性初始化：`baidupcs/baidupcs.go`、`baidupcs/cache.go`、`baidupcs/cache_test.go`
   - `go vet` 既有告警：`baidupcs/prepare.go`、`internal/pcscommand/login.go`、`pcsutil/converter/converter.go`、`requester/util.go`
   - 自包含测试夹具：`pcsutil/checksum/checksum_test.go`、`pcsutil/file_test.go`、`pcsutil/getip/getip_test.go`、`requester/downloader/download_test.go`、`requester/downloader/range_test.go`
3. **rc 传输支撑**
   - 独立上传续传状态库与缓存匹配开关：`internal/pcsfunctions/pcsupload/upload_database.go`、`internal/pcsfunctions/pcsupload/upload_task_unit.go`
4. **internal/rc 实现**
   - 后端、端点、筛选、worker、计划/执行引擎与操作：`internal/rc/*.go`
   - 对应单元测试：`internal/rc/*_test.go`
5. **CLI 接入**
   - 命令注册与主程序接线：`internal/rc/registration.go`、`main.go`
   - 旧命令不被打断的回归测试：`internal/rc/handles_test.go`
6. **回归脚本**
   - `scripts/test-rc-local.ps1`
7. **文档**
   - `README.md`、`docs/rc.md`、`docs/rc-implementation.md`、`docs/rc-validation.md`、`docs/patch-series.md`

## 从上游更新

`upstream` 的默认分支是 `main`，本 fork 的集成分支是 `main`。建议流程：

```bash
git fetch upstream
git switch main
# 保持 fork 的线性历史；如有本地提交，先确认冲突处理策略
git rebase upstream/main
go test ./... -timeout 120s
go vet ./...
git push origin main
```

然后重建补丁分支：

```bash
git switch codex/rclone-patch
git rebase main
go test ./... -timeout 120s
go vet ./...
```

如果上游改动触及补丁文件，优先使用 `git range-diff main@{1}..codex/rclone-patch@{1} main..codex/rclone-patch` 检查补丁是否仍然等价。

## 从参考实现更新补丁

参考实现只用于取源码差异，不合并历史：

```bash
# 只取指定路径的差异
git diff <old-reference-base> rc-reference/master -- path/to/file | git apply --index

# 或先查看某个参考提交
git show rc-reference/master:internal/rc/engine.go
git log --oneline rc-reference/master
```

更新后必须重新运行本仓库的测试和本地 CLI 回归；历史验证结果不能替代当前验证。

## 导出补丁

导出完整系列（推荐保留提交边界）：

```bash
git format-patch --cover-letter --base=main -o dist/patches main..codex/rclone-patch
```

只导出源码差异：

```bash
mkdir -p dist
git diff main..codex/rclone-patch > dist/baidupcs-go-rc.patch
```

应用导出的补丁：

```bash
git am --3way dist/patches/*.patch
```

导出前检查：

```bash
git status --short
git diff --check
go test ./... -timeout 120s
go vet ./...
```

## 约束

- 不提交账号、Cookie、`pcs_config.json`、`rc_remotes.json`、`rc-state/` 或临时测试目录。
- 不把 `.gitignore` 和 `build.sh` 的基线模式差异混入补丁。
- 旧命令语义保持不变；新增入口只在明确的 rc 命令名或显式远端/盘符路径下接管。
- 真实账号/网盘写入验证不属于自动测试；本地测试只用独立配置目录和临时目录。
