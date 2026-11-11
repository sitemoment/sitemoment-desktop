// google-serp: Google's first results page, read in the page (see
// internal/collect/sdk.js for `sm`). Google's markup in the WebView has no
// stable ids (#search, #rso, cite are gone), so results are found by shape:
// a link holding an <h3>.
function captcha() {
  return location.pathname.indexOf("/sorry/") === 0 || sm.exists("form#captcha-form") || sm.exists("#recaptcha");
}
if (captcha()) {
  sm.needHuman("Google 要求人机验证");
  return;
}
// Google first serves a page that runs a script check and reloads itself
// with the results. Wait for results; if none come, end quietly unless this
// is a real results page without results: the reload injects us again.
if (!(await sm.waitFor("a h3", 8000))) {
  if (captcha()) {
    sm.needHuman("Google 要求人机验证");
  } else if (sm.exists("#main") && !sm.exists("noscript meta[http-equiv='refresh']")) {
    sm.done({ url: location.href, query: sm.input.q, gl: sm.input.gl, hl: sm.input.hl, location: "", aiOverview: false, organic: [], related: [], paa: [] });
  }
  return;
}

var current = new URL(location.href).searchParams.get("q") || sm.input.q;

function isGoogle(host) {
  return /(^|\.)google\.[a-z.]+$/i.test(host);
}

// The result block: the highest ancestor of a that holds no other result title.
function blockOf(a) {
  var el = a;
  while (el.parentElement && el.parentElement !== document.body && el.parentElement.querySelectorAll("h3").length === 1) {
    el = el.parentElement;
  }
  return el;
}

var organic = [];
var seen = {};
sm.all("a h3").forEach(function (h3) {
  if (organic.length >= 10) return;
  var a = h3.closest("a");
  var title = sm.text(h3);
  if (!a || !title) return;
  var u;
  try {
    u = new URL(a.getAttribute("href") || "", location.href);
  } catch (e) {
    return;
  }
  var url = "", from = "", redirect = "";
  if (u.pathname === "/url" && isGoogle(u.hostname)) {
    var target = u.searchParams.get("q") || u.searchParams.get("url") || "";
    if (/^https?:\/\//i.test(target)) {
      url = target;
      from = "redirect";
    } else {
      redirect = u.pathname + u.search; // an opaque token: Go follows it
    }
  } else if ((u.protocol === "https:" || u.protocol === "http:") && !isGoogle(u.hostname)) {
    url = u.href;
    from = "direct";
  } else {
    return; // Google's own links (images, maps, more results)
  }
  var key = url || redirect;
  if (seen[key]) return;
  seen[key] = true;
  var linkText = sm.text(a);
  var breadcrumb = linkText.indexOf(title) === 0 ? linkText.slice(title.length).trim() : "";
  var snippet = sm.text(blockOf(a)).replace(linkText, "").trim();
  organic.push({
    rank: organic.length + 1, title: title, url: url, urlFrom: from, domain: "",
    breadcrumb: breadcrumb, snippet: snippet.slice(0, 300), redirect: redirect,
  });
});

// Related searches: /search links for another query (the tabs and tools
// above the results repeat the current query with a view switch).
var related = [];
var seenRelated = {};
sm.all("a[href^='/search?']").forEach(function (a) {
  if (related.length >= 10) return;
  var u;
  try {
    u = new URL(a.getAttribute("href"), location.href);
  } catch (e) {
    return;
  }
  var q = u.searchParams.get("q");
  if (!q || q === current || u.searchParams.get("tbm") || u.searchParams.get("udm") || u.searchParams.get("start")) return;
  var text = sm.text(a) || q;
  if (seenRelated[text.toLowerCase()]) return;
  seenRelated[text.toLowerCase()] = true;
  related.push(text);
});

// People also ask: short questions under a "People also ask" heading.
var paa = [];
sm.all("div, span, h2").some(function (el) {
  if (el.children.length > 1 || sm.text(el) !== "People also ask") return false;
  var box = el.parentElement;
  while (box && box !== document.body && sm.text(box).length < 40) box = box.parentElement;
  if (!box) return true;
  sm.all("div, span", box).forEach(function (q) {
    var t = sm.text(q);
    if (paa.length < 8 && q.children.length === 0 && /\?$/.test(t) && t.length >= 8 && t.length <= 200 && paa.indexOf(t) < 0) paa.push(t);
  });
  return true;
});

// The location Google says it used, from the footer ("United States · From your IP address").
var where = "";
var link = sm.$("a[href*='ws_settings_location']");
for (var el = link && link.parentElement, i = 0; el && i < 4 && !where; el = el.parentElement, i++) {
  var m = sm.text(el).match(/^(.*?)\s*[-·]?\s*(From your IP address|From your device|Based on your)/);
  if (m) where = m[1].trim();
}

sm.done({
  url: location.href, query: current, gl: sm.input.gl || "", hl: sm.input.hl || "", location: where,
  aiOverview: /AI Overview/.test(document.body ? document.body.textContent : ""),
  organic: organic, related: related, paa: paa,
});
