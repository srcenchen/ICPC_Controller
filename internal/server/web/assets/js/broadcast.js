// Broadcast admin page — interact.js canvas (V3 - Clock & Corner Fix)
"use strict";
var bcMode = "before", bcPages = [], bcFonts = [], bcConfig = {}, bcSelPage = null, bcSelItem = null;

// ==================== CSS INJECTION ====================
function initStyles() {
  if ($('#bc-opt-styles').length) return;
  $('head').append(`
        <style id="bc-opt-styles">
            .bc-item-wrapper { position: absolute; box-sizing: border-box; touch-action: none; user-select: none; }
            .bc-item-wrapper.selected { outline: 2px solid #0d6efd; z-index: 99999 !important; }
            .bc-item { width: 100%; height: 100%; position: relative; overflow: hidden; pointer-events: none; }
            
            /* 8 个蓝色缩放手柄 */
            .bc-rs { position: absolute; width: 10px; height: 10px; background: #0d6efd; border: 1.5px solid #fff; border-radius: 50%; z-index: 10; display: none; }
            .bc-item-wrapper.selected .bc-rs { display: block; }
            .rs-nw { top: -5px; left: -5px; cursor: nwse-resize; }
            .rs-n  { top: -5px; left: calc(50% - 5px); cursor: ns-resize; }
            .rs-ne { top: -5px; right: -5px; cursor: nesw-resize; }
            .rs-e  { top: calc(50% - 5px); right: -5px; cursor: ew-resize; }
            .rs-se { bottom: -5px; right: -5px; cursor: nwse-resize; }
            .rs-s  { bottom: -5px; left: calc(50% - 5px); cursor: ns-resize; }
            .rs-sw { bottom: -5px; left: -5px; cursor: nesw-resize; }
            .rs-w  { top: calc(50% - 5px); left: -5px; cursor: ew-resize; }
        </style>
    `);
}

// ==================== REAL-TIME CLOCK ====================
function startClock() {
  if (window._bcClockTmr) clearInterval(window._bcClockTmr);
  window._bcClockTmr = setInterval(() => {
    let d = new Date();
    let h = String(d.getHours()).padStart(2,'0');
    let m = String(d.getMinutes()).padStart(2,'0');
    let s = String(d.getSeconds()).padStart(2,'0');
    // 只更新画布内的时钟组件
    $('.bc-clock-val').text(`${h}:${m}:${s}`);
  }, 1000);
}

function loadBroadcastAdmin() {
  initStyles();
  $.when($.getJSON("/api/broadcast/config"), $.getJSON("/api/broadcast/fonts"))
      .done(function(cfg, fonts){ bcConfig=cfg[0]; bcFonts=fonts[0]; loadPages(); })
      .fail(function(){ $("#content").html('<div class="empty-state">加载失败</div>'); });
}

function loadPages() {
  $.getJSON("/api/broadcast/pages?mode=" + bcMode, function(data){
    bcPages = data.pages || data; // unwrap if response is {pages: [...]}
    render();
  });
}

// ==================== RENDER (APP SHELL) ====================
function render() {
  if ($('#bc-app-root').length === 0) {
    $("#content").html(`
            <div id="bc-app-root">
                <div id="bc-header"></div>
                <div class="bc-editor-layout">
                    <div id="bc-sidebar"></div>
                    <div id="bc-main"></div>
                </div>
                <div id="bc-config" class="bc-config-layout"></div>
            </div>
        `);
    startClock();
    bindBroadcastEvents();
  }

  $('#bc-header').html(renderHeaderHtml());
  $('#bc-config').html(renderConfigHtml());
  updateUI();
}

function updateUI() {
  try {
    $('#bc-sidebar').html(renderFontCard() + renderPageCard() + renderLayerCard());
    $('#bc-main').html(renderEditor());
    setTimeout(updateCanvasInfo, 100);
  } catch(e) {
    console.error('updateUI error:', e);
    $('#bc-sidebar').html('<div class="settings-card" style="padding:12px;color:var(--danger);">渲染错误: '+e.message+'</div>');
  }
}

