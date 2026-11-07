package panhome

import (
	"github.com/qjfoidnh/Baidu-Login/bdcrypto"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/netdisksign"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
)

type (
	// SignRes 签名结果
	SignRes interface {
		Sign() string
		Timestamp() string
	}

	signRes struct {
		sign      string
		timestamp string
	}
)

func (sr *signRes) Sign() string {
	return sr.sign
}
func (sr *signRes) Timestamp() string {
	return sr.timestamp
}

func (ph *PanHome) Signature() (sign SignRes, err error) {
	ph.mu.Lock()
	defer ph.mu.Unlock()
	return ph.signature()
}

// signature requires mu to be held by the caller.
func (ph *PanHome) signature() (sign SignRes, err error) {
	ph.lazyInit()
	err = ph.getSignInfo()
	if err != nil {
		return nil, err
	}

	o := netdisksign.Sign2(ph.sign3, ph.sign1)
	signed := bdcrypto.Base64Encode(o)
	return &signRes{
		sign:      converter.ToString(signed),
		timestamp: ph.timestamp,
	}, nil
}
