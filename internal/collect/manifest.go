// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Manifest is a collector's manifest.json.
type Manifest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Tool is the agent tool name (default: ID with "-" → "_").
	Tool string `json:"tool"`
	// Hosts are the exact host names the collector may read.
	Hosts []string `json:"hosts"`
	// URL is the page to open: https, a host from Hosts, and {param}
	// placeholders that Go fills with query-escaped input values.
	URL            string `json:"url"`
	CacheHours     int    `json:"cacheHours"`
	MinGapSeconds  int    `json:"minGapSeconds"`
	JitterSeconds  int    `json:"jitterSeconds"`
	DailyLimit     int    `json:"dailyLimit"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// schemaFile is a collector's schema.json: the agent tool's description and
// parameters (Input, a JSON Schema object) and the shape of its output.
type schemaFile struct {
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output"`
}

// Param is one input parameter.
type Param struct {
	Type        string `json:"type"` // string | integer | number | boolean
	Description string `json:"description"`
	Enum        []any  `json:"enum"`
	Default     any    `json:"default"`
}

type inputSchema struct {
	Type       string           `json:"type"`
	Properties map[string]Param `json:"properties"`
	Required   []string         `json:"required"`
}

// Collector is a loaded, validated collector package.
type Collector struct {
	Manifest
	Description string
	Input       json.RawMessage
	Output      json.RawMessage
	Script      string
	Builtin     bool
	// SHA is the sha256 (hex) of manifest.json + schema.json +
	// collector.js, in that order. A grant binds this, not just the
	// version, so a user collector's code changing without a version
	// bump still asks the user to approve again.
	SHA    string
	params inputSchema
	host   string // the URL template's host, set by checkURL
}

// Host is the exact host this collector's page is on (the one host from
// the URL template, not the full, possibly broader, Hosts list that
// Verify uses for message origins). Runner shares spacing and daily-limit
// state across every enabled collector with the same Host, built-in or
// not, so a user collector can't get a laxer budget against a site a
// built-in already paces.
func (c *Collector) Host() string { return c.host }

var (
	idPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	toolPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)
	hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	placeholder = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)
)

// maxScript caps collector.js.
const maxScript = 256 << 10

// Load reads and validates the collector package in dir of fsys.
func Load(fsys fs.FS, dir string, builtin bool) (*Collector, error) {
	readRaw := func(name string) ([]byte, error) {
		b, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("读不到 %s", name)
		}
		return b, nil
	}
	c := &Collector{Builtin: builtin}
	manifestRaw, err := readRaw("manifest.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(manifestRaw, &c.Manifest); err != nil {
		return nil, fmt.Errorf("manifest.json 不是有效的 JSON：%v", err)
	}
	schemaRaw, err := readRaw("schema.json")
	if err != nil {
		return nil, err
	}
	var sch schemaFile
	if err := json.Unmarshal(schemaRaw, &sch); err != nil {
		return nil, fmt.Errorf("schema.json 不是有效的 JSON：%v", err)
	}
	js, err := readRaw("collector.js")
	if err != nil {
		return nil, errors.New("读不到 collector.js")
	}
	c.Description, c.Input, c.Output, c.Script = strings.TrimSpace(sch.Description), sch.Input, sch.Output, string(js)
	sum := sha256.New()
	sum.Write(manifestRaw)
	sum.Write(schemaRaw)
	sum.Write(js)
	c.SHA = hex.EncodeToString(sum.Sum(nil))
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Collector) validate() error {
	m := &c.Manifest
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("id %q 只能是小写字母、数字和 -，以字母开头", m.ID)
	}
	if m.Tool == "" {
		m.Tool = strings.ReplaceAll(m.ID, "-", "_")
	}
	if !toolPattern.MatchString(m.Tool) {
		return fmt.Errorf("tool %q 只能是小写字母、数字和 _，以字母开头", m.Tool)
	}
	if n := len([]rune(strings.TrimSpace(m.Name))); n == 0 || n > 40 {
		return errors.New("name 要有 1–40 个字")
	}
	if v := strings.TrimSpace(m.Version); v == "" || len(v) > 20 {
		return errors.New("version 要有 1–20 个字符")
	}
	if len(m.Hosts) == 0 || len(m.Hosts) > 10 {
		return errors.New("hosts 要列出 1–10 个域名")
	}
	for _, h := range m.Hosts {
		if !hostPattern.MatchString(h) {
			return fmt.Errorf("hosts 里的 %q 不是完整的小写域名（不支持通配符）", h)
		}
		if net.ParseIP(h) != nil {
			return fmt.Errorf("hosts 里的 %q 是 IP 地址，不接受", h)
		}
		if strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
			return fmt.Errorf("hosts 里的 %q 是内网域名，不接受", h)
		}
	}
	if c.Description == "" || len([]rune(c.Description)) > 2000 {
		return errors.New("schema.json 的 description 要有 1–2000 个字")
	}
	if err := json.Unmarshal(c.Input, &c.params); err != nil || c.params.Type != "object" {
		return errors.New("schema.json 的 input 必须是 type 为 object 的 JSON Schema")
	}
	for name, p := range c.params.Properties {
		switch p.Type {
		case "string", "integer", "number", "boolean":
		default:
			return fmt.Errorf("参数 %s 的类型 %q 不支持（只支持 string、integer、number、boolean）", name, p.Type)
		}
	}
	for _, r := range c.params.Required {
		if _, ok := c.params.Properties[r]; !ok {
			return fmt.Errorf("required 里的 %s 没有定义", r)
		}
	}
	if strings.TrimSpace(c.Script) == "" || len(c.Script) > maxScript {
		return errors.New("collector.js 不能为空，最大 256 KB")
	}
	if err := c.checkURL(); err != nil {
		return err
	}
	if m.CacheHours <= 0 {
		m.CacheHours = 24
	}
	if m.MinGapSeconds < 3 {
		m.MinGapSeconds = 3
	}
	if m.JitterSeconds < 0 {
		m.JitterSeconds = 0
	}
	if m.DailyLimit <= 0 || m.DailyLimit > 1000 {
		m.DailyLimit = 100
	}
	if m.TimeoutSeconds <= 0 || m.TimeoutSeconds > 300 {
		m.TimeoutSeconds = 60
	}
	return nil
}

