// SPDX-License-Identifier: AGPL-3.0-or-later
(function () {
  "use strict";
  // Before touching anything else: only run on a page the collector
  // declared (exact host, https only). A page can navigate itself (a
  // redirect, a stale late-arriving script injection after the window was
  // released, the app's own wails:// boot page) to somewhere the run's
  // nonce was never meant to be exposed to; bail out before the nonce or
  // input is even assigned to a variable the page's own scripts could read.
  var allowedHosts = __HOSTS__;
  var hostOK = false;
  for (var hi = 0; hi < allowedHosts.length; hi++) {
    if (location.hostname === allowedHosts[hi]) { hostOK = true; break; }
  }
  if (location.protocol !== "https:" || !hostOK) return;
  var run = __RUN__, nonce = __NONCE__, input = __INPUT__;
  var finished = false;
  function post(kind, data) {
    var msg = "smc:" + JSON.stringify({ run: run, nonce: nonce, kind: kind, data: data });
    if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external) {
      window.webkit.messageHandlers.external.postMessage(msg);
    } else if (window.chrome && window.chrome.webview) {
      window.chrome.webview.postMessage(msg);
    }
  }
  // end posts the run's outcome once; later calls are ignored.
  function end(kind, data) {
    if (finished) return;
    finished = true;
    post(kind, data);
  }
  var sm = {
    input: input,
    post: post,
    $: function (sel, root) { return (root || document).querySelector(sel); },
    all: function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); },
    text: function (el) { return el ? (el.textContent || "").replace(/\s+/g, " ").trim() : ""; },
    exists: function (sel) { return !!document.querySelector(sel); },
    sleep: function (ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); },
    // waitFor resolves with the first element matching sel, or null after ms (default 8000).
    waitFor: function (sel, ms) {
      var deadline = Date.now() + (ms || 8000);
      return new Promise(function (resolve) {
        (function poll() {
          var el = document.querySelector(sel);
          if (el) return resolve(el);
          if (Date.now() >= deadline) return resolve(null);
          setTimeout(poll, 200);
        })();
      });
    },
    done: function (data) { end("result", data); },
    needHuman: function (reason) { end("need_human", { reason: String(reason || ""), url: location.href }); },
    fail: function (message) { end("error", { message: String(message) }); },
    log: function (message) { post("log", { message: String(message) }); }
  };
  Promise.resolve().then(function () {
    return (async function (sm) {
      __COLLECTOR__
    })(sm);
  }).catch(function (e) {
    end("error", { message: String((e && e.message) || e) });
  });
})();