function renderHeaderHtml() {
  let pushedMode = bcConfig.pushed_state || "";
  let statusText = pushedMode 
      ? ('<span class="badge badge-online" style="margin-left: 10px; font-size: 12px; vertical-align: middle;">已推送: ' + (pushedMode === 'before' ? '赛前' : pushedMode === 'contesting' ? '赛中' : '赛后') + '</span>') 
      : '<span class="badge badge-offline" style="margin-left: 10px; font-size: 12px; vertical-align: middle;">未推送</span>';

  let h = '<div class="page-header">' +
      '<div style="display:flex;align-items:center;"><h2 class="section-title" style="margin:0;">广播管理</h2>' + statusText + '</div>' +
      '<div style="display:flex;gap:6px;">' +
      '<button class="btn btn-sm btn-outline" onclick="openPreview()">预览</button>' +
      '<button class="btn btn-sm btn-outline" onclick="syncReset()" title="复位同步时钟">复位</button>' +
      '<button class="btn btn-sm btn-primary" onclick="pushToDevices()">推送</button>' +
      '<button class="btn btn-sm btn-danger" onclick="pushKillBroadcast()">关闭广播</button>' +
      '</div></div>';
  h += '<div style="display:flex;gap:6px;margin-bottom:10px;">' + mt("before","赛前") + mt("contesting","赛中") + mt("after","赛后") + '</div>';
  return h;
}

function syncReset() {
  if(!confirm("复位同步时钟？所有展示端将从第一页重新开始轮播。")) return;
  $.ajax({url:"/api/broadcast/config",method:"PUT",contentType:"application/json",
    data:JSON.stringify({sync_reset:bcMode}),
    success:function(){alert("已复位，展示端将在下次轮询时同步。");}});
}

function renderConfigHtml() {
  return '<div class="settings-card" style="padding:10px;"><h4 style="font-size:13px;margin-bottom:6px;">倒计时</h4><div style="display:flex;gap:6px;"><input id="countdown-target" placeholder="2026-06-16T14:00:00" value="'+esc(bcConfig.countdown_target||'')+'" style="flex:1;font-size:12px;"><button class="btn btn-sm btn-primary" onclick="saveCfg(\'countdown_target\',$(\'#countdown-target\').val().trim())">保存</button></div></div>'+
  '<div class="settings-card" style="padding:10px;"><h4 style="font-size:13px;margin-bottom:6px;">推送地址</h4><div style="display:flex;gap:6px;"><input id="broadcast-base-url" placeholder="http://icpc-server.local:8082" value="'+esc(bcConfig.base_url||'')+'" style="flex:1;font-size:12px;"><button class="btn btn-sm btn-primary" onclick="saveCfg(\'base_url\',$(\'#broadcast-base-url\').val().trim())">保存</button></div></div>';
}

function mt(m,label){ return `<button style="${m===bcMode?'background:var(--accent);color:#fff;':''}" class="btn btn-sm btn-outline" onclick="switchMode('${m}')">${label}</button>`; }
function switchMode(m){ bcMode=m; bcSelPage=null; bcSelItem=null; loadPages(); }

// ==================== SIDEBAR ====================
function renderFontCard() {
  var rows=bcFonts.map(f => {
    var act=bcConfig.active_font===f.filename?'<span class="badge badge-online" style="font-size:9px;">激活</span>':`<button class="btn btn-sm" style="font-size:9px;padding:1px 5px;" onclick="activateFont('${f.filename}')">激活</button>`;
    return `<div style="display:flex;justify-content:space-between;padding:3px 0;border-bottom:1px solid var(--border);font-size:11px;"><span>${esc(f.name)} <small style="color:var(--text-secondary)">.${esc(f.format)}</small>${act}</span><button class="btn btn-sm btn-danger" style="font-size:9px;padding:1px 5px;" onclick="deleteFont(${f.id})">删</button></div>`;
  }).join("")||'<div style="font-size:11px;color:var(--text-secondary);padding:4px;">暂无字体</div>';
  return `<div class="settings-card" style="padding:8px;margin-bottom:8px;"><h4 style="font-size:13px;margin-bottom:4px;">字体</h4><input type="file" id="font-file" accept=".ttf,.woff,.woff2" style="font-size:11px;width:100%;margin-bottom:3px;"><input type="text" id="font-name" placeholder="名称(可选)" style="width:100%;margin-bottom:3px;font-size:11px;padding:3px 5px;"><button class="btn btn-primary btn-sm" style="width:100%;font-size:11px;" onclick="uploadFont()">上传</button><div style="margin-top:4px;">${rows}</div></div>`;
}

