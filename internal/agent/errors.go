// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/webdata"
)

var errFreeOnly = errors.New("free_only")

func fmtCredits(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

// describeError returns a short message for the UI and a JSON hint for the model.
func describeError(err error) (string, string) {
	var ce *cloud.CreditsError
	var we *webdata.Error
	hint := func(code, msg string) string { return jsonText(map[string]string{"error": code, "message": msg}) }
	switch {
	case errors.Is(err, errFreeOnly):
		return "当前是只用免费工具模式", hint("free_only", "当前只允许免费工具。不要再调用付费工具。")
	case errors.As(err, &ce):
		msg := fmt.Sprintf("本月 SiteMoment 额度不够（这次最多要 %s 积分，已用 %s / %s）", fmtCredits(ce.Need), fmtCredits(ce.Used), fmtCredits(ce.Limit))
		return msg, hint("credits", msg+"。不要再调用付费工具，基于已有数据作答。")
	case errors.Is(err, cloud.ErrSignedOut):
		return "没有登录 SiteMoment", hint("signed_out", "用户没有登录 SiteMoment，付费数据不可用。不要再调用付费工具。")
	case errors.Is(err, cloud.ErrDataUnavailable):
		return "SiteMoment 数据暂时不可用", hint("unavailable", "这个付费数据源暂时不可用。这一轮不要重试，基于已有数据作答。")
	case strings.Contains(err.Error(), "request_too_large"):
		return "这次请求太大", hint("too_large", "请求太大：减少关键词数量或 limit 再试一次。")
	case errors.Is(err, ErrCollectorHuman):
		return "本机谷歌搜索需要人工验证，没有完成", hint("needs_human", "本机浏览器查询被要求人工验证，用户没有完成。这一轮不要再用本机采集器查同一个网站；需要谷歌第一页时可以改用「谷歌搜索结果（付费）」（约 1 积分），或基于已有数据作答。")
	case errors.Is(err, ErrCollectorLimit):
		return "本机采集器今天的次数用完了", hint("daily_limit", "本机采集器今天的次数已用完。需要谷歌第一页时可以改用「谷歌搜索结果（付费）」，或基于已有数据作答。")
	case errors.As(err, &we) && we.Kind == webdata.KindRateLimited:
		return "数据源限速，稍后再试", hint("rate_limited", "数据源限速。这一轮不要重试同一个工具。")
	default:
		msg := truncateRunes(err.Error(), 300)
		return msg, hint("failed", msg)
	}
}
