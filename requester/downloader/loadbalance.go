package downloader

import (
	"net/http"
	"sync"
)

type (
	// LoadBalancerResponse 负载均衡响应状态
	LoadBalancerResponse struct {
		URL     string
		Referer string
	}

	// LoadBalancerResponseList 负载均衡列表
	LoadBalancerResponseList struct {
		lbr    []*LoadBalancerResponse
		cursor int
		mu     sync.Mutex
	}

	LoadBalancerCompareFunc func(info map[string]string, subResp *http.Response) bool
)

// NewLoadBalancerResponseList 初始化负载均衡列表
func NewLoadBalancerResponseList(lbr []*LoadBalancerResponse) *LoadBalancerResponseList {
	return &LoadBalancerResponseList{
		lbr: lbr,
	}
}

// SequentialGet 顺序获取
func (lbrl *LoadBalancerResponseList) SequentialGet() *LoadBalancerResponse {
	lbrl.mu.Lock()
	defer lbrl.mu.Unlock()
	if len(lbrl.lbr) == 0 {
		return nil
	}

	lbr := lbrl.lbr[lbrl.cursor]
	lbrl.cursor++
	if lbrl.cursor == len(lbrl.lbr) {
		lbrl.cursor = 0
	}
	return lbr
}

// RandomGet 随机获取
func (lbrl *LoadBalancerResponseList) RandomGet() *LoadBalancerResponse {
	if len(lbrl.lbr) == 0 {
		return nil
	}
	return lbrl.lbr[RandomNumber(0, len(lbrl.lbr))]
}

// AddLoadBalanceServer 增加负载均衡服务器
func (der *Downloader) AddLoadBalanceServer(urls ...string) {
	der.loadBalansers = append(der.loadBalansers, urls...)
}

// DefaultLoadBalancerCompareFunc 检测负载均衡的服务器是否一致
func DefaultLoadBalancerCompareFunc(info map[string]string, subResp *http.Response) bool {
	if info == nil || subResp == nil {
		return false
	}

	for headerKey, value := range info {
		if value != subResp.Header.Get(headerKey) {
			return false
		}
	}

	return true
}