function renderPageCard() {
  var rows=bcPages.map((p,i) => {
    var sel=bcSelPage===p.id?'border-color:var(--accent)!important;background:rgba(13,110,253,0.06);':'';
    return `<div style="${sel}cursor:pointer;margin-bottom:3px;" class="device-card" onclick="selectPage(${p.id})"><div class="device-card-name" style="font-size:12px;">${i+1}. ${esc(p.title||'未命名')}</div><div class="device-card-meta">${p.duration_ms/1000}s · ${(p.items||[]).length} 元素</div></div>`;
  }).join("")||'<div class="empty-state" style="padding:10px;font-size:12px;">暂无页面</div>';
  return `<div class="settings-card" style="padding:8px;margin-bottom:8px;"><div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:4px;"><h4 style="font-size:13px;margin:0;">页面</h4><button class="btn btn-sm btn-primary" style="font-size:10px;padding:2px 7px;" onclick="addPage()">+ 添加</button></div><div style="max-height:220px;overflow-y:auto;">${rows}</div></div>`;
}

// 图层卡片
function renderLayerCard() {
  if(!bcSelPage) return '';
  var page = bcPages.find(p => p.id === bcSelPage);
  if(!page || !page.items || page.items.length === 0) return '';

  let items = [...page.items].sort((a,b) => (b.z_index||10) - (a.z_index||10));
  let rows = items.map(it => {
    let isSel = bcSelItem === it.id;
    let bg = isSel ? 'rgba(13,110,253,0.15)' : 'transparent';
    let bcol = isSel ? 'var(--accent)' : 'var(--border)';
    return `<div style="display:flex;justify-content:space-between;align-items:center;padding:6px;margin-bottom:4px;border:1px solid ${bcol};border-radius:4px;background:${bg};cursor:pointer;" onclick="selectItem(${it.id})">
            <span style="font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;width:130px;" title="${esc(it.content)}"><span style="color:#888;">[Z:${it.z_index||10}]</span> ${it.item_type}: ${esc(it.content).substring(0,10)}</span>
            <div style="display:flex;gap:4px;">
                <button class="btn btn-sm btn-outline" style="padding:0 6px;font-size:12px;" onclick="changeZ(${it.id}, 1, event)">+</button>
                <button class="btn btn-sm btn-outline" style="padding:0 6px;font-size:12px;" onclick="changeZ(${it.id}, -1, event)">-</button>
            </div>
        </div>`;
  }).join('');

  return `<div class="settings-card" style="padding:8px;">
            <h4 style="font-size:13px;margin-bottom:6px;">图层列表</h4>
            <div style="max-height:200px;overflow-y:auto;padding-right:4px;">${rows}</div>
        </div>`;
}

function selectPage(id){ bcSelPage=id; bcSelItem=null; updateUI(); }
function selectItem(id) { bcSelItem=id; updateUI(); }

function changeZ(id, delta, e) {
  e.stopPropagation();
  var page = bcPages.find(p => p.id === bcSelPage);
  var it = page.items.find(x => x.id === id);
  if(!it) return;
  it.z_index = (it.z_index || 10) + delta;

  $.ajax({
    url: "/api/broadcast/items/" + id, method: "PUT", contentType: "application/json",
    data: JSON.stringify(it), success: function() { loadPages(); }
  });
  updateUI();
}

function addPage(){ $.ajax({url:"/api/broadcast/pages",method:"POST",contentType:"application/json",data:JSON.stringify({mode:bcMode,title:"新页面",sort_order:bcPages.length,duration_ms:10000,bg_color:"#000000",transition:"fade"}),success:function(p){bcSelPage=p.id;loadPages();}}); }

function uploadFont(){ var f=document.getElementById("font-file").files[0];if(!f){alert("选文件");return;} var fd=new FormData();fd.append("file",f);var n=document.getElementById("font-name").value.trim();if(n)fd.append("name",n); $.ajax({url:"/api/broadcast/fonts",method:"POST",data:fd,processData:false,contentType:false,success:function(){loadBroadcastAdmin();},error:function(){alert("失败");}}); }
function activateFont(fn){bcConfig.active_font=fn;$.ajax({url:"/api/broadcast/config",method:"PUT",contentType:"application/json",data:JSON.stringify({active_font:fn}),success:function(){loadBroadcastAdmin();}});}
function deleteFont(id){if(!confirm("删？"))return;$.ajax({url:"/api/broadcast/fonts/"+id,method:"DELETE",success:function(){loadBroadcastAdmin();}});}