// checkURL makes sure the URL template is https on one of the hosts, with
// placeholders only after the host and only for declared parameters.
func (c *Collector) checkURL() error {
	rest, ok := strings.CutPrefix(c.URL, "https://")
	if !ok {
		return errors.New("url 必须以 https:// 开头")
	}
	host, _, _ := strings.Cut(rest, "/")
	if strings.ContainsAny(host, "{}?#") {
		return errors.New("url 的域名部分不能带参数")
	}
	for _, m := range placeholder.FindAllStringSubmatch(c.URL, -1) {
		if _, ok := c.params.Properties[m[1]]; !ok {
			return fmt.Errorf("url 里的 {%s} 不是 input 里的参数", m[1])
		}
	}
	u, err := url.Parse(placeholder.ReplaceAllString(c.URL, "x"))
	if err != nil || u.Port() != "" || !slices.Contains(c.Hosts, u.Hostname()) {
		return errors.New("url 的域名必须是 hosts 里的一个，且不能带端口")
	}
	c.host = u.Hostname()
	return nil
}

// Params lists the input parameters by name, sorted.
func (c *Collector) Params() []string {
	names := make([]string, 0, len(c.params.Properties))
	for n := range c.params.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Normalize checks input against the input schema: unknown keys are
// dropped, defaults filled, strings trimmed (at most 200 characters),
// numbers and enums checked, required parameters enforced.
func (c *Collector) Normalize(input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for name, p := range c.params.Properties {
		v, ok := input[name]
		if !ok || v == nil {
			if p.Default != nil {
				out[name] = p.Default
			}
			continue
		}
		switch p.Type {
		case "string":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("参数 %s 必须是文本", name)
			}
			s = strings.TrimSpace(s)
			if len([]rune(s)) > 200 {
				return nil, fmt.Errorf("参数 %s 最长 200 个字", name)
			}
			if s == "" {
				if p.Default != nil {
					out[name] = p.Default
				}
				continue
			}
			v = s
		case "integer", "number":
			f, ok := v.(float64)
			if !ok {
				if n, isInt := v.(int); isInt {
					f, ok = float64(n), true
				}
			}
			if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, fmt.Errorf("参数 %s 必须是数字", name)
			}
			if p.Type == "integer" {
				f = math.Trunc(f)
			}
			v = f
		case "boolean":
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("参数 %s 必须是 true 或 false", name)
			}
		}
		if len(p.Enum) > 0 && !slices.ContainsFunc(p.Enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) }) {
			return nil, fmt.Errorf("参数 %s 只能是 %v 之一", name, p.Enum)
		}
		out[name] = v
	}
	for _, r := range c.params.Required {
		if _, ok := out[r]; !ok {
			return nil, fmt.Errorf("缺少参数 %s", r)
		}
	}
	return out, nil
}

// PageURL fills the URL template with input (already normalized).
func (c *Collector) PageURL(input map[string]any) (string, error) {
	s := placeholder.ReplaceAllStringFunc(c.URL, func(m string) string {
		v, ok := input[m[1:len(m)-1]]
		if !ok || v == nil {
			return ""
		}
		if f, isNum := v.(float64); isNum {
			return url.QueryEscape(strconv.FormatFloat(f, 'f', -1, 64))
		}
		return url.QueryEscape(fmt.Sprint(v))
	})
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || !slices.Contains(c.Hosts, u.Hostname()) {
		return "", errors.New("collect: page URL left the collector's hosts")
	}
	return s, nil
}
