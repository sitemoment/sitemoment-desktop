if (location.pathname.indexOf("/sorry/") === 0 || sm.exists("form#captcha-form")) {
  sm.post("need_human", { reason: "captcha", url: location.href });
  return;
}

function firstContainer() {
  var sels = ["#rso", "#center_col", "#main"];
  for (var i = 0; i < sels.length; i++) {
    var el = sm.$(sels[i]);
    if (el) return el;
  }
  return document;
}

function isGoogleHost(host) {
  return /(^|\.)google\.[a-z.]+$/i.test(host);
}

// resolveUrl picks the best destination url+provenance for a result <a>/<h3>:
// a decoded /url?q= redirect target, else the href itself if it already
// points off Google, else the nearest <cite> text inside the result block.
function resolveUrl(a, h3) {
  var u;
  try {
    u = new URL(a.href, location.href);
  } catch (e) {
    return null;
  }
  if (u.pathname === "/url") {
    var q = u.searchParams.get("q") || u.searchParams.get("url");
    if (q && /^https?:\/\//i.test(q)) {
      return { url: q, urlFrom: "redirect" };
    }
  }
  if (!isGoogleHost(u.hostname)) {
    return { url: a.href, urlFrom: "direct" };
  }
  var block = h3.closest("div[data-hveid], div.g, div");
  var cite = block ? sm.$("cite", block) : null;
  if (cite) {
    var text = sm.text(cite);
    if (text) return { url: text, urlFrom: "cite" };
  }
  return null;
}

var organic = [];
var seenURLs = {};
sm.all("a h3", firstContainer()).forEach(function (h3) {
  if (organic.length >= 10) return;
  var a = h3.closest("a");
  if (!a || !a.href) return;
  var resolved = resolveUrl(a, h3);
  if (!resolved || !resolved.url || seenURLs[resolved.url]) return;
  seenURLs[resolved.url] = true;
  organic.push({ rank: organic.length + 1, url: resolved.url, urlFrom: resolved.urlFrom, title: sm.text(h3) });
});

// #hdtb (the top nav tab bar in the plan's assumed markup) no longer exists
// on the page as observed; its tabs (All/Images/Videos/Maps/News/Books, plus
// the time filters under "Search tools") are instead /search? links that
// repeat the current q= with a tbm=/udm= view switch appended, so filter
// those out by comparing q= to the current query instead of by container id.
var hdtb = sm.$("#hdtb");
var currentQuery = new URL(location.href).searchParams.get("q");
var related = [];
var seenRelated = {};
sm.all("a[href^='/search?']").forEach(function (a) {
  if (related.length >= 10) return;
  if (hdtb && hdtb.contains(a)) return;
  var u;
  try {
    u = new URL(a.href, location.href);
  } catch (e) {
    return;
  }
  if (u.searchParams.get("q") === currentQuery) return;
  var text = sm.text(a);
  if (!text || seenRelated[text]) return;
  seenRelated[text] = true;
  related.push(text);
});

sm.post("result", { url: location.href, organic: organic, related: related });