// ==================== EDITOR & CANVAS ====================
function renderEditor() {
  if(!bcSelPage) return '<div class="settings-card" style="padding:24px;text-align:center;color:var(--text-secondary);">选择左侧页面开始编辑</div>';
  var page = bcPages.find(p => p.id === bcSelPage);
  if(!page) return '<div class="empty-state">页面不存在</div>';

  var bg = page.bg_color || '#000000';
  var h = '';

  h += `<div style="display:flex;gap:6px;align-items:center;margin-bottom:8px;flex-wrap:wrap;">
         <input id="page-title" value="${esc(page.title)}" style="width:110px;font-size:12px;padding:4px;" placeholder="标题">
         <label style="font-size:11px;">时长(s)</label><input id="page-duration" type="number" value="${page.duration_ms/1000}" style="width:50px;font-size:12px;padding:4px;">
         <input id="page-bg" type="color" value="${bg}" style="width:28px;height:24px;padding:1px;" title="背景色">
         <button class="btn btn-sm btn-primary" onclick="savePage()">保存</button>
         <button class="btn btn-sm btn-danger" onclick="if(confirm('删除此页？')){$.ajax({url:'/api/broadcast/pages/${page.id}',method:'DELETE',success:()=>{bcSelPage=null;loadPages();}})}">删页</button>
         </div>`;

  // 移除了滚动通告，加入了时钟
  h += `<div style="display:flex;gap:6px;margin-bottom:8px;padding:8px;background:rgba(255,255,255,0.05);border:1px solid #333;border-radius:6px;flex-wrap:wrap;">
         <button class="btn btn-sm btn-outline" onclick="addItem('text')">+ 文字</button>
         <button class="btn btn-sm btn-outline" onclick="addItem('image')">+ 图片</button>
         <button class="btn btn-sm btn-outline" onclick="addItem('clock')">+ 时钟</button>
         </div>`;

  h += `<div id="bc-canvas" style="position:relative;width:100%;aspect-ratio:16/9;background:${bg};border:1px solid #444;border-radius:6px;overflow:hidden;box-shadow:0 8px 24px rgba(0,0,0,0.6);">
    <div class="bc-guide bc-guide-x" style="position:absolute;left:0;right:0;top:50%;height:0;border-top:1px dashed rgba(255,255,255,0.06);pointer-events:none;z-index:1;transition:border-color 0.2s;"></div>
    <div class="bc-guide bc-guide-y" style="position:absolute;top:0;bottom:0;left:50%;width:0;border-left:1px dashed rgba(255,255,255,0.06);pointer-events:none;z-index:1;transition:border-color 0.2s;"></div>`;
  (page.items || []).forEach(it => { h += renderItem(it); });
  h += '</div>';
  h += '<div id="bc-canvas-info" style="font-size:10px;color:var(--text-secondary);margin-top:4px;text-align:right;">画布: <span id="bc-canvas-w">--</span>px × <span id="bc-canvas-h">--</span>px (16:9) | 字号为 vh% 等比缩放</div>';

  if(bcSelItem) {
    var it = (page.items || []).find(x => x.id === bcSelItem);
    if(it) {
      h += '<div class="settings-card" style="margin-top:12px;padding:12px;border-top:3px solid var(--accent);">';
      h += '<h4 style="font-size:13px;margin-top:0;margin-bottom:10px;">编辑属性</h4>';
      h += renderProps(it);
      h += '</div>';
    }
  } else {
    h += '<div style="margin-top:12px;text-align:center;font-size:12px;color:var(--text-secondary);padding:10px;">在画布或左侧图层列表点击元素进行编辑</div>';
  }

  return h;
}

function savePage(){ $.ajax({url:"/api/broadcast/pages/"+bcSelPage,method:"PUT",contentType:"application/json",data:JSON.stringify({title:$("#page-title").val(),duration_ms:(parseInt($("#page-duration").val())||10)*1000,bg_color:$("#page-bg").val(),transition:"fade"}),success:function(){loadPages();}}); }

