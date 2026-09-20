// ICPC Broadcast Display — shared display logic for before/contesting/after modes.
"use strict";

var BroadcastDisplay = (function() {
  var mode = "";
  var pages = [];
  var currentIdx = -1;
  var pageTimer = null;
  var ws = null;
  var clockTimer = null;
  var fontLoaded = false;
  var lockCheckTimer = null;

  // Batched countdown cache
  var countdownCache = { target: null, clientOffset: 0 };

  function init(m) {
    mode = m;
    document.title = "ICPC " + ({before:"赛前",contesting:"赛中",after:"赛后"})[mode] + " 广播";
    loadFont();
    initGlobalClock();
    startFullscreenLock();
    connectWS();
  }

  var wsReconnect = false;
  var pingTimer = null;
  function connectWS() {
    var proto = location.protocol === "https:" ? "wss:" : "ws:";
    if (ws) {
      try { ws.close(); } catch (e) {}
    }
    if (pingTimer) {
      clearInterval(pingTimer);
      pingTimer = null;
    }
    ws = new WebSocket(proto + "//" + location.host + "/ws/broadcast?mode=" + mode);
    ws.onopen = function() {
      // Only do full HTTP refresh on first connect. Reconnects just resume.
      if (!wsReconnect) {
        pollPagesHTTP();
        wsReconnect = true;
      }
      // Stop local fallback timer.
      clearTimeout(pageTimer);
      pingTimer = setInterval(function(){ if(ws&&ws.readyState===WebSocket.OPEN) ws.send('ping'); }, 30000);
    };
    ws.onmessage = function(e) {
      try { var msg = JSON.parse(e.data); handleWSMessage(msg); } catch(err) {}
    };
    ws.onclose = function() {
      if (pingTimer) {
        clearInterval(pingTimer);
        pingTimer = null;
      }
      // Start local carousel as fallback.
      if (pages.length > 1) {
        scheduleNext(pages[currentIdx] || pages[0]);
      }
      setTimeout(connectWS, 5000);
    };
  }

  function handleWSMessage(msg) {
    if (msg.server_time) serverTimeOffset = Date.now() - new Date(msg.server_time).getTime();
    if (msg.started_at) syncStartedAt = msg.started_at;
    if (msg.type === "pages_updated" && msg.pages) {
      pages = msg.pages;
      if (pages.length === 0) { showEmpty(); return; }
      // WS active: server controls page. Stay on current or switch if needed.
      if (currentIdx < 0 || currentIdx >= pages.length) currentIdx = 0;
      // Don't re-render if already showing the same page (avoids flash).
      renderCurrentPage();
    } else if (msg.type === "page_switch") {
      // Server tells us which page to show.
      if (msg.page_index !== undefined && pages.length > 0) {
        var idx = msg.page_index;
        if (idx >= 0 && idx < pages.length && idx !== currentIdx) {
          currentIdx = idx;
          renderCurrentPage();
        }
      }
    } else if (msg.type === "sync_reset") {
      syncStartedAt = msg.started_at;
      if (pages.length > 0) { currentIdx = 0; renderCurrentPage(); }
    }
  }

  function pollPagesHTTP() {
    fetch("/api/broadcast/pages?mode=" + mode).then(function(r){return r.json()}).then(function(data){
      var newPages = data.pages || data;
      if (data.server_time) serverTimeOffset = Date.now() - new Date(data.server_time).getTime();
      if (data.started_at) syncStartedAt = data.started_at;
      pages = newPages;
      if (pages.length === 0) { showEmpty(); return; }
      currentIdx = calcSyncedPageIndex();
      renderCurrentPage();
    }).catch(function(){});
    fetch("/api/broadcast/config/countdown").then(function(r){return r.json()}).then(function(d){
      countdownCache.target = d.target || null;
      if (d.server_time) countdownCache.clientOffset = Date.now() - new Date(d.server_time).getTime();
    }).catch(function(){});
  }

  // 字号存的是 vh 百分比 (如 "5" = 5vh)，展示端直接用 vh 单位
  function scaleFont(fontSize) {
    if(!fontSize) return '';
    var v = parseFloat(fontSize);
    if(!v) return fontSize; // fallback for old "48px" values
    return v + 'vh';
  }

  // ---- Global & Component Clock ----
  function initGlobalClock() {
    var clockEl = document.createElement("div");
    clockEl.id = "icpc-global-clock";
    // 右上角固定时钟样式，大小使用相对视口单位(vw)以适配不同分辨率的大屏
    clockEl.style.cssText = "position:fixed; top:3vh; right:2.5vw; font-family:monospace; font-size:2.8vw; font-weight:bold; color:#fff; text-shadow:0 4px 12px rgba(0,0,0,0.8); z-index:99999; pointer-events:none; letter-spacing:2px;";
    document.body.appendChild(clockEl);

    if (clockTimer) clearInterval(clockTimer);
    clockTimer = setInterval(function() {
      var d = new Date();
      var h = String(d.getHours()).padStart(2, '0');
      var m = String(d.getMinutes()).padStart(2, '0');
      var s = String(d.getSeconds()).padStart(2, '0');
      var timeStr = h + ":" + m + ":" + s;

      // 1. 更新全局时钟
      clockEl.textContent = timeStr;

      // 2. 更新画布内部的 clock 组件
      document.querySelectorAll(".bc-clock-val").forEach(function(el) {
        el.textContent = timeStr;
      });

      // 3. 顺便更新可能存在的倒计时
      updateAllCountdownEls();
    }, 1000);
  }

  // ---- Font loading ----
  function loadFont() {
    fetch("/api/broadcast/config").then(function(r){return r.json()}).then(function(cfg){
      if (!cfg.active_font) return;
      fetch("/api/broadcast/fonts").then(function(r){return r.json()}).then(function(fonts){
        var f = fonts.filter(function(x){return x.filename === cfg.active_font})[0];
        if (!f) return;
        var fmt = f.format === "ttf" ? "truetype" : f.format;
        var style = document.createElement("style");
        style.textContent =
            '@font-face{font-family:"BroadcastFont";src:url("/broadcast/fonts/'+f.filename+'") format("'+fmt+'");' +
            'font-display:swap}';
        document.head.appendChild(style);
        fontLoaded = true;
        document.querySelectorAll(".broadcast-item").forEach(function(el){el.style.fontFamily='"BroadcastFont",sans-serif'});
      });
    }).catch(function(){});
  }

  // ---- Fullscreen lock ----
  function startFullscreenLock() {
    requestFS();
    document.addEventListener("click", requestFS);
    document.addEventListener("keydown", blockKeys, true);
    document.addEventListener("keyup", blockKeys, true);
    document.addEventListener("contextmenu", function(e){e.preventDefault()});
    lockCheckTimer = setInterval(function(){
      if (!document.fullscreenElement && !document.webkitFullscreenElement) requestFS();
    }, 500);
  }

  function requestFS() {
    var el = document.documentElement;
    if (el.requestFullscreen) el.requestFullscreen().catch(function(){});
    else if (el.webkitRequestFullscreen) el.webkitRequestFullscreen();
  }

  function blockKeys(e) {
    var k = e.key || e.code || "";
    if (k === "Escape" || k === "F11" || e.keyCode === 27 || e.keyCode === 122) { e.preventDefault(); e.stopPropagation(); return false; }
    if (e.ctrlKey && (k === "w" || k === "W" || k === "t" || k === "T" || k === "n" || k === "N" || e.keyCode === 87 || e.keyCode === 84 || e.keyCode === 78)) { e.preventDefault(); e.stopPropagation(); return false; }
    if (e.ctrlKey && e.shiftKey && (k === "Tab" || e.keyCode === 9)) { e.preventDefault(); e.stopPropagation(); return false; }
    if (e.altKey && (k === "F4" || e.keyCode === 115)) { e.preventDefault(); e.stopPropagation(); return false; }
    if (k === "Meta" || k === "OS" || e.keyCode === 91 || e.keyCode === 92) { e.preventDefault(); e.stopPropagation(); return false; }
  }

  // ---- Page polling (time-synced) ----
  var serverTimeOffset = 0; // clientNow - serverNow
  var syncStartedAt = null; // server time when mode started

  function serverNow() { return Date.now() - serverTimeOffset; }

  function calcSyncedPageIndex() {
    // Time-synced mode: calculate page from server start time.
    if (syncStartedAt && pages.length > 1) {
      var elapsed = serverNow() - new Date(syncStartedAt).getTime();
      if (elapsed >= 0) {
        var total = 0;
        for (var i = 0; i < pages.length; i++) total += (pages[i].duration_ms || 10000);
        if (total > 0) {
          var cycle = elapsed % total;
          var acc = 0;
          for (var j = 0; j < pages.length; j++) {
            acc += (pages[j].duration_ms || 10000);
            if (cycle < acc) return j;
          }
        }
      }
    }
    // Fallback: keep current index.
    if (currentIdx < 0 || currentIdx >= pages.length) return 0;
    return currentIdx;
  }

  function calcNextSwitchMs() {
    // Time-synced mode.
    if (syncStartedAt && pages.length > 1) {
      var elapsed = serverNow() - new Date(syncStartedAt).getTime();
      var total = 0;
      for (var i = 0; i < pages.length; i++) total += (pages[i].duration_ms || 10000);
      if (total > 0) {
        var cycle = elapsed % total;
        var acc = 0;
        for (var j = 0; j < pages.length; j++) {
          acc += (pages[j].duration_ms || 10000);
          if (cycle < acc) return acc - cycle;
        }
      }
    }
    // Fallback: use current page's own duration.
    if (currentIdx >= 0 && currentIdx < pages.length) {
      return pages[currentIdx].duration_ms || 10000;
    }
    return 10000;
  }

  function showEmpty() {
    clearTimeout(pageTimer);
    document.getElementById("broadcast-container").innerHTML =
        '<div style="display:flex;align-items:center;justify-content:center;height:100%;color:#888;font-size:24px;">等待广播配置...</div>';
  }

  // ---- Page rendering ----
  function renderCurrentPage() {
    if (pages.length === 0) return;
    var page = pages[currentIdx];
    var c = document.getElementById("broadcast-container");
    var pageEl = document.createElement("div");
    pageEl.className = "broadcast-page";
    pageEl.style.background = page.bg_color || "#000";

    (page.items || []).forEach(function(it){
      var el = renderItem(it);
      if (el) pageEl.appendChild(el);
    });

    var oldPage = c.querySelector(".broadcast-page.active");
    if (oldPage) {
      oldPage.classList.remove("active");
      setTimeout(function(){ if (oldPage.parentNode) oldPage.parentNode.removeChild(oldPage); }, 800);
    }

    c.appendChild(pageEl);
    pageEl.offsetHeight; // trigger reflow
    pageEl.classList.add("active");
    scheduleNext(page);
  }

  function scheduleNext(page) {
    clearTimeout(pageTimer);
    if (pages.length <= 1) return;
    // If WS is connected, server controls timing — no local timer needed.
    if (ws && ws.readyState === WebSocket.OPEN) return;
    // Fallback: local carousel when WS is disconnected.
    var dur = (page && page.duration_ms) ? page.duration_ms : 10000;
    pageTimer = setTimeout(function(){
      currentIdx = (currentIdx + 1) % pages.length;
      renderCurrentPage();
    }, dur);
  }

  // ---- Item rendering ----
  function renderItem(it) {
    var el = document.createElement("div");
    el.className = "broadcast-item " + it.item_type;
    el.style.position = "absolute";
    el.style.left = it.pos_x + "%";
    el.style.top = it.pos_y + "%";
    el.style.width = it.width + "%";
    el.style.height = it.height + "%";
    el.style.fontSize = scaleFont(it.font_size);
    el.style.color = it.font_color;
    el.style.fontWeight = it.font_weight;
    el.style.backgroundColor = it.bg_color !== "transparent" ? it.bg_color : "";
    el.style.borderRadius = it.border_radius;
    el.style.zIndex = it.z_index;

    if (fontLoaded) el.style.fontFamily = '"BroadcastFont",sans-serif';
    if (it.animation === "fadeIn") el.style.animation = "fadeIn 0.5s ease";
    else if (it.animation === "slideUp") el.style.animation = "slideUp 0.5s ease";
    else if (it.animation === "pulse") el.style.animation = "pulse 2s ease infinite";

    var alignMap = { left: 'flex-start', center: 'center', right: 'flex-end' };

    switch (it.item_type) {
      case "text":
        el.textContent = it.content;
        el.style.display = "flex";
        el.style.alignItems = "center";
        el.style.justifyContent = alignMap[it.text_align] || 'center';
        el.style.textAlign = it.text_align || 'center';
        el.style.whiteSpace = "pre-wrap"; // 完美支持后台敲入的回车换行
        el.style.lineHeight = "1.2";
        break;

      case "clock":
        el.textContent = "00:00:00";
        el.className += " bc-clock-val"; // 打上标记，交由全局定时器接管
        el.style.display = "flex";
        el.style.alignItems = "center";
        el.style.justifyContent = alignMap[it.text_align] || 'center';
        el.style.fontFamily = "monospace";
        el.style.whiteSpace = "nowrap";
        break;

      case "image":
        var img = document.createElement("img");
        img.src = it.content;
        img.draggable = false;
        img.style.width = "100%";
        img.style.height = "100%";
        img.style.objectFit = "contain";
        el.appendChild(img);
        break;

      case "countdown":
        // 兼容旧的倒计时页面
        el.textContent = "--:--:--";
        break;

      case "scrolling_notice":
        renderScrollingNotice(el, it);
        break;
    }
    return el;
  }

  // ---- Countdown Legacy Update ----
  function updateAllCountdownEls() {
    var els = document.querySelectorAll(".broadcast-item.countdown");
    if (!els.length) return;
    if (!countdownCache.target) {
      els.forEach(function(el){ el.textContent = "--:--:--"; });
      return;
    }
    var target = new Date(countdownCache.target).getTime();
    var remaining = target - (Date.now() - countdownCache.clientOffset);
    var text;
    if (remaining <= 0) {
      text = "00:00:00";
    } else {
      var h = Math.floor(remaining / 3600000);
      var m = Math.floor((remaining % 3600000) / 60000);
      var s = Math.floor((remaining % 60000) / 1000);
      text = pad(h) + ":" + pad(m) + ":" + pad(s);
    }
    els.forEach(function(el){ el.textContent = text; });
  }

  function pad(n) { return n < 10 ? "0" + n : "" + n; }

  // ---- Scrolling notice ----
  function renderScrollingNotice(el, it) {
    var extra = {};
    try { extra = JSON.parse(it.extra_json); } catch(e) {}
    var speed = extra.scroll_speed || 120;
    var span = document.createElement("span");
    span.className = "scroll-text";
    span.textContent = it.content;
    span.style.fontSize = scaleFont(it.font_size);
    span.style.color = it.font_color;
    span.style.fontWeight = it.font_weight;
    el.style.display = "flex";
    el.style.alignItems = "center";
    el.style.overflow = "hidden";
    el.style.whiteSpace = "nowrap";
    el.appendChild(span);

    // 动态计算滚动动画时间以适配不同长度的通告
    requestAnimationFrame(function() {
      var duration = (el.offsetWidth + span.offsetWidth) / speed;
      span.style.animation = "scroll-left " + duration + "s linear infinite";
    });
  }

  return { init: init };
})();