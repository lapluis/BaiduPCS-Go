package pcsclone

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcscommand"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

func buildBaiduGroup() *command {
	return &command{
		Name: "baidu", Category: "Baidu netdisk",
		Summary: "Baidu-specific netdisk operations",
		Usage:   "pcsclone baidu <subcommand> [options]",
		Description: `Baidu-specific operations. Every operation selects its account
through an explicit remote path (for example bd: or bd:/backup). There is no
implicit current account or remote working directory.`,
		Group: []*command{
			{
				Name: "share", Summary: "Create, list or cancel share links",
				Usage: "pcsclone baidu share <subcommand> [options]",
				Group: []*command{
					{
						Name: "create", Summary: "Share one or more netdisk paths",
						Usage: "pcsclone baidu share create PATH... [options]",
						Description: `Share one or more remote paths under the same account.
The netdisk root cannot be shared. An empty --pwd lets Baidu generate a code.`,
						MinArgs: 1, MaxArgs: -1,
						Flags: []flagSpec{
							stringFlag("pwd", "", "CODE", "Four-character extraction code (default generated)"),
							intFlag("period", "", "DAYS", "Validity in days; 0 means permanent (default 0)"),
							boolFlag("combined", "", "Include a link?pwd=... form in the output"),
							boolFlag("json", "", "Write a JSON object"),
						},
						Run: runBaiduShareCreate,
					},
					{
						Name: "list", Summary: "List existing share links",
						Usage: "pcsclone baidu share list REMOTE [options]",
						Description: `List shares for the account selected by REMOTE. Page
defaults to 1.`,
						MinArgs: 1, MaxArgs: 1,
						Flags: []flagSpec{
							intFlag("page", "", "N", "Page number (default 1)"),
							boolFlag("json", "", "Write a JSON array"),
						},
						Run: runBaiduShareList,
					},
					{
						Name: "cancel", Summary: "Cancel share links by share ID",
						Usage:   "pcsclone baidu share cancel REMOTE SHAREID... [options]",
						MinArgs: 2, MaxArgs: -1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduShareCancel,
					},
				},
			},
			{
				Name: "transfer", Summary: "Transfer a share link into a remote directory",
				Usage: "pcsclone baidu transfer SHARE_URL DEST_REMOTE [options]",
				Description: `Save a Baidu share link into DEST_REMOTE. DEST_REMOTE must
name a directory; it is created if missing. The account is selected by the
destination remote.`,
				MinArgs: 2, MaxArgs: 2,
				Flags: []flagSpec{
					stringFlag("code", "", "CODE", "Extraction code when it is not in the URL"),
					boolFlag("collect", "", "Place multiple transferred items in a subdirectory"),
					boolFlag("json", "", "Write a JSON object"),
				},
				Run: runBaiduTransfer,
			},
			{
				Name: "link", Summary: "Get download links for a remote file",
				Usage: "pcsclone baidu link REMOTE_FILE [options]",
				Description: `Resolve download links for a file through the account selected
by REMOTE_FILE. The default locate API returns temporary URLs. --direct prints
the PCS download URL, which still requires the account cookie when fetched.`,
				MinArgs: 1, MaxArgs: 1,
				Flags: []flagSpec{
					boolFlag("direct", "", "Use the PCS download URL instead of the locate API"),
					boolFlag("json", "", "Write a JSON object"),
				},
				Run: runBaiduLink,
			},
			{
				Name: "search", Summary: "Search filenames under a remote directory",
				Usage: "pcsclone baidu search REMOTE_DIR KEYWORD [options]",
				Description: `Search for KEYWORD below REMOTE_DIR. Search is one level deep
by default; add --recursive for all descendants.`,
				MinArgs: 2, MaxArgs: 2,
				Flags: []flagSpec{
					boolFlag("recursive", "R", "Search all descendants"),
					boolFlag("json", "", "Write a JSON array"),
				},
				Run: runBaiduSearch,
			},
			{
				Name: "offline", Summary: "Manage offline download tasks",
				Usage: "pcsclone baidu offline <subcommand> [options]",
				Group: []*command{
					{
						Name: "add", Summary: "Add offline download tasks",
						Usage: "pcsclone baidu offline add SAVE_REMOTE URL... [options]",
						Description: `Add offline download tasks that save into SAVE_REMOTE.
The directory is created if missing.`,
						MinArgs: 2, MaxArgs: -1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduOfflineAdd,
					},
					{
						Name: "list", Summary: "List offline download tasks",
						Usage:   "pcsclone baidu offline list REMOTE [options]",
						MinArgs: 1, MaxArgs: 1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON array")},
						Run:   runBaiduOfflineList,
					},
					{
						Name: "query", Summary: "Query offline tasks by ID",
						Usage:   "pcsclone baidu offline query REMOTE ID... [options]",
						MinArgs: 2, MaxArgs: -1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON array")},
						Run:   runBaiduOfflineQuery,
					},
					{
						Name: "cancel", Summary: "Cancel offline tasks",
						Usage:   "pcsclone baidu offline cancel REMOTE ID... [options]",
						MinArgs: 2, MaxArgs: -1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduOfflineCancel,
					},
					{
						Name: "delete", Summary: "Delete offline task records",
						Usage:   "pcsclone baidu offline delete REMOTE ID... [options]",
						MinArgs: 2, MaxArgs: -1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduOfflineDelete,
					},
					{
						Name: "clear", Summary: "Clear all offline task records",
						Usage:   "pcsclone baidu offline clear REMOTE [options]",
						MinArgs: 1, MaxArgs: 1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduOfflineClear,
					},
				},
			},
			{
				Name: "recycle", Summary: "Manage the netdisk recycle bin",
				Usage: "pcsclone baidu recycle <subcommand> [options]",
				Group: []*command{
					{
						Name: "list", Summary: "List recycle bin entries",
						Usage: "pcsclone baidu recycle list REMOTE [options]",
						Description: `List recycle bin entries for the account selected by
REMOTE. Page defaults to 1.`,
						MinArgs: 1, MaxArgs: 1,
						Flags: []flagSpec{
							intFlag("page", "", "N", "Page number (default 1)"),
							boolFlag("json", "", "Write a JSON array"),
						},
						Run: runBaiduRecycleList,
					},
					{
						Name: "restore", Summary: "Restore recycle bin entries",
						Usage:   "pcsclone baidu recycle restore REMOTE FSID... [options]",
						MinArgs: 2, MaxArgs: -1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduRecycleRestore,
					},
					{
						Name: "delete", Summary: "Permanently delete recycle bin entries",
						Usage:   "pcsclone baidu recycle delete REMOTE FSID... [options]",
						MinArgs: 2, MaxArgs: -1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduRecycleDelete,
					},
					{
						Name: "clear", Summary: "Empty the recycle bin",
						Usage:   "pcsclone baidu recycle clear REMOTE [options]",
						MinArgs: 1, MaxArgs: 1,
						Flags: []flagSpec{boolFlag("json", "", "Write a JSON object")},
						Run:   runBaiduRecycleClear,
					},
				},
			},
		},
	}
}