// ==================== CANVAS ITEM ====================
function renderItem(it) {
  var selCls = (bcSelItem === it.id) ? ' selected' : '';
  var inner = '';
  var alignMap = { left: 'flex-start', center: 'center', right: 'flex-end' };
  var align = alignMap[it.text_align || 'center'];
  // 字号存 vh%，画布上按画布高度换算 px: px = vh% * canvasHeight / 100
  var canvasH = 450; // default 16:9 at some width
  var c = document.getElementById('bc-canvas');
  if(c) canvasH = c.offsetHeight || 450;
  var fontSizePx = (parseFloat(it.font_size) || 3) * canvasH / 100 + 'px';

  switch(it.item_type) {
    case "text": inner = `<div style="width:100%;height:100%;display:flex;align-items:center;justify-content:${align};text-align:${it.text_align||'center'};white-space:pre-wrap;line-height:1.2;">${esc(it.content||"文字")}</div>`; break;
    case "image": inner = `<img src="${it.content}" style="width:100%;height:100%;object-fit:contain;">`; break;
    case "clock": inner = `<div style="width:100%;height:100%;display:flex;align-items:center;justify-content:${align};white-space:nowrap;font-family:monospace;" class="bc-clock-val">00:00:00</div>`; break;
  }

  var handles = (bcSelItem === it.id) ? `<div class="bc-rs rs-nw"></div><div class="bc-rs rs-n"></div><div class="bc-rs rs-ne"></div><div class="bc-rs rs-e"></div><div class="bc-rs rs-se"></div><div class="bc-rs rs-s"></div><div class="bc-rs rs-sw"></div><div class="bc-rs rs-w"></div>` : '';

  return `<div class="bc-item-wrapper${selCls}" data-id="${it.id}"
            data-x="${it.pos_x}" data-y="${it.pos_y}" data-w="${it.width}" data-h="${it.height}"
            style="left:${it.pos_x}%; top:${it.pos_y}%; width:${it.width}%; height:${it.height}%;
            font-size:${fontSizePx}; color:${it.font_color}; font-weight:${it.font_weight};
            background:${it.bg_color!=='transparent'?it.bg_color:''}; border-radius:${it.border_radius||'0'}; z-index:${it.z_index||10};">
            <div class="bc-item">${inner}</div>${handles}
        </div>`;
}

