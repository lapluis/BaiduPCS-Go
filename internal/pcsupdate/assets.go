package pcsupdate

import (
	"regexp"
	"strings"
)

func updateAssetRegexp(version, goos, goarch string) *regexp.Regexp {
	builder := &strings.Builder{}
	builder.WriteString("^BaiduPCS-Go-" + regexp.QuoteMeta(version) + "-" + regexp.QuoteMeta(goos) + "-.*?")

	switch goarch {
	case "amd64":
		builder.WriteString("(amd64|x86_64|x64)")
	case "386":
		builder.WriteString("(386|x86)")
	case "arm":
		builder.WriteString("(armv5|armv7|arm)")
	case "arm64":
		builder.WriteString("arm64")
	case "mips":
		builder.WriteString("mips")
	case "mips64":
		builder.WriteString("mips64")
	case "mipsle":
		builder.WriteString("(mipsle|mipsel)")
	case "mips64le":
		builder.WriteString("(mips64le|mips64el)")
	default:
		builder.WriteString(regexp.QuoteMeta(goarch))
	}

	builder.WriteString("\\.zip$")

	return regexp.MustCompile(builder.String())

}
