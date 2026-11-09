package pcscommand

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
)

// RunCloudDlAddTask 执行添加离线下载任务
func RunCloudDlAddTask(sourceURLs []string, savePath string) {
	if err := RunCloudDlAddTaskWithError(sourceURLs, savePath); err != nil {
		fmt.Println(err)
	}
}

// RunCloudDlAddTaskWithError retains successful additions and reports all failures.
func RunCloudDlAddTaskWithError(sourceURLs []string, savePath string) error {
	sourceURLs = slices.Clone(sourceURLs)
	if len(sourceURLs) == 0 {
		return errors.New("没有任何离线下载资源地址")
	}
	for i, sourceURL := range sourceURLs {
		if strings.TrimSpace(sourceURL) == "" {
			return fmt.Errorf("第 %d 项离线下载资源地址不能为空", i+1)
		}
	}
	if err := matchPathByShellPatternOnce(&savePath); err != nil {
		return err
	}
	pcs := GetBaiduPCS()
	var failures []error
	for k, sourceURL := range sourceURLs {
		taskID, err := pcs.CloudDlAddTask(sourceURL, savePath+baidupcs.PathSeparator)
		if err != nil {
			failures = append(failures, fmt.Errorf("[%d] 地址 %s: %w", k+1, sourceURL, err))
			continue
		}
		fmt.Printf("[%d] 添加离线任务成功, 任务ID(task_id): %d, 源地址: %s, 保存路径: %s\n", k+1, taskID, sourceURL, savePath)
	}
	return errors.Join(failures...)
}

// RunCloudDlQueryTask 精确查询离线下载任务
func RunCloudDlQueryTask(taskIDs []int64) {
	if err := RunCloudDlQueryTaskWithError(taskIDs); err != nil {
		fmt.Println(err)
	}
}

// RunCloudDlQueryTaskWithError validates the entire input before querying.
func RunCloudDlQueryTaskWithError(taskIDs []int64) error {
	ids, err := validatedOfflineTaskIDs(taskIDs)
	if err != nil {
		return err
	}
	cl, err := GetBaiduPCS().CloudDlQueryTask(ids)
	if err != nil {
		return err
	}
	fmt.Println(cl)
	return nil
}

// RunCloudDlListTask 查询离线下载任务列表
func RunCloudDlListTask() {
	if err := RunCloudDlListTaskWithError(); err != nil {
		fmt.Println(err)
	}
}

// RunCloudDlListTaskWithError returns request and decoding failures to the CLI.
func RunCloudDlListTaskWithError() error {
	cl, err := GetBaiduPCS().CloudDlListTask()
	if err != nil {
		return err
	}
	fmt.Println(cl)
	return nil
}

// RunCloudDlCancelTask 取消离线下载任务
func RunCloudDlCancelTask(taskIDs []int64) {
	if err := RunCloudDlCancelTaskWithError(taskIDs); err != nil {
		fmt.Println(err)
	}
}

// RunCloudDlCancelTaskWithError validates and snapshots IDs before canceling.
func RunCloudDlCancelTaskWithError(taskIDs []int64) error {
	return runCloudDlTaskMutation(taskIDs, false)
}

// RunCloudDlDeleteTask 删除离线下载任务
func RunCloudDlDeleteTask(taskIDs []int64) {
	if err := RunCloudDlDeleteTaskWithError(taskIDs); err != nil {
		fmt.Println(err)
	}
}

// RunCloudDlDeleteTaskWithError validates and snapshots IDs before deleting.
func RunCloudDlDeleteTaskWithError(taskIDs []int64) error {
	return runCloudDlTaskMutation(taskIDs, true)
}

func validatedOfflineTaskIDs(taskIDs []int64) ([]int64, error) {
	ids := slices.Clone(taskIDs)
	if len(ids) == 0 {
		return nil, errors.New("没有任何任务ID, task_id")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("任务ID %d 必须为正整数", id)
		}
	}
	return ids, nil
}

func runCloudDlTaskMutation(taskIDs []int64, deleting bool) error {
	ids, err := validatedOfflineTaskIDs(taskIDs)
	if err != nil {
		return err
	}
	pcs := GetBaiduPCS()
	run, action := pcs.CloudDlCancelTask, "取消"
	if deleting {
		run, action = pcs.CloudDlDeleteTask, "删除"
	}
	var failures []error
	for _, id := range ids {
		if err := run(id); err != nil {
			failures = append(failures, fmt.Errorf("[%d] %s失败: %w", id, action, err))
			continue
		}
		fmt.Printf("[%d] %s成功\n", id, action)
	}
	return errors.Join(failures...)
}

// RunCloudDlClearTask 清空离线下载任务记录
func RunCloudDlClearTask() {
	if err := RunCloudDlClearTaskWithError(); err != nil {
		fmt.Println(err)
	}
}

// RunCloudDlClearTaskWithError reports failure instead of a successful exit.
func RunCloudDlClearTaskWithError() error {
	total, err := GetBaiduPCS().CloudDlClearTask()
	if err != nil {
		return err
	}
	fmt.Printf("%s成功, 共清除 %d 条记录\n", baidupcs.OperationCloudDlClearTask, total)
	return nil
}