// ==================== INTERACT & EVENTS BINDING ====================
let eventsBound = false;
function updateCanvasInfo() {
  var c = document.getElementById('bc-canvas'); if(!c) return;
  $('#bc-canvas-w').text(Math.round(c.offsetWidth));
  $('#bc-canvas-h').text(Math.round(c.offsetHeight));
}
function bindBroadcastEvents() {
  if (eventsBound) return;
  eventsBound = true;
  // Track canvas pixel size for WYSIWYG font sizing.
  $(window).on('resize.bc', updateCanvasInfo);
  // Observe canvas size changes.
  setInterval(function(){
    var c = document.getElementById('bc-canvas'); if(c) updateCanvasInfo();
  }, 2000);

  const $doc = $(document);

  $doc.on('mousedown', '#bc-canvas', function(e) {
    if (e.target.id === 'bc-canvas') { selectItem(null); }
  });

  $doc.on('mousedown', '.bc-item-wrapper', function(e) {
    e.stopPropagation();
    let id = parseInt($(this).attr('data-id'));
    if (bcSelItem !== id) { selectItem(id); }
  });

  interact('.bc-item-wrapper')
      .draggable({
        inertia: false,
        modifiers: [],
        listeners: {
          move(e) {
            let target = e.target;
            let rect = document.getElementById('bc-canvas').getBoundingClientRect();
            let w = parseFloat(target.getAttribute('data-w')) || 10;
            let h = parseFloat(target.getAttribute('data-h')) || 10;
            let snap = 0.3; // soft snap threshold

            let x = (parseFloat(target.getAttribute('data-x')) || 0) + (e.dx / rect.width) * 100;
            let y = (parseFloat(target.getAttribute('data-y')) || 0) + (e.dy / rect.height) * 100;

            // Snap: center alignment
            let cx = x + w/2, cy = y + h/2;
            let sx = false, sy = false;
            if (Math.abs(cx - 50) < snap) { x = 50 - w/2; sx = true; }
            if (Math.abs(cy - 50) < snap) { y = 50 - h/2; sy = true; }
            // Edge snap
            if (Math.abs(x) < snap) { x = 0; }
            if (Math.abs(x + w - 100) < snap) { x = 100 - w; }
            if (Math.abs(y) < snap) { y = 0; }
            if (Math.abs(y + h - 100) < snap) { y = 100 - h; }

            // Soft flash guides on alignment (not just snap)
            $('.bc-guide-x').css('border-color', sy?'rgba(13,110,253,0.35)':'rgba(255,255,255,0.06)');
            $('.bc-guide-y').css('border-color', sx?'rgba(13,110,253,0.35)':'rgba(255,255,255,0.06)');

            target.style.left = x + '%'; target.style.top = y + '%';
            target.setAttribute('data-x', x); target.setAttribute('data-y', y);

            if (bcSelItem === parseInt(target.getAttribute('data-id'))) {
              $('#ip-x').val(rd(x)); $('#ip-y').val(rd(y));
            }
          },
          end(e) { savePos(e.target); $('.bc-guide-x,.bc-guide-y').css('border-color','rgba(255,255,255,0.06)'); }
        }
      })
      .resizable({
        // 【修复核心】：把四个角同时绑定到相邻的两条边上，让 interact.js 自动接管斜向差值
        edges: {
          top: '.rs-n, .rs-nw, .rs-ne',
          left: '.rs-w, .rs-nw, .rs-sw',
          bottom: '.rs-s, .rs-sw, .rs-se',
          right: '.rs-e, .rs-ne, .rs-se'
        },
        modifiers: [],
        listeners: {
          move(e) {
            let target = e.target;
            let canvasRect = document.getElementById('bc-canvas').getBoundingClientRect();

            let x = parseFloat(target.getAttribute('data-x')) || 0;
            let y = parseFloat(target.getAttribute('data-y')) || 0;
            let w = parseFloat(target.getAttribute('data-w')) || 10;
            let h = parseFloat(target.getAttribute('data-h')) || 10;

            // 使用增量进行精准数学计算
            if (e.edges.left) {
              let dx = (e.deltaRect.left / canvasRect.width) * 100;
              x += dx; w -= dx;
            }
            if (e.edges.right) {
              w += (e.deltaRect.right / canvasRect.width) * 100;
            }
            if (e.edges.top) {
              let dy = (e.deltaRect.top / canvasRect.height) * 100;
              y += dy; h -= dy;
            }
            if (e.edges.bottom) {
              h += (e.deltaRect.bottom / canvasRect.height) * 100;
            }

            // 限制最小宽高，防止反转或压缩到消失
            w = Math.max(1, w);
            h = Math.max(1, h);

            target.style.left = x + '%'; target.style.top = y + '%';
            target.style.width = w + '%'; target.style.height = h + '%';

            target.setAttribute('data-x', x); target.setAttribute('data-y', y);
            target.setAttribute('data-w', w); target.setAttribute('data-h', h);

            if (bcSelItem === parseInt(target.getAttribute('data-id'))) {
              $('#ip-x').val(rd(x)); $('#ip-y').val(rd(y));
              $('#ip-w').val(rd(w)); $('#ip-h').val(rd(h));
            }
          },
          end(e) { savePos(e.target); $('.bc-guide-x,.bc-guide-y').css('border-color','rgba(255,255,255,0.06)'); }
        }
      });
}

function rd(v) { return Math.round((parseFloat(v) || 0) * 100) / 100; }

function savePos(el) {
  let id = parseInt(el.getAttribute('data-id'));
  let x = rd(el.getAttribute('data-x')); let y = rd(el.getAttribute('data-y'));
  let w = rd(el.getAttribute('data-w')); let h = rd(el.getAttribute('data-h'));

  let page = bcPages.find(p => p.id === bcSelPage);
  if(page) {
    let it = page.items.find(i => i.id === id);
    if(it) { it.pos_x = x; it.pos_y = y; it.width = w; it.height = h; }
  }

  $.ajax({
    url: '/api/broadcast/items/'+id+'/position', method: 'PATCH', contentType: 'application/json',
    data: JSON.stringify({ pos_x: x, pos_y: y, width: w, height: h })
  });
}

