# Rclone 风格命令的实现说明

本文件说明本 fork 中 rclone 风格命令的来源、补丁边界与兼容性。维护流程见 [patch-series.md](patch-series.md)，命令契约见 [rc.md](rc.md)，验证记录见 [rc-validation.md](rc-validation.md)。

## 基线与参考实现

- **本 fork 基线**：`main` / `origin/main` 的 `1b91318`（qjfoidnh/BaiduPCS-Go 上游代码加本 fork 已有修复）。
- **历史参考实现**：本机导入的 `rc-reference/master`，tip 为 `797ed02`（2026-10-14），采用孤儿历史。
- **参考基线**：`bc05e23`。它与 `1b91318` 的树只差 `.gitignore` 内容和 `build.sh` 文件模式；这两处基线差异没有导入本 fork。
- **引入方式**：按路径取 `git diff` 后应用，或选择性复制源码文件；不 merge 参考分支的无关历史。

参考实现的阶段划分（仅作为历史来源）：

1. `41e0708`：操作失败传播与目录列表完整性。
2. `6fcc713`：显式端点、别名、筛选与隔离传输 worker。
3. `e2a32e6`：复制/同步/移动/检查/删除的计划与执行。
4. `a63a665`、`dc1dea1`、`0b1d327`、`52420bc`：下载响应修复、CLI 接入、空文件、跨账号中转测试及既有静态检查修复。
5. `797ed02`：命令指南与验证文档。

本 fork 重新组织为以下可独立审查的补丁组（文件清单见 [patch-series.md](patch-series.md)）：

1. 核心可靠性修复：列表分页失败、复制/移动错误传播、下载首响应与长度处理。
2. 客户端与静态检查修复：共享缓存指针、`unsafe.Slice`、不可达登录分支、自包含测试。
3. rc 传输支撑：按任务隔离的上传续传状态库、`IgnoreCachedMatch` 开关。
4. `internal/rc` 实现：Local/Remote 后端、端点解析、筛选、worker、计划和执行引擎。
5. CLI 接入：`main.go` 在旧 CLI 初始化前捕获调用目录，并在显式 rc 命令或显式远端/盘符路径时交给新入口；其余参数仍走旧 CLI。
6. 本地回归脚本与文档。

## 兼容性规则

- 原有 `ls`、`cd`、`upload`、`download`、`cp`、`mv`、`rm`、`mkdir`、`tree`、`quota`、登录和切换账号等命令继续保留。
- 新增的顶层命令名（`copy`、`sync`、`move`、`check`、`lsjson` 等）在旧 CLI 中不存在，因此不会覆盖旧命令。
- `mkdir`、`tree`、`quota` 只在参数包含显式远端（如 `bd:/Temp`）或 Windows 盘符绝对路径（如 `D:\Backup`）时由 rc 接管；旧的网盘相对路径用法保持不变。
- 新入口的本地相对路径以程序启动目录为基准。`main.go` 在调用 `pcsutil.ChWorkDir()` 之前保存该目录，避免沿用旧命令的工作目录切换。
- 旧上传/下载命令继续使用原有语义；rc worker 仅通过 UID 和路径参数启动子进程，不通过命令行传递凭据。
- 新增状态文件位于既有配置目录：`rc_remotes.json`（别名 → UID）和 `rc-state/`（按账号、源、目标隔离的续传状态）。凭据仍只来自原有配置，不复制到仓库。

## 验证状态

- 参考实现在 2026-10-14 做过一次真实网盘验证，记录在 [rc-validation.md](rc-validation.md) 的“历史参考验证”一节；那是历史环境的结果，**不是本 fork 当前补丁的重新验证**。
- 本 fork 当前只重新运行了构建、单元测试、`go vet` 和本地 CLI 回归；未重新访问真实账号或网盘。
- 真实跨账号中转仍由模拟两个账号的单元测试覆盖；真实双账号验证需要另行准备测试账号。
