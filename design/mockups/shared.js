// Mock data and render helpers shared by the four style mockups.
// Each page provides the layout and CSS; this file fills elements marked
// with data-slot="…" using the same content, so the styles can be compared.

(function () {
  // ---------- data ----------
  const day = 24 * 3600 * 1000;
  const curStart = Date.UTC(2026, 9, 12); // 2026-10-12 .. 2026-11-08
  const prevStart = curStart - 28 * day;
  const fmtDay = (t) => {
    const d = new Date(t);
    return `${d.getUTCMonth() + 1}-${String(d.getUTCDate()).padStart(2, "0")}`;
  };
  const wave = (i, base, slope, amp, seed) => {
    const weekend = [5, 6].includes((i + 0) % 7) ? -0.16 : 0;
    const noise = Math.sin(i * 1.7 + seed) * 0.05 + Math.cos(i * 0.9 + seed * 2) * 0.04;
    return Math.round(base * (1 + weekend + noise) + slope * i + amp * Math.sin(i / 4));
  };
  const cur = Array.from({ length: 28 }, (_, i) => wave(i, 400, 4.2, 10, 1));
  const prev = Array.from({ length: 28 }, (_, i) => wave(i, 370, 1.0, 8, 4));
  const curDays = cur.map((_, i) => fmtDay(curStart + i * day));
  const prevDays = prev.map((_, i) => fmtDay(prevStart + i * day));
  const sum = (a) => a.reduce((x, y) => x + y, 0);
  const clicks = sum(cur);
  const clicksPrev = sum(prev);
  const n = (v) => v.toLocaleString("en-US");

  const D = {
    account: { name: "askuy", plan: "Pro", credits: "312 / 1,000" },
    sites: [
      { host: "thumbnailsnap.com", score: 92, state: "good", active: true, note: "GSC · GA4 · Bing" },
      { host: "sitemoment.com", score: 78, state: "warn", note: "GSC · GA4" },
      { host: "aitoolsradar.io", score: 61, state: "bad", note: "1 个 P0" },
      { host: "heyaer.com", score: 88, state: "good", note: "GSC · Bing" },
      { host: "kitionai.com", score: 83, state: "good", note: "GSC" },
    ],
    nav: [
      ["overview", "概览", true],
      ["keywords", "关键词"],
      ["pages", "页面"],
      ["health", "体检"],
      ["tasks", "任务"],
      ["reports", "报告"],
      ["collectors", "采集器"],
    ],
    sync: [
      { src: "Search Console", at: "09:42", ok: true, note: "数据截至 11-08" },
      { src: "GA4", at: "09:42", ok: true, note: "properties/41822" },
      { src: "Bing", at: "09:40", ok: true, note: "API key" },
    ],
    kpis: [
      { label: "点击", value: n(clicks), delta: `+${(((clicks - clicksPrev) / clicksPrev) * 100).toFixed(1)}%`, up: true, src: "GSC", spark: cur },
      { label: "展示", value: "486.2K", delta: "+9.4%", up: true, src: "GSC", spark: cur.map((v, i) => v * 38 + (i % 5) * 900) },
      { label: "CTR", value: "2.57%", delta: "+0.20 pt", up: true, src: "GSC", spark: cur.map((v, i) => 2.3 + i * 0.01 + Math.sin(i) * 0.08) },
      { label: "平均排名", value: "14.3", delta: "提升 2.1", up: true, src: "GSC", spark: cur.map((v, i) => 17 - i * 0.09 + Math.cos(i) * 0.4) },
      { label: "GA4 会话", value: "15,902", delta: "+12.6%", up: true, src: "GA4", spark: cur.map((v, i) => v * 1.25 + Math.sin(i * 2) * 20) },
      { label: "Bing 点击", value: "1,236", delta: "+31.0%", up: true, src: "Bing", spark: cur.map((v, i) => v / 10 + i * 0.6 + Math.sin(i * 1.3) * 4) },
    ],
    chart: { cur, prev, curDays, prevDays },
    queries: [
      { q: "youtube thumbnail maker", pos: 6.2, chg: +1.4, clicks: 2310, impr: "61.2K", vol: "74K", kd: 48, tag: ["target", "目标词"] },
      { q: "thumbnail generator ai", pos: 11.8, chg: +3.0, clicks: 842, impr: "52.0K", vol: "33K", kd: 41, tag: ["target", "目标词 · 第 2 页"] },
      { q: "free thumbnail maker no watermark", pos: 4.1, chg: +0.6, clicks: 1120, impr: "18.4K", vol: "8.1K", kd: 22, tag: ["found", "意外流量"] },
      { q: "youtube thumbnail size", pos: 9.3, chg: -0.8, clicks: 690, impr: "40.3K", vol: "110K", kd: 35, tag: ["found", "意外流量"] },
      { q: "thumbnail maker for podcast", pos: 18.6, chg: null, clicks: 54, impr: "3.1K", vol: "1.9K", kd: 12, tag: ["scene", "场景词 · 新出现"] },
    ],
    health: {
      score: 92, at: "11-10 08:00", p0: 0, p1: 2, p2: 7,
      items: [
        ["P1", "3 个页面从 200 变成 404", "/templates/gaming-3 等"],
        ["P1", "12 个页面已抓取但未编入索引", "GSC 网址检查 · 11-09"],
        ["P2", "41 张图片缺少 alt", "站点体检 · 300 页"],
      ],
    },
    tasks: [
      ["进行中", "为 thumbnail generator ai 补一节「AI 生成 vs 模板」对比", "验收：28 天内排名进前 10"],
      ["待验收", "修复 /templates/gaming-3 的 404，301 到新地址", "验收：下次体检返回 200"],
    ],
    agent: {
      model: "SiteMoment 托管 · gpt-5.6-sol",
      ask: "thumbnail generator ai 一直在第二页，下一步该怎么做？",
      tools: [
        { kind: "local", kindLabel: "本地数据", title: "GSC 查询：thumbnail generator ai，近 90 天", meta: "0.2 秒 · 本地 SQLite", state: "ok" },
        { kind: "collector", kindLabel: "采集器", title: "Google 搜索：thumbnail generator ai（美国 · 英语）", meta: "隐藏窗口 · 3.1 秒 · 缓存到 11-17", state: "ok" },
        { kind: "cloud", kindLabel: "SiteMoment 数据", title: "关键词难度和搜索量", meta: "2 积分 · web.cafe · 11-10", state: "ok" },
        { kind: "collector", kindLabel: "采集器", title: "抓取第 1 名页面：canva.com/create/thumbnails", meta: "排队中，8 秒后开始（限速）", state: "run" },
      ],
      answer: {
        verdict: "值得冲第一页。排名 11.8，近 28 天上升 3.0；第一页有 2 个小站（DR 18、DR 26），说明不是大站垄断。",
        basis: [
          "GSC：近 90 天展示 52.0K，点击 842，排名从 14.8 升到 11.8（本地数据，截至 11-08）",
          "Google 第一页：Canva、Adobe Express、Fotor 之外，第 7、9 名是小站（采集器，11-10）",
          "KD 41，月搜索量 33K（SiteMoment 数据 · web.cafe，11-10）",
        ],
        advice: [
          "在 /ai-thumbnail-generator 补一节「AI 生成 vs 模板」对比，回答第一页都没回答的“能不能商用”",
          "从「youtube thumbnail maker」页加一条锚文本内链",
        ],
        check: "28 天后排名进前 10，展示不低于 52K。",
      },
    },
    collectorQueue: { running: 1, queued: 2, today: "Google 搜索 23 / 200" },
  };

  // ---------- helpers ----------
  const h = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const stateLabel = { good: "健康", warn: "注意", bad: "有 P0" };
  const stateIcon = { good: "●", warn: "▲", bad: "■" };

  function spark(values, w = 96, h2 = 28) {
    const min = Math.min(...values), max = Math.max(...values);
    const pts = values.map((v, i) => {
      const x = (i / (values.length - 1)) * (w - 4) + 2;
      const y = h2 - 3 - ((v - min) / (max - min || 1)) * (h2 - 6);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    });
    return `<svg class="spark" viewBox="0 0 ${w} ${h2}" width="${w}" height="${h2}" aria-hidden="true"><polyline points="${pts.join(" ")}" fill="none" stroke="var(--spark, var(--series-1))" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/></svg>`;
  }

  const R = {};

  R.account = (el) => {
    el.innerHTML = `<span class="acct-avatar">A</span><span class="acct-text"><b>${D.account.name}</b><small>${D.account.plan} · 额度 ${D.account.credits}</small></span>`;
  };

  R.sites = (el) => {
    el.innerHTML = D.sites.map((s) => `
      <a class="site${s.active ? " is-active" : ""}" href="#">
        <span class="site-fav">${s.host[0].toUpperCase()}</span>
        <span class="site-main"><span class="site-name">${s.host}</span><span class="site-note">${s.note}</span></span>
        <span class="site-score st-${s.state}" title="${stateLabel[s.state]}"><i>${stateIcon[s.state]}</i>${s.score}</span>
      </a>`).join("") + `<a class="site site-add" href="#"><span class="site-fav">+</span><span class="site-main"><span class="site-name">添加站点</span></span></a>`;
  };

  R.nav = (el) => {
    el.innerHTML = D.nav.map(([k, label, on]) => `<a class="nav-item${on ? " is-active" : ""}" href="#" data-k="${k}">${label}</a>`).join("");
  };

  R.sync = (el) => {
    el.innerHTML = D.sync.map((s) => `<span class="sync-chip" title="${s.note}"><i class="st-good">●</i>${s.src}<small>${s.at}</small></span>`).join("");
  };

  R.kpis = (el) => {
    el.innerHTML = D.kpis.map((k) => `
      <div class="kpi">
        <div class="kpi-label">${k.label}<span class="kpi-src">${k.src}</span></div>
        <div class="kpi-value">${k.value}</div>
        <div class="kpi-foot"><span class="kpi-delta ${k.up ? "up" : "down"}">${k.up ? "↑" : "↓"} ${k.delta}</span>${spark(k.spark)}</div>
      </div>`).join("");
  };

  R.queries = (el) => {
    el.innerHTML = `<table class="qtable">
      <thead><tr><th>关键词</th><th class="num">排名</th><th class="num">点击</th><th class="num">展示</th><th class="num">月搜索量</th><th class="num">KD</th><th>类型</th></tr></thead>
      <tbody>${D.queries.map((r) => `
        <tr><td class="q">${r.q}</td>
          <td class="num">${r.pos.toFixed(1)}${r.chg == null ? '<span class="chg new">新</span>' : `<span class="chg ${r.chg > 0 ? "up" : "down"}">${r.chg > 0 ? "↑" : "↓"}${Math.abs(r.chg).toFixed(1)}</span>`}</td>
          <td class="num">${n(r.clicks)}</td><td class="num">${r.impr}</td><td class="num">${r.vol}</td><td class="num">${r.kd}</td>
          <td><span class="tag tag-${r.tag[0]}">${r.tag[1]}</span></td></tr>`).join("")}
      </tbody></table>
      <p class="src-note">排名、点击、展示来自 GSC（截至 11-08）；月搜索量和 KD 来自 SiteMoment 数据（web.cafe，11-10）。</p>`;
  };

  R.health = (el) => {
    const x = D.health;
    el.innerHTML = `
      <div class="health-head">
        <div class="health-score"><b>${x.score}</b><small>健康分</small></div>
        <div class="health-counts">
          <span class="pc pc-0">P0 <b>${x.p0}</b></span><span class="pc pc-1">P1 <b>${x.p1}</b></span><span class="pc pc-2">P2 <b>${x.p2}</b></span>
        </div>
        <small class="health-at">体检 ${x.at}</small>
      </div>
      <ul class="health-list">${x.items.map(([p, t, m]) => `<li><span class="pri pri-${p}">${p}</span><span class="hl-main"><b>${t}</b><small>${m}</small></span></li>`).join("")}</ul>`;
  };

  R.tasks = (el) => {
    el.innerHTML = `<ul class="task-list">${D.tasks.map(([s, t, c]) => `<li><span class="task-state">${s}</span><span class="task-main"><b>${t}</b><small>${c}</small></span></li>`).join("")}</ul>`;
  };

  R.agentModel = (el) => { el.textContent = D.agent.model; };

  R.agent = (el) => {
    const a = D.agent;
    const stateTxt = { ok: "完成", run: "进行中", confirm: "待确认" };
    el.innerHTML = `
      <div class="msg msg-user">${h(a.ask)}</div>
      <div class="tools">${a.tools.map((t) => `
        <div class="tool tool-${t.kind} is-${t.state}">
          <span class="tool-icon" aria-hidden="true">${t.state === "run" ? '<i class="spin"></i>' : "✓"}</span>
          <span class="tool-body"><span class="tool-kind">${t.kindLabel}</span><span class="tool-title">${h(t.title)}</span><span class="tool-meta">${h(t.meta)}</span></span>
          <span class="tool-state">${stateTxt[t.state]}</span>
        </div>`).join("")}
      </div>
      <div class="msg msg-ai">
        <h4>结论</h4><p>${h(a.answer.verdict)}</p>
        <h4>依据</h4><ul>${a.answer.basis.map((b) => `<li>${h(b)}</li>`).join("")}</ul>
        <h4>建议</h4><ol>${a.answer.advice.map((b) => `<li>${h(b)}</li>`).join("")}</ol>
        <p class="check"><b>验收</b>${h(a.answer.check)}</p>
        <div class="msg-actions"><button>加入任务</button><button>复制</button></div>
      </div>`;
  };

  R.composer = (el) => {
    el.innerHTML = `
      <div class="composer-box">
        <textarea rows="2" placeholder="问 Agent：比如「这周哪些页面掉得最厉害？」"></textarea>
        <div class="composer-bar">
          <span class="chip">站点：thumbnailsnap.com</span>
          <span class="chip">付费数据：先确认</span>
          <span class="chip">${D.agent.model.split(" · ")[1]}</span>
          <button class="send" aria-label="发送">↑</button>
        </div>
      </div>`;
  };

  R.queue = (el) => {
    const q = D.collectorQueue;
    el.innerHTML = `<span><i class="spin"></i> 采集器运行 ${q.running}，排队 ${q.queued}</span><span>${q.today}</span>`;
  };

  // ---------- line chart: clicks, this period vs previous ----------
  R.chart = (el) => {
    const { cur, prev, curDays, prevDays } = D.chart;
    el.classList.add("chart-wrap");
    el.innerHTML = `
      <div class="chart-head">
        <div><div class="chart-title">每日点击 · 本期与上期</div><div class="chart-sub">GSC · 网页搜索 · 10-12 至 11-08，对比前 28 天</div></div>
        <div class="legend">
          <span><i class="lg lg-1"></i>本期 ${n(clicks)}</span>
          <span><i class="lg lg-2"></i>上期 ${n(clicksPrev)}</span>
        </div>
      </div>
      <div class="chart-plot"><svg role="img" aria-label="每日点击折线图，本期与上期"></svg><div class="tip" hidden></div></div>
      <details class="chart-table"><summary>查看数据表</summary><table><thead><tr><th>日期</th><th>本期</th><th>上期日期</th><th>上期</th></tr></thead><tbody>${cur.map((v, i) => `<tr><td>${curDays[i]}</td><td>${v}</td><td>${prevDays[i]}</td><td>${prev[i]}</td></tr>`).join("")}</tbody></table></details>`;
    const plot = el.querySelector(".chart-plot");
    const svg = el.querySelector("svg");
    const tip = el.querySelector(".tip");

    function draw() {
      const W = Math.max(280, plot.clientWidth), H = Math.max(140, plot.clientHeight || 200);
      const m = { l: 40, r: 56, t: 10, b: 24 };
      const all = cur.concat(prev);
      const lo = Math.floor(Math.min(...all) / 100) * 100, hi = Math.ceil(Math.max(...all) / 100) * 100;
      const x = (i) => m.l + (i / 27) * (W - m.l - m.r);
      const y = (v) => m.t + (1 - (v - lo) / (hi - lo)) * (H - m.t - m.b);
      const ticks = [];
      for (let v = lo; v <= hi; v += 100) ticks.push(v);
      const line = (arr) => arr.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join("");
      svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
      svg.setAttribute("width", W);
      svg.setAttribute("height", H);
      svg.innerHTML = `
        ${ticks.map((v) => `<line x1="${m.l}" x2="${W - m.r}" y1="${y(v)}" y2="${y(v)}" class="grid"/><text x="${m.l - 8}" y="${y(v) + 4}" class="axis" text-anchor="end">${v}</text>`).join("")}
        ${[0, 7, 14, 21, 27].map((i) => `<text x="${x(i)}" y="${H - 6}" class="axis" text-anchor="middle">${curDays[i]}</text>`).join("")}
        <path d="${line(prev)}" class="ln ln-2"/>
        <path d="${line(cur)}" class="ln ln-1"/>
        <text x="${x(27) + 8}" y="${y(cur[27]) + 4}" class="dl">本期</text>
        <text x="${x(27) + 8}" y="${y(prev[27]) + 4}" class="dl">上期</text>
        <line class="xh" y1="${m.t}" y2="${H - m.b}" visibility="hidden"/>
        <circle class="dot dot-2" r="4" visibility="hidden"/><circle class="dot dot-1" r="4" visibility="hidden"/>
        <rect x="${m.l}" y="0" width="${W - m.l - m.r}" height="${H}" fill="transparent" class="hit"/>`;
      const xh = svg.querySelector(".xh"), d1 = svg.querySelector(".dot-1"), d2 = svg.querySelector(".dot-2");
      const hit = svg.querySelector(".hit");
      const show = (evt) => {
        const r = svg.getBoundingClientRect();
        const px = ((evt.clientX - r.left) / r.width) * W;
        const i = Math.max(0, Math.min(27, Math.round(((px - m.l) / (W - m.l - m.r)) * 27)));
        for (const el2 of [xh, d1, d2]) el2.setAttribute("visibility", "visible");
        xh.setAttribute("x1", x(i)); xh.setAttribute("x2", x(i));
        d1.setAttribute("cx", x(i)); d1.setAttribute("cy", y(cur[i]));
        d2.setAttribute("cx", x(i)); d2.setAttribute("cy", y(prev[i]));
        const diff = (((cur[i] - prev[i]) / prev[i]) * 100).toFixed(1);
        tip.innerHTML = `<b>${curDays[i]}</b><span><i class="lg lg-1"></i>本期 ${cur[i]}</span><span><i class="lg lg-2"></i>上期 ${prev[i]}（${prevDays[i]}）</span><span>变化 ${diff > 0 ? "+" : ""}${diff}%</span>`;
        tip.hidden = false;
        const left = (x(i) / W) * plot.clientWidth;
        tip.style.left = `${Math.min(left + 12, plot.clientWidth - tip.offsetWidth - 4)}px`;
        tip.style.top = `4px`;
      };
      hit.addEventListener("mousemove", show);
      hit.addEventListener("mouseleave", () => { tip.hidden = true; for (const el2 of [xh, d1, d2]) el2.setAttribute("visibility", "hidden"); });
    }
    draw();
    new ResizeObserver(draw).observe(plot);
  };

  function fill() {
    document.querySelectorAll("[data-slot]").forEach((el) => {
      const fn = R[el.dataset.slot];
      if (fn) fn(el);
    });
  }
  window.SM = { data: D, render: R, fill };
  document.addEventListener("DOMContentLoaded", fill);
})();