// ==================== ADD ITEM / PROPS ====================
function addItem(type){
  var defs={
    text:{content:"新建文字\n支持换行",w:30,h:12,fs:"3"},
    image:{content:"",w:30,h:18,fs:"2",needImg:true},
    clock:{content:"clock",w:20,h:8,fs:"5",fw:"bold",fc:"#ffffff"}
  };
  var d=defs[type]||defs.text;
  if(d.needImg){
    var inp=document.createElement("input");inp.type="file";inp.accept="image/*";
    inp.onchange=function(){var f=inp.files[0];if(!f)return;var fd=new FormData();fd.append("file",f);
      $.ajax({url:"/api/broadcast/images/upload",method:"POST",data:fd,processData:false,contentType:false,
        success:function(r){createItem(type,r.url,d);},error:function(){alert("上传失败");}});};
    inp.click();return;
  }
  createItem(type,d.content,d);
}

function createItem(type,content,d){
  $.ajax({url:"/api/broadcast/items",method:"POST",contentType:"application/json",
    data:JSON.stringify({page_id:bcSelPage,item_type:type,content:content||d.content,
      pos_x:Math.max(0,50-(d.w||25)/2),pos_y:Math.max(0,50-(d.h||8)/2),width:d.w||25,height:d.h||8,
      font_size:d.fs||"3",font_color:d.fc||"#ffffff",font_weight:d.fw||"normal",
      text_align:"center",bg_color:"transparent",border_radius:"0",animation:"",z_index:10,extra_json:d.ex||"{}"}),
    success:function(){loadPages();}});
}

function renderProps(it){
  var h = '<div style="display:grid;grid-template-columns:repeat(4, 1fr);gap:10px;font-size:11px;">';

  // 如果是时钟组件，禁用内容编辑框
  if(it.item_type === 'clock') {
    h += pf(4, '内容', `<div style="width:100%;height:50px;font-size:12px;padding:4px;border:1px solid var(--border);border-radius:4px;background:rgba(255,255,255,0.05);color:#888;display:flex;align-items:center;">实时系统时钟 (不可编辑内容)</div><input type="hidden" id="ip-content" value="clock">`);
  } else {
    h += pf(4, '内容 (支持回车换行)', `<textarea id="ip-content" style="width:100%;height:50px;font-size:12px;padding:4px;border:1px solid var(--border);border-radius:4px;resize:vertical;">${esc(it.content)}</textarea>`);
  }

  h += pf(1, 'X (%)', '<input id="ip-x" type="number" value="'+it.pos_x+'" step="0.5" style="width:100%;">');
  h += pf(1, 'Y (%)', '<input id="ip-y" type="number" value="'+it.pos_y+'" step="0.5" style="width:100%;">');
  h += pf(1, '宽 (%)', '<input id="ip-w" type="number" value="'+it.width+'" step="0.5" style="width:100%;">');
  h += pf(1, '高 (%)', '<input id="ip-h" type="number" value="'+it.height+'" step="0.5" style="width:100%;">');
  h += pf(2, '字号 (vh%)', '<input id="ip-fs" value="'+esc(it.font_size)+'" style="width:100%;"><span style="font-size:10px;color:var(--text-secondary);">屏幕高度的百分比</span>');
  h += pf(1, '字体颜色', '<input id="ip-fc" type="color" value="'+it.font_color+'" style="width:100%;height:26px;padding:0;">');
  var bgTrans = it.bg_color === 'transparent';
  h += '<div style="grid-column:span 1;display:flex;flex-direction:column;gap:4px;">'+
    '<label style="font-size:11px;color:var(--text-secondary);font-weight:bold;">背景</label>'+
    '<div style="display:flex;align-items:center;gap:4px;">'+
      '<input id="ip-bg" type="color" value="'+(bgTrans?'#000000':it.bg_color)+'" style="width:36px;height:26px;padding:0;'+(bgTrans?'opacity:0.3':'')+'">'+
      '<label style="font-size:10px;display:flex;align-items:center;gap:2px;cursor:pointer;white-space:nowrap;">'+
        '<input id="ip-bg-trans" type="checkbox" '+(bgTrans?'checked':'')+' onchange="$(\'#ip-bg\').prop(\'disabled\',this.checked).css(\'opacity\',this.checked?0.3:1);"> 透明</label>'+
    '</div></div>';
  h += pf(1, '字重', '<select id="ip-fw" style="width:100%;"><option value="normal"'+(it.font_weight=="normal"?" selected":"")+'>普通</option><option value="bold"'+(it.font_weight=="bold"?" selected":"")+'>加粗</option></select>');
  h += pf(1, '对齐', '<select id="ip-ta" style="width:100%;"><option value="left"'+(it.text_align=="left"?" selected":"")+'>左</option><option value="center"'+(it.text_align=="center"?" selected":"")+'>中</option><option value="right"'+(it.text_align=="right"?" selected":"")+'>右</option></select>');
  h += pf(1, '动画', '<select id="ip-an" style="width:100%;"><option value="">无</option><option value="fadeIn"'+(it.animation=="fadeIn"?" selected":"")+'>淡入</option><option value="slideUp"'+(it.animation=="slideUp"?" selected":"")+'>上滑</option><option value="pulse"'+(it.animation=="pulse"?" selected":"")+'>脉冲</option></select>');
  h += pf(1, '圆角', '<input id="ip-br" value="'+esc(it.border_radius)+'" style="width:100%;">');

  h += '<div style="grid-column:1/-1;display:flex;justify-content:space-between;align-items:center;margin-top:8px;">'+
      '<button class="btn btn-sm btn-primary" onclick="saveProps('+it.id+')">保存修改</button>'+
      '<button class="btn btn-sm btn-danger" onclick="if(confirm(\'删除该元素？\')){$.ajax({url:\'/api/broadcast/items/'+it.id+'\', method:\'DELETE\', success:()=>{bcSelItem=null;loadPages();}})}">删除元素</button>'+
      '</div></div>';
  return h;
}
function pf(n, label, input) {
  return `<div style="grid-column:span ${n};display:flex;flex-direction:column;gap:4px;">
            <label style="font-size:11px;color:var(--text-secondary);font-weight:bold;">${label}</label>${input}</div>`;
}

