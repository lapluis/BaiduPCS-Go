package baidupcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/qjfoidnh/baidu-tools/tieba/tiebautil"
)

// The locate signature needs the Baidu UID, not the full Tieba profile.
// Use the existing Tieba login protocol without its uncancellable profile fetch.
func (pcs *BaiduPCS) locateDownloadUID(ctx context.Context, bduss string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	post := map[string]string{
		"bdusstoken": bduss + "|null", "channel_id": "", "channel_uid": "",
		"stErrorNums": "0", "subapp_type": "mini", "timestamp": timestamp + "922",
	}
	tiebautil.TiebaClientSignature(post)
	resp, err := pcs.client.ReqWithContext(ctx, http.MethodPost, "http://tieba.baidu.com/c/s/login", post, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded", "Cookie": "ka=open", "net": "1",
		"User-Agent": "bdtb for Android 6.9.2.1", "client_logid": timestamp + "416", "Connection": "Keep-Alive",
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var reply struct {
		Code    string `json:"error_code"`
		Message string `json:"error_msg"`
		User    struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	err = json.NewDecoder(resp.Body).Decode(&reply)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if err != nil {
		return 0, fmt.Errorf("decode locate UID: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("locate UID HTTP status %d", resp.StatusCode)
	}
	if reply.Code != "0" {
		return 0, fmt.Errorf("locate UID error %s: %s", reply.Code, reply.Message)
	}
	uid, err := strconv.ParseUint(reply.User.ID, 10, 64)
	if err != nil || uid == 0 {
		return 0, fmt.Errorf("invalid locate UID")
	}
	return uid, nil
}
