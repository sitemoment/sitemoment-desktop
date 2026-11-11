// google-trends: interest over time and related queries, read through the
// JSON endpoints the Trends page itself uses (same origin, so the page's
// cookies apply). Answers start with ")]}'" before the JSON.
if (location.pathname.indexOf("/sorry/") === 0 || sm.exists("form#captcha-form")) {
  sm.needHuman("Google 要求人机验证");
  return;
}

function parse(text) {
  var i = text.indexOf("{");
  if (i < 0) throw new Error("谷歌趋势返回的不是 JSON");
  return JSON.parse(text.slice(i));
}

async function get(path, params) {
  var qs = Object.keys(params).map(function (k) { return k + "=" + encodeURIComponent(params[k]); }).join("&");
  var res = await fetch(path + "?" + qs, { credentials: "include" });
  if (res.status === 429) throw new Error("谷歌趋势限流了，稍后再试");
  if (!res.ok) throw new Error("谷歌趋势返回 HTTP " + res.status);
  return parse(await res.text());
}

var input = sm.input;
var req = { comparisonItem: [{ keyword: input.q, geo: input.geo || "", time: input.date }], category: 0, property: "" };
var explore = await get("/trends/api/explore", { hl: "en-US", tz: "0", req: JSON.stringify(req) });
var widgets = explore.widgets || [];
function widget(id) {
  for (var i = 0; i < widgets.length; i++) if (widgets[i].id === id) return widgets[i];
  return null;
}

var out = { query: input.q, geo: input.geo || "", date: input.date, interest: [], top: [], rising: [] };
var ts = widget("TIMESERIES");
if (ts) {
  var m = await get("/trends/api/widgetdata/multiline", { hl: "en-US", tz: "0", req: JSON.stringify(ts.request), token: ts.token });
  out.interest = ((m["default"] || {}).timelineData || []).map(function (p) {
    return { time: p.formattedTime, value: p.value && p.value.length ? p.value[0] : null };
  });
}
var rq = widget("RELATED_QUERIES");
if (rq) {
  var r = await get("/trends/api/widgetdata/relatedsearches", { hl: "en-US", tz: "0", req: JSON.stringify(rq.request), token: rq.token });
  var lists = (r["default"] || {}).rankedList || [];
  var pick = function (list) {
    return ((list || {}).rankedKeyword || []).slice(0, 25).map(function (k) {
      return { query: k.query, value: k.formattedValue || String(k.value) };
    });
  };
  out.top = pick(lists[0]);
  out.rising = pick(lists[1]);
}
sm.done(out);
