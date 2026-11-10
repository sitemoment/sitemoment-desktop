if (location.pathname.indexOf("/sorry/") === 0 || sm.exists("form#captcha-form")) {
  sm.post("need_human", { reason: "captcha", url: location.href });
  return;
}
var organic = [];
sm.all("#search a h3").forEach(function (h3) {
  var a = h3.closest("a");
  if (!a || !a.href || organic.length >= 10) return;
  organic.push({ rank: organic.length + 1, url: a.href, title: sm.text(h3) });
});
var related = sm.all("#botstuff a[href^='/search']").map(sm.text).filter(Boolean).slice(0, 10);
sm.post("result", { url: location.href, organic: organic, related: related, html: document.documentElement.outerHTML.length });