func (c *commandContext) resolveRemote(arg string) (*rc.Remote, string, error) {
	if !rc.IsRemote(arg) {
		return nil, "", usageErrorf("%q is not a remote path; use NAME:/path or NAME: to select an account", arg)
	}
	endpoint, err := c.resolve(arg)
	if err != nil {
		return nil, "", operationError(err)
	}
	remote, ok := endpoint.FS.(*rc.Remote)
	if !ok {
		return nil, "", usageErrorf("%q does not select a Baidu account", arg)
	}
	return remote, endpoint.Root, nil
}

func parseIDs(args []string) ([]int64, error) {
	ids := make([]int64, 0, len(args))
	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil || id <= 0 {
			return nil, usageErrorf("invalid numeric ID %q", arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func writeJSON(c *commandContext, value interface{}) error {
	if err := json.NewEncoder(c.app.Stdout).Encode(value); err != nil {
		return operationError(err)
	}
	return nil
}

type shareCreateResult struct {
	ShareID  int64  `json:"shareid"`
	Link     string `json:"link"`
	Password string `json:"pwd"`
	Combined string `json:"combined_link,omitempty"`
}

func runBaiduShareCreate(c *commandContext) error {
	var (
		pcs   *baidupcs.BaiduPCS
		uid   uint64
		paths []string
	)
	for i, arg := range c.args {
		remote, root, err := c.resolveRemote(arg)
		if err != nil {
			return err
		}
		if root == "/" {
			return usageErrorf("refusing to share the netdisk root")
		}
		if i == 0 {
			pcs, uid = remote.PCS, remote.UID
		} else if remote.UID != uid {
			return usageErrorf("all share paths must belong to the same remote")
		}
		paths = append(paths, root)
	}
	code := c.flags.String("pwd")
	if code != "" && len(code) != 4 {
		return usageErrorf("--pwd must contain exactly four characters")
	}
	period := c.flags.IntOr("period", 0)
	if period < 0 {
		return usageErrorf("--period must be zero or greater")
	}
	shared, pcsErr := pcs.ShareSet(paths, &baidupcs.ShareOption{Password: code, Period: period})
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	result := shareCreateResult{ShareID: shared.ShareID, Link: shared.Link, Password: shared.Pwd}
	if c.flags.Bool("combined") {
		result.Combined = shared.Link
		if result.Password != "" {
			separator := "?"
			if strings.Contains(result.Combined, "?") {
				separator = "&"
			}
			result.Combined += separator + "pwd=" + result.Password
		}
	}
	if c.flags.Bool("json") {
		return writeJSON(c, result)
	}
	fmt.Fprintf(c.app.Stdout, "ShareID: %d\nLink: %s\nPassword: %s\n", result.ShareID, result.Link, result.Password)
	if result.Combined != "" {
		fmt.Fprintf(c.app.Stdout, "Combined: %s\n", result.Combined)
	}
	return nil
}

func runBaiduShareList(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	page := c.flags.IntOr("page", 1)
	if page < 1 {
		return usageErrorf("--page must be 1 or greater")
	}
	records, pcsErr := remote.PCS.ShareList(page)
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	if records == nil {
		records = baidupcs.ShareRecordInfoList{}
	}
	if c.flags.Bool("json") {
		return writeJSON(c, records)
	}
	for _, record := range records {
		fmt.Fprintf(c.app.Stdout, "shareid=%d link=%s path=%s expire_type=%d views=%d\n",
			record.ShareID, record.Shortlink, record.TypicalPath, record.ExpireType, record.ViewCount)
	}
	return nil
}

func runBaiduShareCancel(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	ids, err := parseIDs(c.args[1:])
	if err != nil {
		return err
	}
	if pcsErr := remote.PCS.ShareCancel(ids); pcsErr != nil {
		return operationError(pcsErr)
	}
	if c.flags.Bool("json") {
		return writeJSON(c, map[string]interface{}{"canceled": len(ids)})
	}
	fmt.Fprintf(c.app.Stdout, "Canceled %d share(s)\n", len(ids))
	return nil
}

type transferOutput struct {
	Filename  string   `json:"filename"`
	Filenames []string `json:"filenames"`
	Path      string   `json:"path"`
}

func runBaiduTransfer(c *commandContext) error {
	remote, root, err := c.resolveRemote(c.args[1])
	if err != nil {
		return err
	}
	if err = remote.Mkdir(c.ctx, root); err != nil {
		return operationError(err)
	}
	params := []string{c.args[0]}
	if code := c.flags.String("code"); code != "" {
		if len(code) != 4 {
			return usageErrorf("--code must contain exactly four characters")
		}
		params = append(params, code)
	}
	result, err := pcscommand.ShareTransferTo(remote.PCS, params, root, &baidupcs.TransferOption{Collect: c.flags.Bool("collect")})
	if err != nil {
		return operationError(err)
	}
	output := transferOutput{Filename: result.Filename, Filenames: result.Filenames, Path: root}
	if c.flags.Bool("json") {
		return writeJSON(c, output)
	}
	fmt.Fprintf(c.app.Stdout, "Transferred: %s\nSaved to: %s\n", output.Filename, output.Path)
	return nil
}

type linkView struct {
	URL     string `json:"url"`
	Encrypt int    `json:"encrypt"`
}

type linkResult struct {
	URLs []linkView `json:"urls"`
}

func runBaiduLink(c *commandContext) error {
	remote, root, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	info, pcsErr := remote.PCS.FilesDirectoriesMeta(root)
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	if info == nil || info.Isdir {
		return usageErrorf("%q is not a netdisk file", c.args[0])
	}
	output := linkResult{URLs: []linkView{}}
	if c.flags.Bool("direct") {
		link, pcsErr := remote.PCS.GetDirectDownloadLink(root)
		if pcsErr != nil {
			return operationError(pcsErr)
		}
		output.URLs = append(output.URLs, linkView{URL: link})
	} else {
		located, pcsErr := remote.PCS.LocateDownload(root)
		if pcsErr != nil {
			return operationError(pcsErr)
		}
		if located == nil {
			return operationError(errors.New("locate API returned no download information"))
		}
		for _, item := range located.URLs {
			output.URLs = append(output.URLs, linkView{URL: item.URL, Encrypt: item.Encrypt})
		}
	}
	if c.flags.Bool("json") {
		return writeJSON(c, output)
	}
	for _, item := range output.URLs {
		fmt.Fprintln(c.app.Stdout, item.URL)
	}
	return nil
}

type searchView struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
	FsID    int64     `json:"fs_id"`
}

func runBaiduSearch(c *commandContext) error {
	remote, root, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	keyword := c.args[1]
	if strings.TrimSpace(keyword) == "" {
		return usageErrorf("search keyword cannot be empty")
	}
	files, pcsErr := remote.PCS.Search(root, keyword, c.flags.Bool("recursive"))
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	views := make([]searchView, 0, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		filePath := file.Path
		if filePath == "" {
			filePath = path.Join(root, file.Filename)
		}
		views = append(views, searchView{
			Path: filePath, Name: file.Filename, Size: file.Size,
			ModTime: time.Unix(file.Mtime, 0), IsDir: file.Isdir, FsID: file.FsID,
		})
	}
	if c.flags.Bool("json") {
		return writeJSON(c, views)
	}
	for _, view := range views {
		name := view.Path
		if view.IsDir && !strings.HasSuffix(name, "/") {
			name += "/"
		}
		fmt.Fprintln(c.app.Stdout, name)
	}
	return nil
}

type offlineTaskView struct {
	TaskID     int64  `json:"task_id"`
	Status     int    `json:"status"`
	StatusText string `json:"status_text"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Finished   int64  `json:"finished"`
	SavePath   string `json:"save_path"`
	SourceURL  string `json:"source_url"`
	CreateTime int64  `json:"create_time"`
}

func offlineViews(tasks baidupcs.CloudDlTaskList) []offlineTaskView {
	views := make([]offlineTaskView, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		views = append(views, offlineTaskView{
			TaskID: task.TaskID, Status: task.Status, StatusText: task.StatusText,
			Name: task.TaskName, Size: task.FileSize, Finished: task.FinishedSize,
			SavePath: task.SavePath, SourceURL: task.SourceURL, CreateTime: task.CreateTime,
		})
	}
	return views
}

func printOfflineViews(c *commandContext, views []offlineTaskView) {
	for _, view := range views {
		fmt.Fprintf(c.app.Stdout, "task_id=%d status=%s name=%s save_path=%s source=%s\n",
			view.TaskID, view.StatusText, view.Name, view.SavePath, view.SourceURL)
	}
}

type offlineAddEntry struct {
	TaskID int64  `json:"task_id"`
	URL    string `json:"url"`
}

type offlineAddResult struct {
	Added  []offlineAddEntry `json:"added"`
	Errors []string          `json:"errors,omitempty"`
}

func runBaiduOfflineAdd(c *commandContext) error {
	remote, root, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	if err = remote.Mkdir(c.ctx, root); err != nil {
		return operationError(err)
	}
	savePath := root
	if savePath != "/" {
		savePath = strings.TrimRight(savePath, "/") + "/"
	}
	result := offlineAddResult{Added: []offlineAddEntry{}}
	var failures []error
	for _, source := range c.args[1:] {
		taskID, pcsErr := remote.PCS.CloudDlAddTask(source, savePath)
		if pcsErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", source, pcsErr))
			failures = append(failures, fmt.Errorf("%s: %w", source, pcsErr))
			continue
		}
		result.Added = append(result.Added, offlineAddEntry{TaskID: taskID, URL: source})
	}
	if c.flags.Bool("json") {
		if err = writeJSON(c, result); err != nil {
			return err
		}
	} else {
		for _, item := range result.Added {
			fmt.Fprintf(c.app.Stdout, "task_id=%d source=%s\n", item.TaskID, item.URL)
		}
	}
	if len(failures) > 0 {
		return operationError(errors.Join(failures...))
	}
	return nil
}

func runBaiduOfflineList(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	tasks, pcsErr := remote.PCS.CloudDlListTask()
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	views := offlineViews(tasks)
	if c.flags.Bool("json") {
		return writeJSON(c, views)
	}
	printOfflineViews(c, views)
	return nil
}

func runBaiduOfflineQuery(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	ids, err := parseIDs(c.args[1:])
	if err != nil {
		return err
	}
	tasks, pcsErr := remote.PCS.CloudDlQueryTask(ids)
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	views := offlineViews(tasks)
	if c.flags.Bool("json") {
		return writeJSON(c, views)
	}
	printOfflineViews(c, views)
	return nil
}

func runBaiduOfflineCancel(c *commandContext) error {
	return runOfflineIDAction(c, "cancel")
}

func runBaiduOfflineDelete(c *commandContext) error {
	return runOfflineIDAction(c, "delete")
}

func runOfflineIDAction(c *commandContext, action string) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	ids, err := parseIDs(c.args[1:])
	if err != nil {
		return err
	}
	done := make([]int64, 0, len(ids))
	var failures []error
	for _, id := range ids {
		var pcsErr error
		if action == "cancel" {
			pcsErr = remote.PCS.CloudDlCancelTask(id)
		} else {
			pcsErr = remote.PCS.CloudDlDeleteTask(id)
		}
		if pcsErr != nil {
			failures = append(failures, fmt.Errorf("%d: %w", id, pcsErr))
			continue
		}
		done = append(done, id)
	}
	if c.flags.Bool("json") {
		if err = writeJSON(c, map[string]interface{}{"action": action, "succeeded": done, "failed": len(failures)}); err != nil {
			return err
		}
	} else {
		for _, id := range done {
			fmt.Fprintf(c.app.Stdout, "%s task %d: succeeded\n", action, id)
		}
	}
	if len(failures) > 0 {
		return operationError(errors.Join(failures...))
	}
	return nil
}

func runBaiduOfflineClear(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	total, pcsErr := remote.PCS.CloudDlClearTask()
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	if c.flags.Bool("json") {
		return writeJSON(c, map[string]int{"cleared": total})
	}
	fmt.Fprintf(c.app.Stdout, "Cleared %d offline task record(s)\n", total)
	return nil
}

type recycleView struct {
	FsID     int64  `json:"fs_id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Ctime    int64  `json:"ctime"`
	Mtime    int64  `json:"mtime"`
	LeftTime int    `json:"left_time"`
	MD5      string `json:"md5,omitempty"`
}

func runBaiduRecycleList(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	page := c.flags.IntOr("page", 1)
	if page < 1 {
		return usageErrorf("--page must be 1 or greater")
	}
	files, pcsErr := remote.PCS.RecycleList(page)
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	views := make([]recycleView, 0, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		views = append(views, recycleView{
			FsID: file.FsID, Name: file.Filename, Path: file.Path, Size: file.Size,
			IsDir: file.Isdir == 1, Ctime: file.Ctime, Mtime: file.Mtime,
			LeftTime: file.LeftTime, MD5: file.MD5,
		})
	}
	if c.flags.Bool("json") {
		return writeJSON(c, views)
	}
	for _, view := range views {
		name := view.Path
		if view.IsDir && !strings.HasSuffix(name, "/") {
			name += "/"
		}
		fmt.Fprintf(c.app.Stdout, "fs_id=%d size=%d left_time=%d path=%s\n", view.FsID, view.Size, view.LeftTime, name)
	}
	return nil
}

func runBaiduRecycleRestore(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	ids, err := parseIDs(c.args[1:])
	if err != nil {
		return err
	}
	restored, pcsErr := remote.PCS.RecycleRestore(ids...)
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	restoredIDs := make([]int64, 0, len(restored))
	for _, item := range restored {
		if item != nil {
			restoredIDs = append(restoredIDs, item.FsID)
		}
	}
	if c.flags.Bool("json") {
		return writeJSON(c, map[string]interface{}{"restored": restoredIDs})
	}
	fmt.Fprintf(c.app.Stdout, "Restored %d item(s)\n", len(restoredIDs))
	return nil
}

func runBaiduRecycleDelete(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	ids, err := parseIDs(c.args[1:])
	if err != nil {
		return err
	}
	if pcsErr := remote.PCS.RecycleDelete(ids...); pcsErr != nil {
		return operationError(pcsErr)
	}
	if c.flags.Bool("json") {
		return writeJSON(c, map[string]interface{}{"deleted": ids})
	}
	fmt.Fprintf(c.app.Stdout, "Permanently deleted %d item(s)\n", len(ids))
	return nil
}

func runBaiduRecycleClear(c *commandContext) error {
	remote, _, err := c.resolveRemote(c.args[0])
	if err != nil {
		return err
	}
	total, pcsErr := remote.PCS.RecycleClear()
	if pcsErr != nil {
		return operationError(pcsErr)
	}
	if c.flags.Bool("json") {
		return writeJSON(c, map[string]int{"cleared": total})
	}
	fmt.Fprintf(c.app.Stdout, "Emptied recycle bin, %d item(s)\n", total)
	return nil
}
