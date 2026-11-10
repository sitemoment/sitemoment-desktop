// SPDX-License-Identifier: AGPL-3.0-or-later
(function () {
  "use strict";
  var run = __RUN__, nonce = __NONCE__;
  function post(kind, data) {
    var msg = "smc:" + JSON.stringify({ run: run, nonce: nonce, kind: kind, data: data });
    if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external) {
      window.webkit.messageHandlers.external.postMessage(msg);
    } else if (window.chrome && window.chrome.webview) {
      window.chrome.webview.postMessage(msg);
    }
  }
  var sm = {
    post: post,
    $: function (sel, root) { return (root || document).querySelector(sel); },
    all: function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); },
    text: function (el) { return el ? (el.textContent || "").replace(/\s+/g, " ").trim() : ""; },
    exists: function (sel) { return !!document.querySelector(sel); }
  };
  try {
    (function (sm) {
      __COLLECTOR__
    })(sm);
  } catch (e) {
    post("error", { message: String(e && e.message || e) });
  }
})();