function saveProps(id){
  var page=bcPages.find(p=>p.id===bcSelPage);
  var it=(page?page.items:[]).find(x=>x.id===id)||{};
  $.ajax({url:"/api/broadcast/items/"+id,method:"PUT",contentType:"application/json",
    data:JSON.stringify({page_id:bcSelPage,item_type:it.item_type||"text",
      content:$("#ip-content").val(),pos_x:parseFloat($("#ip-x").val())||0,pos_y:parseFloat($("#ip-y").val())||0,
      width:parseFloat($("#ip-w").val())||20,height:parseFloat($("#ip-h").val())||10,
      font_size:$("#ip-fs").val(),font_color:$("#ip-fc").val(),font_weight:$("#ip-fw").val(),
      text_align:$("#ip-ta").val(),bg_color:$("#ip-bg-trans").is(":checked")?"transparent":$("#ip-bg").val(),border_radius:$("#ip-br").val(),
      animation:$("#ip-an").val(),z_index:it.z_index||10,extra_json:it.extra_json||"{}"}),
    success:function(){loadPages();}});
}

// ==================== CONFIG & UTILS ====================
function saveCfg(key,val){ var d={};d[key]=val;if(key==='base_url')bcConfig.base_url=val; $.ajax({url:"/api/broadcast/config",method:"PUT",contentType:"application/json",data:JSON.stringify(d),success:function(){alert("已保存");}}); }
function openPreview(){window.open("/broadcast/"+bcMode,"_blank");}
function pushToDevices(){
  var base=bcConfig.base_url||"http://icpc-server.local:8082",url=base+"/broadcast/"+bcMode,cmd="full-firefox "+url;
  if(!confirm("向目标推送广播？\n命令: "+cmd))return;

  var performPush = function() {
    $.ajax({
      url:"/api/broadcast/config",
      method:"PUT",
      contentType:"application/json",
      data:JSON.stringify({sync_reset:bcMode, pushed_state:bcMode}),
      success:function(){
        execCmd(cmd);
        loadBroadcastAdmin();
      }
    });
  };

  if (bcConfig.pushed_state) {
    execCmd("full-firefox kill");
    setTimeout(performPush, 500);
  } else {
    performPush();
  }
}

function pushKillBroadcast(){
  var cmd="full-firefox kill";
  if(!confirm("关闭广播？"))return;
  $.ajax({
    url:"/api/broadcast/config",
    method:"PUT",
    contentType:"application/json",
    data:JSON.stringify({pushed_state:""}),
    success:function(){
      execCmd(cmd);
      loadBroadcastAdmin();
    }
  });
}
function execCmd(cmd){ var body={target_type:"broadcast",command:cmd}; $.ajax({url:"/api/commands",method:"POST",contentType:"application/json",data:JSON.stringify(body),success:function(){alert("已派发");}}); }
function esc(s){return escapeHtml(s);}